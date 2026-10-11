// claude generates harness config into one state dir, then execs claude, omp, codex, rig or opencode.
//
//	claude [--claude|--omp|--codex|--rig|--opencode|--oc] [--model ROLE_OR_ID] [harness args...]
//	claude sync | claude doctor
//	claude fresh            (inside a rig seat: relaunch this seat with a blank context)
//	claude lease acquire <resource> <cap> <holder> | release <resource> <holder> | list <resource>
//
// Default harness: $HARNESS_DEFAULT, else claude. opencode runs vanilla, no generated config.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Quentin-M/harness/internal/harness"
	_ "github.com/Quentin-M/harness/internal/harness/claude"
	_ "github.com/Quentin-M/harness/internal/harness/codex"
	_ "github.com/Quentin-M/harness/internal/harness/omp"
	_ "github.com/Quentin-M/harness/internal/harness/opencode"
	_ "github.com/Quentin-M/harness/internal/harness/rig"
	"github.com/Quentin-M/harness/internal/layer"
	"github.com/Quentin-M/harness/internal/lease"
	"github.com/Quentin-M/harness/internal/memory"
	"github.com/Quentin-M/harness/internal/model"
	"github.com/Quentin-M/harness/internal/sys"
)

// dryRunKeys are the launch env vars a dry run reports, in order.
var dryRunKeys = []string{"HOME", "PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "PI_FORCE_IMAGE_PROTOCOL", "CLAUDE_CONFIG_DIR",
	"CODEX_HOME", "OPENRIG_HOME", "OPENRIG_NO_KERNEL", "OPENRIG_RUNTIME_CODEX_HOOKS_ENABLED", "ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN"}

var harnessFlags = map[string]string{"--claude": "claude", "--omp": "omp", "--codex": "codex", "--rig": "rig",
	"--opencode": "opencode", "--oc": "opencode"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "harness:") {
			msg = "harness: " + msg
		}
		fmt.Fprintln(os.Stderr, msg)
		if errors.Is(err, errFull) {
			os.Exit(75)
		}
		os.Exit(1)
	}
}

type args struct {
	harness, model string
	rest           []string
	cmd            string // sync, doctor, lease, fresh or "" for a launch
}

func parse(argv []string, env sys.Env) args {
	a := args{harness: env.Get("HARNESS_DEFAULT")}
	if a.harness == "" {
		a.harness = "claude"
	}
	i, dashdash := 0, false
	for ; i < len(argv); i++ {
		arg := argv[i]
		if h, ok := harnessFlags[arg]; ok {
			a.harness = h
			if h == "rig" { // everything after --rig is rig's own argv
				i++
				dashdash = true
				break
			}
		} else if arg == "--model" && i+1 < len(argv) {
			i++
			a.model = argv[i]
		} else if arg == "--" {
			i++
			dashdash = true
			break
		} else {
			break
		}
	}
	a.rest = argv[i:]
	if len(a.rest) > 0 && !dashdash && slices.Contains([]string{"sync", "doctor", "lease", "fresh"}, a.rest[0]) {
		a.cmd = a.rest[0]
	}
	return a
}

func run(argv []string) error {
	env := sys.OSEnv()
	a := parse(argv, env)
	switch a.cmd {
	case "lease":
		return leaseCmd(env, a.rest[1:])
	case "fresh":
		return fresh(env)
	}
	root, err := harnessRoot(env)
	if err != nil {
		return err
	}
	ctx := &harness.Ctx{Root: root, State: stateDir(env), Env: env.Clone(), Sys: sys.New(env),
		DryRun: env.Get("HARNESS_DRY_RUN") == "1"}
	ctx.Cwd, _ = os.Getwd()
	ctx.Layers = layer.Discover(root, env)
	if ctx.Models, err = model.Load(env); err != nil {
		return err
	}
	switch a.cmd {
	case "sync":
		for _, h := range harness.Synced() {
			hh, _ := harness.Get(h)
			if err := hh.Sync(ctx); err != nil {
				return err
			}
		}
		fmt.Println(ctx.State)
		return nil
	case "doctor":
		doctor(ctx)
		return nil
	}
	return launch(ctx, a)
}

