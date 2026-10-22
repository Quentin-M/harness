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
	if cfg["memory"].(map[string]any)["backend"] != "off" || cfg["mnemopi"] != nil {
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

// The split layout Q hit: a harness store with its own sessions and a bare ~/.omp store.
func TestArgvResumesBareOmpSession(t *testing.T) {
	ctx := fixture(t, gateway())
	local := filepath.Join(AgentDir(ctx.State), "sessions", "-p")
	bare := filepath.Join(ctx.Sys.Home, ".omp", "agent", "sessions", "-q")
	write(t, filepath.Join(local, "2026-10-22T03-10-29-351Z_01a16196-b5e7-77e8-b31e-f43f1df436fb.jsonl"), "h", 0o644)
	bareFile := filepath.Join(bare, "2026-10-21T03-57-35-876Z_01a15c9b-7b04-700a-9703-1cf3ac2cae2b.jsonl")
	write(t, bareFile, "b", 0o644)
	write(t, filepath.Join(bare, "2026-10-21T04-00-02-742Z_01a15c9d-b8b6-7009-a604-48fc24b25443.jsonl"), "b", 0o644)
	write(t, filepath.Join(bare, "2026-10-21T03-59-47-509Z_01a15c9d-7d35-77a2-b25b-19bc4bb2b0ac.jsonl"), "b", 0o644)
	for _, c := range []struct{ in, want []string }{
		{[]string{"--resume", "01a15c9b-7b04-700a-9703-1cf3ac2cae2b"}, []string{"--resume", bareFile}},
		{[]string{"-r", "01A15C9B", "hi"}, []string{"-r", bareFile, "hi"}},
		{[]string{"--resume=01a15c9b"}, []string{"--resume=" + bareFile}},
		{[]string{"--resume", "01a16196"}, []string{"--resume", "01a16196"}},
		{[]string{"--resume", "01a15c9d"}, []string{"--resume", "01a15c9d"}},
		{[]string{"--resume", "ffffffff"}, []string{"--resume", "ffffffff"}},
		{[]string{"--resume", "../-q/*"}, []string{"--resume", "../-q/*"}},
		{[]string{"01a15c9b"}, []string{"01a15c9b"}},
	} {
		got, err := (H{}).Argv(ctx, "", c.in)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got[1:], c.want) {
			t.Errorf("%v: argv = %v, want %v", c.in, got[1:], c.want)
		}
	}
}

func TestArgvResumeBareLinksItsBlobs(t *testing.T) {
	ctx := fixture(t, gateway())
	hash := strings.Repeat("ab", 32)
	bare := filepath.Join(ctx.Sys.Home, ".omp", "agent")
	session := filepath.Join(bare, "sessions", "-q", "2026-10-21T03-57-35-876Z_01a15c9b-7b04-700a-9703-1cf3ac2cae2b.jsonl")
	write(t, session, `{"image":"blob:sha256:`+hash+`"}`+"\n", 0o644)
	write(t, filepath.Join(bare, "blobs", hash), "PNGDATA", 0o644)
	write(t, filepath.Join(bare, "blobs", hash+".png"), "PNGDATA", 0o644)
	write(t, filepath.Join(bare, "blobs", strings.Repeat("cd", 32)), "other", 0o644)
	if _, err := (H{}).Argv(ctx, "", []string{"--resume", "01a15c9b"}); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(AgentDir(ctx.State), "blobs")
	for _, name := range []string{hash, hash + ".png"} {
		if got, err := os.ReadFile(filepath.Join(local, name)); string(got) != "PNGDATA" {
			t.Errorf("blob %s = %q, %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(local, strings.Repeat("cd", 32))); err == nil {
		t.Error("unreferenced blob linked")
	}
	if got, _ := os.ReadFile(filepath.Join(bare, "blobs", hash)); string(got) != "PNGDATA" {
		t.Errorf("bare blob changed: %q", got)
	}
}

func TestArgvResumeBareRespectsExplicitStore(t *testing.T) {
	ctx := fixture(t, gateway())
	write(t, filepath.Join(ctx.Sys.Home, ".omp", "agent", "sessions", "-q",
		"2026-10-21T03-57-35-876Z_01a15c9b-7b04-700a-9703-1cf3ac2cae2b.jsonl"), "b", 0o644)
	for _, in := range [][]string{
		{"--profile", "work", "--resume", "01a15c9b"},
		{"--resume", "01a15c9b", "--profile=work"},
		{"--session-dir", "/s", "-r", "01a15c9b"},
		{"--session-dir=/s", "--resume=01a15c9b"},
		{"--", "--resume", "01a15c9b"},
	} {
		got, err := (H{}).Argv(ctx, "", in)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got[1:], in) {
			t.Errorf("%v rewritten to %v", in, got[1:])
		}
	}
	xdg := filepath.Join(ctx.Sys.Home, "xdg")
	xdgFile := filepath.Join(xdg, "omp", "sessions", "-q", "2026-10-22T00-00-00-000Z_01a15c9b-0000-7000-8000-000000000000.jsonl")
	write(t, xdgFile, "x", 0o644)
	ctx.Env["XDG_DATA_HOME"] = xdg
	if got, _ := (H{}).Argv(ctx, "", []string{"--resume", "01a15c9b"}); !slices.Equal(got[1:], []string{"--resume", "01a15c9b"}) {
		t.Errorf("XDG store is shared, omp resolves it: argv = %v", got[1:])
	}
}

func TestCopyBlobConcurrentAndFailedPublish(t *testing.T) {
	d := t.TempDir()
	from, to := filepath.Join(d, "src"), filepath.Join(d, "dst", "blob")
	data := strings.Repeat("x", 1<<20)
	write(t, from, data, 0o644)
	os.MkdirAll(filepath.Dir(to), 0o755)
	errs := make(chan error, 8)
	for range 8 {
		go func() { errs <- copyBlob(from, to) }()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(to); string(got) != data {
		t.Errorf("published blob has %d bytes, want %d", len(got), len(data))
	}
	if err := copyBlob(filepath.Join(d, "missing"), filepath.Join(d, "dst", "other")); err == nil {
		t.Error("missing source published")
	}
	if err := copyBlob(from, filepath.Join(to, "under-a-file")); err == nil {
		t.Error("publish into a file succeeded")
	}
	busy := filepath.Join(filepath.Dir(to), "busy")
	write(t, filepath.Join(busy, "keep"), "k", 0o644)
	if err := copyBlob(from, busy); err == nil {
		t.Error("publish over a non-empty directory succeeded")
	}
	if _, err := os.Stat(filepath.Join(busy, "keep")); err != nil {
		t.Errorf("directory damaged: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Dir(to)); len(ents) != 2 {
		t.Errorf("temp files left: %v", ents)
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
