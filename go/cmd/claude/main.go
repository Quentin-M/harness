// claude generates harness config into one state dir, then execs claude, omp, codex or opencode.
//
//	claude [--claude|--omp|--codex|--opencode|--oc] [--model ROLE_OR_ID] [harness args...]
//	claude sync | claude doctor
//	claude diagrams <file.md>...   (lint box-drawing diagrams; exit 1 on any issue)
//	claude memory path [dir]       (the project's shared memory dir) | memory context (SessionStart hook)
//	claude dream [--project DIR] [--since DURATION] [--dry-run]   (distil recent sessions into memory)
//	claude dream guard <dir>       (the dream's PreToolUse hook: exit 2 on a tool call outside dir)
//
// Every launch but opencode starts a detached `dream` once the last one is over 20h old.
// Default harness: $HARNESS_DEFAULT, else claude. opencode runs vanilla, no generated config.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Quentin-M/harness/internal/diagram"
	"github.com/Quentin-M/harness/internal/dream"
	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/harness/claude"
	_ "github.com/Quentin-M/harness/internal/harness/codex"
	_ "github.com/Quentin-M/harness/internal/harness/omp"
	_ "github.com/Quentin-M/harness/internal/harness/opencode"
	"github.com/Quentin-M/harness/internal/layer"
	"github.com/Quentin-M/harness/internal/memory"
	"github.com/Quentin-M/harness/internal/model"
	"github.com/Quentin-M/harness/internal/sys"
)

// dryRunKeys are the launch env vars a dry run reports, in order.
var dryRunKeys = []string{"HOME", "PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "PI_FORCE_IMAGE_PROTOCOL", "CLAUDE_CONFIG_DIR",
	"CODEX_HOME", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "LITELLM_URL", model.KeyEnvTag}

var harnessFlags = map[string]string{"--claude": "claude", "--omp": "omp", "--codex": "codex",
	"--opencode": "opencode", "--oc": "opencode"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "harness:") {
			msg = "harness: " + msg
		}
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
}

type args struct {
	harness, model string
	rest           []string
	cmd            string // sync, doctor, diagrams, memory, dream or "" for a launch
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
	if len(a.rest) > 0 && !dashdash && slices.Contains([]string{"sync", "doctor", "diagrams", "memory", "dream"}, a.rest[0]) {
		a.cmd = a.rest[0]
	}
	return a
}

func run(argv []string) error {
	env := sys.OSEnv()
	a := parse(argv, env)
	switch a.cmd {
	case "diagrams":
		return diagrams(a.rest[1:])
	case "memory":
		return memoryCmd(env, a.rest[1:])
	case "dream":
		return dreamCmd(env, a.rest[1:])
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
	// KeyEnv already prefers the shell's pair; an inherited half pair must not reach the helper.
	for _, k := range model.KeyEnvVars {
		if v, ok := ctx.Models.KeyEnv[k]; ok {
			ctx.Env.Set(k, v)
		} else {
			ctx.Env.Unset(k)
		}
	}
	if err := h.Env(ctx); err != nil {
		return err
	}
	if err := h.Sync(ctx); err != nil {
		return err
	}
	argv, err := h.Argv(ctx, a.model, a.rest)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		fmt.Println(string(dryRun(argv, ctx.Env)))
		return nil
	}
	if a.harness != "opencode" && ctx.Sys.Env.Get("HARNESS_DREAM") != "1" {
		if exe, err := os.Executable(); err == nil {
			_ = dream.Spawn(ctx.State, exe, ctx.Sys.Env.Environ(), time.Now()) // a dream never holds up a launch
		}
	}
	return syscall.Exec(argv[0], argv, ctx.Env.Environ())
}

// diagrams lints every diagram in the named markdown files and fails when any has an issue.
func diagrams(files []string) error {
	if len(files) == 0 {
		return errors.New("usage: diagrams <file.md>...")
	}
	n := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("diagrams: %w", err)
		}
		for _, i := range diagram.Lint(string(b)) {
			fmt.Printf("%s:%s\n", f, i)
			n++
		}
	}
	if n > 0 {
		return fmt.Errorf("diagrams: %d issues", n)
	}
	return nil
}

func memoryCmd(env sys.Env, argv []string) error {
	usage := errors.New("usage: memory path [dir] | memory context")
	if len(argv) == 0 {
		return usage
	}
	switch argv[0] {
	case "path":
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("memory path: %w", err)
		}
		return memory.PathCmd(stateDir(env), argv[1:], cwd, os.Stdout)
	case "context":
		// a failing SessionStart hook would block the session; report and let it start
		if err := memory.ContextHook(stateDir(env), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "harness:", err)
		}
		return nil
	}
	return usage
}

