/**
 * omp command gate: every bash and eval tool call goes to `$HARNESS_JUDGE_GATE judge gate`, which
 * builds the request, asks the local judge, applies the gate rule and words any refusal. The gate
 * never prompts. Anything but a valid reply from it refuses the call. `/classifier off|on|status`
 * switches the gate for the current session only (in memory).
 */
import { spawn } from "node:child_process";

const TIMEOUT_MS = 8000; // the gate answers within 6 s; this is the backstop

/** Run `$HARNESS_JUDGE_GATE <args>` with `input` on stdin; resolves stdout, rejects on any failure. */
function run(args: string[], input: string): Promise<string> {
	return new Promise((resolve, reject) => {
		const gate = process.env.HARNESS_JUDGE_GATE;
		if (!gate) return reject(new Error("HARNESS_JUDGE_GATE is not set"));
		const child = spawn(gate, args);
		let out = "";
		let err = "";
		const timer = setTimeout(() => (child.kill("SIGKILL"), reject(new Error(`no answer within ${TIMEOUT_MS / 1000} s`))), TIMEOUT_MS);
		child.stdout.on("data", d => (out += d));
		child.stderr.on("data", d => (err += d));
		child.stdin.on("error", () => {});
		child.on("error", e => (clearTimeout(timer), reject(e)));
		child.on("close", code => {
			clearTimeout(timer);
			code === 0 ? resolve(out) : reject(new Error(`exit code ${code}: ${err.trim().split("\n").pop() ?? ""}`));
		});
		child.stdin.end(input);
	});
}

const text = (c: any): string => (typeof c === "string" ? c : Array.isArray(c) ? c.filter(p => p?.type === "text").map(p => p.text).join("\n") : "");
const sid = (ctx: any): string => String(ctx.sessionManager?.getSessionId?.() ?? "");

/** The last three user-typed messages of the session, oldest first. */
function userMessages(ctx: any): string[] {
	try {
		const typed = ctx.sessionManager.getBranch().filter((e: any) => e?.type === "message" && e.message?.role === "user" && e.message.attribution === "user");
		return typed.map((e: any) => text(e.message.content)).filter((t: string) => t.trim() !== "").slice(-3);
	} catch {
		return []; // no branch in an isolated context
	}
}

export default function (pi: any) {
	const off = new Set<string>(); // session ids with the gate switched off
	const reset = (_e: unknown, ctx: any) => void off.delete(sid(ctx));
	pi.on("session_start", reset);
	pi.on("session_switch", reset);

	pi.on("tool_call", async (event: any, ctx: any) => {
		if ((event.toolName !== "bash" && event.toolName !== "eval") || off.has(sid(ctx))) return;
		try {
			const reply = JSON.parse(await run(["judge", "gate"], JSON.stringify({ tool: event.toolName, input: event.input ?? {}, cwd: ctx.cwd, userMessages: userMessages(ctx) })));
			if (typeof reply?.block !== "boolean" || typeof reply?.reason !== "string") throw new Error("the gate's reply is not {block, reason}");
			return reply.block ? { block: true, reason: reply.reason } : undefined;
		} catch (e) {
			return { block: true, reason: `Blocked by the local command gate: it failed (${(e as Error).message}). The command did not run; do not retry a reworded or obfuscated version, ask the user in chat if this persists.` };
		}
	});

	pi.registerCommand("classifier", {
		description: "Command gate for this session: off, on or status",
		getArgumentCompletions: (p: string) => ["off", "on", "status"].filter(w => w.startsWith(p.toLowerCase())).map(w => ({ label: w, value: w })),
		handler: async (args: string, ctx: any) => {
			const word = String(args ?? "").trim().toLowerCase();
			if (word === "off") off.add(sid(ctx));
			else if (word === "on") off.delete(sid(ctx));
			const status = await run(["judge", "gate", "--status", off.has(sid(ctx)) ? "off" : "on"], "").catch(e => `Command gate status unavailable (${e.message})`);
			ctx.ui?.notify?.(status.trim(), word === "off" ? "warning" : "info");
		},
	});
}
