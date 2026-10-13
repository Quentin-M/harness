package claude

import (
	"encoding/json"
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
		"models": map[string]any{"big": map[string]any{"api": "anthropic", "context": 1000000},
			"rev": map[string]any{"context": 500000}, "small": nil},
		"roles":     map[string]any{"default": "big", "review": "rev", "fast": "small"},
		"overrides": map[string]any{"codex": map[string]any{"default": "rev"}},
	}}
}

// fixture builds one layer, a bin dir with claude, and a Ctx for the given models.
func fixture(t *testing.T, m *model.Config) *harness.Ctx {
	t.Helper()
	tmp := t.TempDir()
	h := filepath.Join(tmp, "harness")
	write(t, filepath.Join(h, "AGENTS.md"), "# BASE\n", 0o644)
	write(t, filepath.Join(h, "styles", "i-have-adhd.md"), "---\nname: i-have-adhd\n---\nSTYLE-BODY\n", 0o644)
	write(t, filepath.Join(h, "skills", "trace", "SKILL.md"), "---\nname: trace\n---\nx\n", 0o644)
	bins := filepath.Join(tmp, "bin")
	for _, b := range []string{"claude"} {
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

func settingsOf(t *testing.T, ctx *harness.Ctx) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ctx.State, "claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSyncGateway(t *testing.T) {
	ctx := fixture(t, gateway())
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	s := settingsOf(t, ctx)
	for k, want := range map[string]any{"model": "big[1m]", "apiKeyHelper": "gw-key", "outputStyle": "i-have-adhd",
		"tui": "fullscreen", "skipDangerousModePermissionPrompt": true, "autoMemoryEnabled": true} {
		if s[k] != want {
			t.Errorf("%s = %v, want %v", k, s[k], want)
		}
	}
	if attr, ok := s["attribution"].(map[string]any); !ok || attr["commit"] != "" || attr["pr"] != "" {
		t.Errorf("attribution = %v, want empty commit and pr", s["attribution"])
	}
	allow := s["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 2 || allow[0] != "Bash(git push --force-with-lease *)" || allow[1] != "Bash(git push --force-with-lease=*)" {
		t.Errorf("allow = %v", allow)
	}
	hooks := s["hooks"].(map[string]any)
	start := hooks["SessionStart"].([]any)
	herdr := start[0].(map[string]any)
	if herdr["matcher"] != "^(startup|resume|clear|compact|fork)$" {
		t.Errorf("matcher = %v", herdr["matcher"])
	}
	h0 := herdr["hooks"].([]any)[0].(map[string]any)
	if !strings.Contains(h0["command"].(string), "herdr-agent-state.sh") || h0["timeout"] != 10.0 {
		t.Errorf("herdr hook = %v", h0)
	}
	if len(start) != 1 {
		t.Errorf("SessionStart groups = %v, want herdr only", start)
	}
	for _, e := range []string{"UserPromptSubmit", "Stop", "SessionEnd"} {
		if _, ok := hooks[e]; ok {
			t.Errorf("%s hook registered: %v", e, hooks[e])
		}
	}
	raw, _ := os.ReadFile(filepath.Join(ctx.State, "claude", "settings.json"))
	text := string(raw)
	if !strings.HasSuffix(text, "}\n") || !strings.Contains(text, "\n  \"model\"") {
		t.Errorf("settings.json not 2-space indented with trailing newline")
	}
	for _, f := range []string{"CLAUDE.md", "output-styles/i-have-adhd.md", "skills/trace/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(ctx.State, "claude", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(ctx.State, "claude", "agents")); !os.IsNotExist(err) {
		t.Errorf("agents dir generated")
	}
	if style, _ := os.ReadFile(filepath.Join(ctx.State, "claude", "output-styles", "i-have-adhd.md")); !strings.HasPrefix(string(style), "---\n") {
		t.Errorf("output style lost its frontmatter: %q", style)
	}
}

func TestModelNarrowContext(t *testing.T) {
	m := gateway()
	m.Raw["roles"].(map[string]any)["default"] = "rev"
	if got := Model(m); got != "rev" {
		t.Errorf("model = %q, want rev", got)
	}
}

func TestSyncKeepsClaudeOwnedKeys(t *testing.T) {
	ctx := fixture(t, &model.Config{Raw: map[string]any{}})
	path := filepath.Join(ctx.State, "claude", "settings.json")
	write(t, path, `{"enabledPlugins": {"slack@claude-plugins-official": true}, "model": "stale",
		"apiKeyHelper": "old-gw-key", "hooks": {"PreToolUse": []}, "statusLine": {"type": "command", "padding": 0}}`, 0o644)
	for range 2 {
		if err := (H{}).Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s := settingsOf(t, ctx)
	if p, _ := s["enabledPlugins"].(map[string]any); p["slack@claude-plugins-official"] != true {
		t.Errorf("enabledPlugins = %v, want slack kept", s["enabledPlugins"])
	}
	if sl, _ := s["statusLine"].(map[string]any); sl["padding"] != 0.0 {
		t.Errorf("statusLine = %v, want kept", s["statusLine"])
	}
	if s["model"] == "stale" || s["outputStyle"] != Style {
		t.Errorf("launcher keys not applied: model=%v outputStyle=%v", s["model"], s["outputStyle"])
	}
	if _, ok := s["apiKeyHelper"]; ok {
		t.Errorf("stale apiKeyHelper kept: %v", s["apiKeyHelper"])
	}
	if _, ok := s["hooks"].(map[string]any)["PreToolUse"]; ok {
		t.Errorf("stale hook event kept: %v", s["hooks"])
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), "{\n  \"enabledPlugins\"") {
		t.Errorf("existing key order lost:\n%s", raw)
	}
}

func TestSyncRejectsCorruptSettings(t *testing.T) {
	ctx := fixture(t, gateway())
	path := filepath.Join(ctx.State, "claude", "settings.json")
	write(t, path, `{"enabledPlugins": `, 0o644)
	if err := (H{}).Sync(ctx); err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("err = %v, want parse error naming settings.json", err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{"enabledPlugins": ` {
		t.Errorf("corrupt file overwritten: %q", raw)
	}
}

func TestEnv(t *testing.T) {
	for name, m := range map[string]*model.Config{"gateway": gateway(), "none": {Raw: map[string]any{}}} {
		t.Run(name, func(t *testing.T) {
			ctx := fixture(t, m)
			for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
				ctx.Env[k] = "stale"
			}
			if err := (H{}).Env(ctx); err != nil {
				t.Fatal(err)
			}
			if got := ctx.Env["CLAUDE_CONFIG_DIR"]; got != filepath.Join(ctx.State, "claude") {
				t.Errorf("CLAUDE_CONFIG_DIR = %q", got)
			}
			if got, ok := ctx.Env["ANTHROPIC_BASE_URL"]; got != m.BaseURL || ok != (m.BaseURL != "") {
				t.Errorf("ANTHROPIC_BASE_URL = %q (%v)", got, ok)
			}
			for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
				if _, ok := ctx.Env[k]; ok {
					t.Errorf("%s not unset", k)
				}
			}
		})
	}
}

func TestArgv(t *testing.T) {
	ctx := fixture(t, gateway())
	bin := filepath.Join(ctx.Cwd, "bin", "claude")
	got, err := (H{}).Argv(ctx, "", []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{bin, "--dangerously-skip-permissions", "hello"}; !slices.Equal(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
	got, _ = (H{}).Argv(ctx, "review", []string{"--dangerously-skip-permissions", "x"})
	if want := []string{bin, "--model", "rev", "--dangerously-skip-permissions", "x"}; !slices.Equal(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
	got, _ = (H{}).Argv(ctx, "literal-id", nil)
	if want := []string{bin, "--model", "literal-id", "--dangerously-skip-permissions"}; !slices.Equal(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func equal(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
