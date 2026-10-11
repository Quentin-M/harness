package rig

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/layer"
	"github.com/Quentin-M/harness/internal/model"
	"github.com/Quentin-M/harness/internal/sys"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fixture(t *testing.T) *harness.Ctx {
	t.Helper()
	m := &model.Config{BaseURL: "https://gw.example", KeyCmd: "echo gw-secret", Raw: map[string]any{
		"provider": "gw", "api": "openai",
		"models": map[string]any{"big": map[string]any{"api": "anthropic", "context": 1000000}},
		"roles":  map[string]any{"default": "big"},
	}}
	tmp := t.TempDir()
	root := filepath.Join(tmp, "harness")
	h := root
	write(t, filepath.Join(h, "AGENTS.md"), "# BASE\n")
	write(t, filepath.Join(h, "styles", "i-have-adhd.md"), "---\nname: i-have-adhd\n---\nSTYLE-BODY\n")
	write(t, filepath.Join(h, "skills", "trace", "SKILL.md"), "---\nname: trace\n---\n")
	home := filepath.Join(tmp, "home")
	env := sys.Env{"PATH": "/usr/bin:/bin", "HOME": home, "ANTHROPIC_AUTH_TOKEN": "stale"}
	return &harness.Ctx{
		Root: root, State: filepath.Join(tmp, "state"), Cwd: tmp,
		Layers: &layer.Set{Root: root, Dirs: []string{h}},
		Models: m, Env: env, Sys: &sys.Sys{Env: env, Home: home}, DryRun: true,
	}
}

func TestEnvSelectsHarnessConfigForSeats(t *testing.T) {
	ctx := fixture(t)
	path := ctx.Env["PATH"]
	if err := (H{}).Env(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"OPENRIG_HOME": filepath.Join(ctx.State, "rig"), "OPENRIG_NO_KERNEL": "1",
		"OPENRIG_RUNTIME_CODEX_HOOKS_ENABLED": "false",
		"CLAUDE_CONFIG_DIR":                   filepath.Join(ctx.State, "claude"),
		"CODEX_HOME":                          filepath.Join(ctx.State, "codex"),
		"ANTHROPIC_BASE_URL":                  "https://gw.example",
		"HOME":                                ctx.Sys.Home,
		"PATH":                                filepath.Join(ctx.State, "rig/npm/node_modules/.bin") + ":" + filepath.Join(ctx.State, "bin") + ":" + path,
	}
	for k, v := range want {
		if ctx.Env[k] != v {
			t.Errorf("%s = %q, want %q", k, ctx.Env[k], v)
		}
	}
	for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "LITELLM_API_KEY", "LITELLM_BASE_URL",
		"OPENRIG_RECOVERY_PROVIDER_AUTH_ENV_ALLOWLIST", "PI_CODING_AGENT_DIR"} {
		if _, ok := ctx.Env[k]; ok {
			t.Errorf("%s set", k)
		}
	}
}

func TestSyncRendersClaudeAndCodex(t *testing.T) {
	ctx := fixture(t)
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(ctx.State, "claude/CLAUDE.md")); !strings.Contains(got, "# BASE") {
		t.Errorf("CLAUDE.md = %q", got)
	}
	if got := read(t, filepath.Join(ctx.State, "claude/settings.json")); !strings.Contains(got, `"outputStyle": "i-have-adhd"`) {
		t.Errorf("settings.json = %q", got)
	}
	cfg := read(t, filepath.Join(ctx.State, "codex/config.toml"))
	for _, s := range []string{`model_provider = "gw"`, `base_url = "https://gw.example/v1"`, `command = "echo gw-secret"`} {
		if !strings.Contains(cfg, s) {
			t.Errorf("codex config.toml lacks %s:\n%s", s, cfg)
		}
	}
	for _, d := range []string{"claude/skills/trace", "codex/skills/trace"} {
		if _, err := os.Stat(filepath.Join(ctx.State, d, "SKILL.md")); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ctx.State, "rig/state/omp")); !os.IsNotExist(err) {
		t.Error("omp seat state seeded")
	}
}

func TestArgv(t *testing.T) {
	ctx := fixture(t)
	bin := filepath.Join(ctx.State, "rig/npm/node_modules/.bin/rig")
	if got, _ := (H{}).Argv(ctx, "", nil); !slices.Equal(got, []string{bin}) {
		t.Errorf("argv = %v", got)
	}
	if got, _ := (H{}).Argv(ctx, "", []string{"up", "x.yaml"}); !slices.Equal(got, []string{bin, "up", "x.yaml"}) {
		t.Errorf("argv = %v", got)
	}
}
