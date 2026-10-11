package omp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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
		"provider": "gw", "api": "openai", "disable_strict_tools": true,
		"models": map[string]any{"big": map[string]any{"api": "anthropic", "context": 1000000},
			"rev": map[string]any{"context": 500000}, "small": nil},
		"roles": map[string]any{"default": "big", "review": "rev", "fast": "small", "vision": "rev", "tiny": "small",
			"extra": "rev"},
	}}
}

func fixture(t *testing.T, m *model.Config) *harness.Ctx {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "harness")
	h := root
	write(t, filepath.Join(h, "AGENTS.md"), "# BASE\n", 0o644)
	write(t, filepath.Join(h, "styles", "i-have-adhd.md"), "---\nname: i-have-adhd\n---\n\nSTYLE-BODY\n", 0o644)
	bins := filepath.Join(tmp, "bin")
	write(t, filepath.Join(bins, "omp"), "#!/bin/sh\necho omp/1.2.3\n", 0o755)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := sys.Env{"PATH": bins + ":/usr/bin:/bin", "HOME": home}
	return &harness.Ctx{
		Root: root, State: filepath.Join(tmp, "state"), Cwd: tmp,
		Layers: &layer.Set{Root: root, Dirs: []string{h}},
		Models: m, Env: env, Sys: &sys.Sys{Env: env, Home: home}, DryRun: true,
	}
}

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSyncGateway(t *testing.T) {
	ctx := fixture(t, gateway())
	d := AgentDir(ctx.State)
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := readYAML(t, filepath.Join(d, "config.yml"))
	roles := cfg["modelRoles"].(map[string]any)
	for k, want := range map[string]string{"default": "gw/big", "slow": "gw/rev", "review": "gw/rev",
		"smol": "gw/small", "memory": "gw/small", "extra": "gw/rev"} {
		if roles[k] != want {
			t.Errorf("modelRoles.%s = %v, want %s", k, roles[k], want)
		}
	}
	skills := cfg["skills"].(map[string]any)
	if skills["enableAgentsUser"] != false || cfg["personality"] != "pragmatic" {
		t.Errorf("base not merged: %v", cfg)
	}
	if cfg["memory"].(map[string]any)["backend"] != "mnemopi" ||
		cfg["mnemopi"].(map[string]any)["dbPath"] != filepath.Join(ctx.State, "memory", "mnemopi.db") ||
		cfg["mnemopi"].(map[string]any)["scoping"] != "global" {
		t.Errorf("memory = %v %v", cfg["memory"], cfg["mnemopi"])
	}
	p := readYAML(t, filepath.Join(d, "models.yml"))["providers"].(map[string]any)["gw"].(map[string]any)
	if p["apiKey"] != "!gw-key" || p["api"] != "openai-responses" || p["disableStrictTools"] != true ||
		p["baseUrl"] != "https://gw.example/v1" {
		t.Errorf("provider = %v", p)
	}
	ids := []string{}
	for _, m := range p["models"].([]any) {
		mm := m.(map[string]any)
		ids = append(ids, mm["id"].(string))
		if mm["id"] == "big" && (mm["api"] != "anthropic-messages" || mm["contextWindow"] != 1000000) {
			t.Errorf("big = %v", mm)
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"big", "rev", "small"}) {
		t.Errorf("model ids = %v", ids)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "APPEND_SYSTEM.md")); string(b) != "STYLE-BODY\n" {
		t.Errorf("APPEND_SYSTEM.md = %q", b)
	}
	if _, err := os.Stat(filepath.Join(d, "AGENTS.md")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(d, "agents")); !os.IsNotExist(err) {
		t.Error("agents dir generated")
	}
}

func TestSyncWithoutGatewayHasNoProviders(t *testing.T) {
	ctx := fixture(t, &model.Config{Raw: map[string]any{"provider": "gw", "roles": map[string]any{"default": "big"}}})
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	m := readYAML(t, filepath.Join(AgentDir(ctx.State), "models.yml"))
	if p, ok := m["providers"].(map[string]any); !ok || len(p) != 0 {
		t.Errorf("providers = %v", m["providers"])
	}
	if r := readYAML(t, filepath.Join(AgentDir(ctx.State), "config.yml"))["modelRoles"].(map[string]any); r["default"] != "gw/big" {
		t.Errorf("default = %v", r["default"])
	}
}

func TestEnv(t *testing.T) {
	ctx := fixture(t, gateway())
	ctx.Env["OMP_PROFILE"], ctx.Env["PI_PROFILE"], ctx.Env["HERDR_ENV"] = "x", "y", "1"
	if err := (H{}).Env(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := ctx.Env["OMP_PROFILE"]; ok {
		t.Error("OMP_PROFILE kept")
	}
	if _, ok := ctx.Env["PI_PROFILE"]; ok {
		t.Error("PI_PROFILE kept")
	}
	if ctx.Env["PI_CODING_AGENT_DIR"] != filepath.Join(ctx.State, "omp", "agent") {
		t.Errorf("PI_CODING_AGENT_DIR = %s", ctx.Env["PI_CODING_AGENT_DIR"])
	}
	rel := ctx.Env["PI_CONFIG_DIR"]
	if filepath.IsAbs(rel) || filepath.Join(ctx.Sys.Home, rel) != filepath.Join(ctx.State, "omp") {
		t.Errorf("PI_CONFIG_DIR = %s", rel)
	}
	if ctx.Env["PI_FORCE_IMAGE_PROTOCOL"] != "kitty" {
		t.Error("kitty not defaulted under herdr")
	}
	ctx = fixture(t, gateway())
	ctx.Env["TERM_PROGRAM"], ctx.Env["PI_FORCE_IMAGE_PROTOCOL"] = "herdr", "sixel"
	_ = (H{}).Env(ctx)
	if ctx.Env["PI_FORCE_IMAGE_PROTOCOL"] != "sixel" {
		t.Error("explicit image protocol overridden")
	}
	ctx = fixture(t, gateway())
	_ = (H{}).Env(ctx)
	if _, ok := ctx.Env["PI_FORCE_IMAGE_PROTOCOL"]; ok {
		t.Error("kitty set outside herdr")
	}
}

func TestArgv(t *testing.T) {
	ctx := fixture(t, gateway())
	bin := filepath.Join(filepath.Dir(ctx.Cwd), filepath.Base(ctx.Cwd), "bin", "omp")
	got, err := (H{}).Argv(ctx, "review", []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != bin+" --model gw/rev hello" {
		t.Errorf("argv = %v", got)
	}
	got, _ = (H{}).Argv(ctx, "", nil)
	if !slices.Equal(got, []string{bin}) {
		t.Errorf("argv = %v", got)
	}
}