func launch(ctx *harness.Ctx, a args) error {
	h, ok := harness.Get(a.harness)
	if !ok {
		return fmt.Errorf("unknown harness %q", a.harness)
	}
	if err := h.Env(ctx); err != nil {
		return err
	}
	if err := h.Sync(ctx); err != nil {
		return err
	}
	if a.harness != "opencode" {
		// claude and codex hooks and omp/rig seats all read this bank, so every harness but opencode needs it
		if !ctx.DryRun {
			if err := memory.Ensure(ctx.State, ctx.Sys, ctx.Env); err != nil {
				return err
			}
		}
	}
	argv, err := h.Argv(ctx, a.model, a.rest)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		fmt.Println(string(dryRun(argv, ctx.Env)))
		return nil
	}
	return syscall.Exec(argv[0], argv, ctx.Env.Environ())
}

// errFull exits 75 (EX_TEMPFAIL) so a seat can tell "try the light run" from a real error.
var errFull = errors.New("full")

// leaseCmd caps concurrent heavy runs on a shared resource; leases live under $OPENRIG_HOME.
func leaseCmd(env sys.Env, argv []string) error {
	ttl := 4 * time.Hour
	if v := env.Get("HARNESS_LEASE_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("HARNESS_LEASE_TTL: %w", err)
		}
		ttl = d
	}
	dir := env.Get("OPENRIG_HOME")
	if dir == "" {
		dir = filepath.Join(stateDir(env), "rig")
	}
	s := &lease.Store{Dir: filepath.Join(dir, "leases"), TTL: ttl, Now: time.Now}
	usage := errors.New("usage: lease acquire <resource> <cap> <holder> | release <resource> <holder> | list <resource>")
	if len(argv) == 0 {
		return usage
	}
	switch {
	case argv[0] == "acquire" && len(argv) == 4:
		n, err := strconv.Atoi(argv[2])
		if err != nil {
			return fmt.Errorf("lease cap %q: %w", argv[2], err)
		}
		ok, err := s.Acquire(argv[1], n, argv[3])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("lease %s: all %d held: %w", argv[1], n, errFull)
		}
		return nil
	case argv[0] == "release" && len(argv) == 3:
		return s.Release(argv[1], argv[2])
	case argv[0] == "list" && len(argv) == 2:
		ls, err := s.List(argv[1])
		for _, l := range ls {
			fmt.Printf("%s\t%s\n", l.Holder, l.Since.Format(time.RFC3339))
		}
		return err
	}
	return usage
}

// fresh relaunches the calling seat with a blank context. `rig seat launch --stop` kills the seat's
// own pane, so the relaunch runs in a detached session after a delay that lets this turn end.
func fresh(env sys.Env) error {
	seat := env.Get("OPENRIG_SESSION_NAME")
	if seat == "" {
		return errors.New("fresh: OPENRIG_SESSION_NAME unset; run it inside a rig seat")
	}
	delay := 5 * time.Second
	if v := env.Get("HARNESS_FRESH_DELAY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("HARNESS_FRESH_DELAY: %w", err)
		}
		delay = d
	}
	rig := sys.New(env).Which("rig")
	if rig == "" {
		return errors.New("fresh: rig not on PATH")
	}
	script := fmt.Sprintf("sleep %.3f; exec %q seat launch %q --fresh --stop --reason 'fresh context for the next row'",
		delay.Seconds(), rig, seat)
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = env.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fresh %s: %w", seat, err)
	}
	fmt.Printf("%s relaunches fresh in %s; end your turn now\n", seat, delay)
	return cmd.Process.Release()
}

// dryRun is {"argv": [...], "env": {key: value or null}}.
func dryRun(argv []string, env sys.Env) []byte {
	var b bytes.Buffer
	av, _ := json.Marshal(argv)
	b.WriteString(`{"argv": `)
	b.Write(av)
	b.WriteString(`, "env": {`)
	for i, k := range dryRunKeys {
		if i > 0 {
			b.WriteString(", ")
		}
		kj, _ := json.Marshal(k)
		b.Write(kj)
		b.WriteString(": ")
		if v, ok := env.Lookup(k); ok {
			vj, _ := json.Marshal(v)
			b.Write(vj)
		} else {
			b.WriteString("null")
		}
	}
	b.WriteString("}}")
	return b.Bytes()
}