// dreamCmd runs claude the way an interactive launch does (config dir, gateway, auth) on the fast
// role. It needs no HARNESS_ROOT: the launch that spawned it already synced the claude config.
func dreamCmd(env sys.Env, argv []string) error {
	if len(argv) == 2 && argv[0] == "guard" {
		if err := dream.Guard(argv[1], os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2) // claude's PreToolUse protocol: exit 2 blocks the call and shows stderr
		}
		return nil
	}
	fs := flag.NewFlagSet("dream", flag.ContinueOnError)
	project := fs.String("project", "", "dream only the project holding `dir`, now, ignoring stamps")
	since := fs.Duration("since", 0, "dream sessions from this long ago instead of since the last dream")
	dryRun := fs.Bool("dry-run", false, "list projects and prompt sizes without calling claude")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("dream: unexpected argument %q", fs.Arg(0))
	}
	timeout := 10 * time.Minute
	if v := env.Get("HARNESS_DREAM_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("HARNESS_DREAM_TIMEOUT: %w", err)
		}
		timeout = d
	}
	ctx := &harness.Ctx{State: stateDir(env), Env: env.Clone(), Sys: sys.New(env)}
	for k := range ctx.Env {
		if strings.HasPrefix(k, "HERDR_") {
			delete(ctx.Env, k) // the herdr hook would report the dream as the launching pane's agent
		}
	}
	var err error
	if ctx.Models, err = model.Load(env); err != nil {
		return err
	}
	if err := (claude.H{}).Env(ctx); err != nil {
		return err
	}
	bin := ctx.Sys.FindBin("claude")
	if bin == "" && !*dryRun {
		return errors.New("dream: claude not found on PATH")
	}
	if *project != "" {
		if *project, err = filepath.Abs(*project); err != nil {
			return fmt.Errorf("dream --project: %w", err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("dream: %w", err)
	}
	logw, closeLog, err := dreamLog(ctx.State)
	if err != nil {
		return err
	}
	defer closeLog()
	r := &dream.Runner{State: ctx.State, Claude: bin, Self: self, Model: ctx.Models.ResolveFlag("claude", "fast"), Env: ctx.Env,
		Timeout: timeout, Now: time.Now, Log: logw, Out: os.Stdout}
	return r.Run(dream.Options{Project: *project, Since: *since, DryRun: *dryRun})
}

// dreamLog writes to stderr and the dream log; a spawned dream's stderr already is the log.
func dreamLog(state string) (io.Writer, func(), error) {
	path := dream.LogPath(state)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("dream log: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("dream log: %w", err)
	}
	fi, err1 := f.Stat()
	si, err2 := os.Stderr.Stat()
	if err1 == nil && err2 == nil && os.SameFile(fi, si) {
		return os.Stderr, func() { f.Close() }, nil
	}
	return io.MultiWriter(os.Stderr, f), func() { f.Close() }, nil
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
	gateway, key := "none: subscription login", "none"
	if m.BaseURL != "" {
		gateway = m.BaseURL + " (" + m.BaseSrc + ")"
	}
	if m.KeyCmd != "" {
		key = m.KeyCmd + " (" + m.KeySrc + ")"
	}
	rows := [][2]string{{"root", ctx.Root}, {"state", ctx.State}, {"gateway", gateway}, {"key cmd", key}}
	for i, d := range ctx.Layers.Dirs {
		rows = append(rows, [2]string{fmt.Sprintf("layer %d", i+1), d})
	}
	for _, b := range []string{"omp", "claude", "codex"} {
		rows = append(rows, [2]string{"bin " + b, or(s.FindBin(b), "MISSING")})
	}
	rows = append(rows, [2]string{"bin opencode", or(s.FindBin("opencode"), "-")})
	rows = append(rows,
		[2]string{"bin herdr", or(s.Which("herdr"), "-")},
		[2]string{"role default", m.Resolve("omp", "default")},
		[2]string{"role review", m.Resolve("omp", "review")})
	for _, r := range rows {
		fmt.Printf("%-14s %s\n", r[0], r[1])
	}
	fmt.Println("\nnotes")
	fmt.Println("  claude: managed settings in /Library/Application Support/ClaudeCode win over generated settings.json.")
	fmt.Println("  codex: also reads ~/.agents/skills (no CODEX_HOME-relative override).")
	fmt.Println("  memory: launches spawn `harness dream` once the last is over 20h old, at most hourly; log in " + dream.LogPath(ctx.State) + ".")
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