// ── paths ────────────────────────────────────────────────────────────────────

// harnessRoot is HARNESS_ROOT, which go/cmd/claude/main.sh sets.
func harnessRoot(env sys.Env) (string, error) {
	if r := env.Get("HARNESS_ROOT"); r != "" {
		return resolve(expand(r, env)), nil
	}
	return "", errors.New("HARNESS_ROOT unset; run go/cmd/claude/main.sh")
}

func stateDir(env sys.Env) string {
	if s := env.Get("HARNESS_STATE"); s != "" {
		return resolve(expand(s, env))
	}
	base := env.Get("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(home(env), ".local/state")
	}
	return filepath.Join(base, "harness")
}

func home(env sys.Env) string {
	if h := env.Get("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func expand(p string, env sys.Env) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return home(env) + p[1:]
	}
	return p
}

// resolve is Python's Path.resolve(strict=False): absolute, symlinks resolved on the existing prefix.
func resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs
	}
	return filepath.Join(resolve(parent), filepath.Base(abs))
}

// ── doctor ───────────────────────────────────────────────────────────────────

func doctor(ctx *harness.Ctx) {
	s, m := ctx.Sys, ctx.Models
	rows := [][2]string{{"root", ctx.Root}, {"state", ctx.State},
		{"gateway", or(m.BaseURL, "MISSING") + " (" + or(m.BaseSrc, "no source") + ")"},
		{"key cmd", or(m.KeyCmd, "MISSING") + " (" + or(m.KeySrc, "no source") + ")"}}
	for i, d := range ctx.Layers.Dirs {
		rows = append(rows, [2]string{fmt.Sprintf("layer %d", i+1), d})
	}
	for _, b := range []string{"omp", "claude", "codex", "bun"} {
		rows = append(rows, [2]string{"bin " + b, or(s.FindBin(b), "MISSING")})
	}
	rows = append(rows, [2]string{"bin opencode", or(s.FindBin("opencode"), "-")})
	rows = append(rows, [2]string{"memory", fmt.Sprintf("%s (mnemopi %s, omp %s)", filepath.Join(ctx.State, "memory/mnemopi.db"),
		or(memory.Installed(ctx.State), "not installed"), or(memory.OmpVersion(s), "none"))})
	rig := filepath.Join(ctx.State, "rig/npm/node_modules/.bin/rig")
	if _, err := os.Stat(rig); err != nil {
		rig = "not installed (first `claude --rig` installs @openrig/cli)"
	}
	rows = append(rows,
		[2]string{"bin rig", rig},
		[2]string{"bin tmux", or(s.Which("tmux"), "MISSING (rig daemon needs tmux)")},
		[2]string{"bin herdr", or(s.Which("herdr"), "-")},
		[2]string{"role default", m.Resolve("omp", "default")},
		[2]string{"role review", m.Resolve("omp", "review")})
	for _, r := range rows {
		fmt.Printf("%-14s %s\n", r[0], r[1])
	}
	fmt.Println("\nnotes")
	fmt.Println("  rig: OPENRIG_NO_KERNEL=1 skips the OpenRig kernel and its operator seats; only pod seats start.")
	fmt.Println("  rig: the OpenRig daemon seeds ~/.claude/skills and ~/.agents/skills (upstream limitation).")
	fmt.Println("  rig: claude-code and codex seats keep the real HOME and read the generated CLAUDE_CONFIG_DIR and CODEX_HOME.")
	fmt.Println("  claude: managed settings in /Library/Application Support/ClaudeCode win over generated settings.json.")
	fmt.Println("  codex: also reads ~/.agents/skills (no CODEX_HOME-relative override).")
	fmt.Println("  codex: approve the memory hooks once with /hooks; codex skips untrusted hooks.")
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
