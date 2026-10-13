// Package e2e runs the launcher binary against fixture layers in temp dirs.
package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

var (
	harnessBin string
	code       string // the real harness checkout
)

var models = map[string]any{
	"provider": "gw", "api": "openai", "disable_strict_tools": true,
	"models": map[string]any{"big": map[string]any{"api": "anthropic", "context": 1000000},
		"rev": map[string]any{"context": 500000}, "small": map[string]any{}},
	"roles":     map[string]any{"default": "big", "review": "rev", "fast": "small", "vision": "rev", "tiny": "small"},
	"overrides": map[string]any{"codex": map[string]any{"default": "rev"}},
}

func TestMain(m *testing.M) {
	wd, _ := os.Getwd()
	code = filepath.Clean(filepath.Join(wd, "..", ".."))
	dir, err := os.MkdirTemp("", "harness-e2e-bin")
	if err != nil {
		panic(err)
	}
	harnessBin = filepath.Join(dir, "harness")
	build := exec.Command("go", "build", "-o", harnessBin, "../cmd/claude")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build harness:", err)
		os.Exit(1)
	}
	rc := m.Run()
	os.RemoveAll(dir)
	os.Exit(rc)
}

// ── fixture ──────────────────────────────────────────────────────────────────

type fixture struct {
	t                               *testing.T
	env                             map[string]string
	tmp, state, root, home, managed string
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte(text), 0o644))
}

func dumpYAML(t *testing.T, v any) string {
	t.Helper()
	b, err := yaml.Marshal(v)
	must(t, err)
	return string(b)
}

func md(t *testing.T, path, text string, fm map[string]any) {
	t.Helper()
	head := ""
	if len(fm) > 0 {
		head = "---\n" + dumpYAML(t, fm) + "---\n"
	}
	write(t, path, head+text)
}

type kv = map[string]any

func newFixture(t *testing.T) *fixture {
	tmp := realpath(t, t.TempDir()) // like pytest's tmp_path; harness resolves HARNESS_STATE and HARNESS_ROOT
	root := filepath.Join(tmp, "harness")
	h, extra := root, filepath.Join(tmp, "extra")
	md(t, h+"/AGENTS.md", "# BASE\n", nil)
	md(t, h+"/rules/a.md", "RULE-A\n", kv{"paths": []string{"**/*.go"}})
	md(t, h+"/styles/i-have-adhd.md", "STYLE-BODY\n", kv{"name": "i-have-adhd"})
	md(t, h+"/skills/trace/SKILL.md", "harness trace\n", kv{"name": "trace"})
	md(t, h+"/skills/only-base/SKILL.md", "x\n", kv{"name": "only-base"})
	md(t, h+"/skills/tdd/SKILL.md", "pstack tdd\n", kv{"name": "tdd"})
	write(t, h+"/skills/pstack-sheet.yaml", "\"judgment and prose\": default\n\"interrogate reviewers\": [default, review]\n")
	md(t, extra+"/AGENTS.md", "# EXTRA\n", nil)
	md(t, extra+"/skills/trace/SKILL.md", "extra trace\n", kv{"name": "trace"})
	md(t, extra+"/rules/z.md", "RULE-EXTRA\n", nil)
	write(t, root+"/rigs/x/rig.yaml", twoSeats)
	modelsFile := filepath.Join(tmp, "models.yaml")
	write(t, modelsFile, dumpYAML(t, models))
	managed := filepath.Join(tmp, "managed")
	write(t, managed+"/00-env.json", `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`)
	write(t, managed+"/05-auth.json", `{"apiKeyHelper": "gw-key"}`)

	bins, shims, home := filepath.Join(tmp, "bin"), filepath.Join(tmp, "shims"), filepath.Join(tmp, "home")
	for _, d := range []string{bins, shims, home} {
		must(t, os.MkdirAll(d, 0o755))
	}
	for _, name := range []string{"omp", "claude", "codex", "opencode"} {
		write(t, filepath.Join(bins, name), "#!/bin/sh\necho real\n")
		write(t, filepath.Join(shims, name), fmt.Sprintf("#!/bin/sh\nexec harness --%s \"$@\"\n", name))
	}
	for _, d := range []string{bins, shims} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			must(t, os.Chmod(filepath.Join(d, e.Name()), 0o755))
		}
	}
	env := map[string]string{
		"PATH": shims + ":" + bins + ":/usr/bin:/bin", "HOME": home, "HARNESS_ROOT": root,
		"HARNESS_STATE": filepath.Join(tmp, "state"), "HARNESS_LAYERS": extra, "HARNESS_MODELS": modelsFile,
		"HARNESS_MANAGED_SETTINGS": managed, "HARNESS_DRY_RUN": "1",
	}
	return &fixture{t: t, env: env, tmp: tmp, state: filepath.Join(tmp, "state"), root: root, home: home, managed: managed}
}

func (f *fixture) p(rel string) string { return filepath.Join(f.state, rel) }

func (f *fixture) run(args ...string) string { return f.runEnv(nil, args...) }

func (f *fixture) runEnv(extra map[string]string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command(harnessBin, args...)
	cmd.Dir = f.tmp
	merged := map[string]string{}
	for k, v := range f.env {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	for k, v := range merged {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		f.t.Fatalf("harness %v: %v\n%s", args, err, stderr.String())
	}
	return string(out)
}

type launchOut struct {
	Argv []string           `json:"argv"`
	Env  map[string]*string `json:"env"`
}

func (f *fixture) launch(args ...string) launchOut { return f.launchEnv(nil, args...) }

func (f *fixture) launchEnv(extra map[string]string, args ...string) launchOut {
	f.t.Helper()
	var out launchOut
	s := f.runEnv(extra, args...)
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		f.t.Fatalf("bad dry-run json %q: %v", s, err)
	}
	return out
}

func envVal(o launchOut, k string) string {
	if v := o.Env[k]; v != nil {
		return *v
	}
	return "<nil>"
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	return string(b)
}

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	must(t, yaml.Unmarshal([]byte(read(t, path)), &m))
	return m
}

func readTOML(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	_, err := toml.Decode(read(t, path), &m)
	must(t, err)
	return m
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	must(t, json.Unmarshal([]byte(read(t, path)), &m))
	return m
}

// hasBig asserts an omp provider lists the gateway's 1M-context anthropic model.
func hasBig(t *testing.T, provider map[string]any) {
	t.Helper()
	models, _ := provider["models"].([]any)
	for _, m := range models {
		if reflect.DeepEqual(m, map[string]any{"id": "big", "api": "anthropic-messages", "contextWindow": 1000000}) {
			return
		}
	}
	t.Errorf("big model missing from %v", provider["models"])
}

// get walks nested maps and slices: get(m, "a", "b", 0, "c").
func get(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			switch s := v.(type) {
			case []any:
				if k >= len(s) {
					return nil
				}
				v = s[k]
			case []map[string]any: // toml arrays of tables
				if k >= len(s) {
					return nil
				}
				v = s[k]
			default:
				return nil
			}
		}
	}
	return v
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	must(t, err)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

func contains(t *testing.T, text, sub, what string) {
	t.Helper()
	if !strings.Contains(text, sub) {
		t.Errorf("%s: %q not in %q", what, sub, text)
	}
}

func inOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	last := -1
	for _, p := range parts {
		i := strings.Index(text, p)
		if i < 0 || i < last {
			t.Errorf("order: %q at %d after %d in %q", p, i, last, text)
		}
		last = i
	}
}

func fmBody(t *testing.T, path string) (map[string]any, string) {
	t.Helper()
	parts := strings.SplitN(read(t, path), "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("no frontmatter in %s", path)
	}
	var m map[string]any
	must(t, yaml.Unmarshal([]byte(parts[1]), &m))
	return m, parts[2]
}

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	must(t, err)
	return r
}

func git(t *testing.T, cwd string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-C", cwd}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestSyncGeneratesEveryHarness(t *testing.T) {
	f := newFixture(t)
	f.run("sync")
	for _, rel := range []string{"omp/agent/config.yml", "omp/agent/models.yml", "omp/agent/AGENTS.md", "omp/agent/APPEND_SYSTEM.md",
		"claude/CLAUDE.md", "claude/settings.json", "claude/output-styles/i-have-adhd.md",
		"codex/config.toml", "codex/AGENTS.md"} {
		if st, err := os.Stat(f.p(rel)); err != nil || !st.Mode().IsRegular() {
			t.Errorf("missing %s", rel)
		}
	}
	for _, rel := range []string{"omp/agent/agents", "claude/agents", "codex/agents"} {
		if exists(f.p(rel)) {
			t.Errorf("%s generated", rel)
		}
	}
	for _, h := range []string{"omp/agent", "claude", "codex"} {
		eq(t, names(t, f.p(h+"/skills")), []string{"only-base", "tdd", "trace"}, h+" skills")
	}
}

func TestInstructionOrderAndPstackSheet(t *testing.T) {
	f := newFixture(t)
	f.run("sync")
	text := read(t, f.p("omp/agent/AGENTS.md"))
	inOrder(t, text, "# BASE", "# EXTRA", "RULE-A", "RULE-EXTRA", "## pstack models")
	if strings.Contains(text, "paths:") {
		t.Error("rule frontmatter leaked")
	}
	contains(t, text, "judgment and prose: gw/big", "sheet")
	contains(t, text, "interrogate reviewers: gw/big, gw/rev", "sheet")
	if strings.Contains(text, "STYLE-BODY") {
		t.Error("style in omp AGENTS.md")
	}
	cx := read(t, f.p("codex/AGENTS.md"))
	contains(t, cx, "STYLE-BODY", "codex style")
	contains(t, cx, "interrogate reviewers: rev, rev", "codex sheet")
	eq(t, strings.TrimSpace(read(t, f.p("omp/agent/APPEND_SYSTEM.md"))), "STYLE-BODY", "APPEND_SYSTEM")
}

func TestLaterLayerWins(t *testing.T) {
	f := newFixture(t)
	f.run("sync")
	if !strings.HasSuffix(read(t, f.p("claude/skills/trace/SKILL.md")), "extra trace\n") {
		t.Error("extra trace skill should win")
	}
}

func TestOmpRoles(t *testing.T) {
	f := newFixture(t)
	f.run("sync")
	cfg := readYAML(t, f.p("omp/agent/config.yml"))
	roles := cfg["modelRoles"].(map[string]any)
	eq(t, roles["review"], "gw/rev", "review")
	eq(t, roles["slow"], "gw/rev", "slow")
	eq(t, roles["default"], "gw/big", "default")
	eq(t, roles["smol"], "gw/small", "smol")
	eq(t, roles["fast"], "gw/small", "fast")
	eq(t, cfg["personality"], "pragmatic", "embedded omp-base.yml merged")
}

func TestGatewayConfigs(t *testing.T) {
	f := newFixture(t)
	f.run("sync")
	p := get(readYAML(t, f.p("omp/agent/models.yml")), "providers", "gw").(map[string]any)
	eq(t, p["apiKey"], "!gw-key", "apiKey")
	eq(t, p["api"], "openai-responses", "api")
	eq(t, p["disableStrictTools"], true, "disableStrictTools")
	eq(t, p["baseUrl"], "https://gw.example/v1", "baseUrl")
	hasBig(t, p)
	cx := readTOML(t, f.p("codex/config.toml"))
	eq(t, cx["model_provider"], "gw", "model_provider")
	eq(t, cx["model"], "rev", "codex model")
	eq(t, cx["model_reasoning_effort"], "high", "effort")
	eq(t, get(cx, "model_providers", "gw", "auth", "command"), "gw-key", "codex auth")
	eq(t, get(cx, "model_providers", "gw", "wire_api"), "responses", "wire_api")
	eq(t, get(cx, "model_providers", "gw", "base_url"), "https://gw.example/v1", "codex base_url")
	if _, ok := cx["mcp_servers"]; ok {
		t.Error("mcp_servers present")
	}
	cl := readJSON(t, f.p("claude/settings.json"))
	eq(t, cl["model"], "big[1m]", "claude model")
	eq(t, cl["apiKeyHelper"], "gw-key", "apiKeyHelper")
	eq(t, cl["outputStyle"], "i-have-adhd", "outputStyle")
	contains(t, fmt.Sprint(get(cl, "permissions", "allow")), "Bash(git push --force-with-lease *)", "allow")
	contains(t, fmt.Sprint(get(cl, "hooks", "SessionStart", 0, "hooks", 0, "command")), "herdr-agent-state.sh", "herdr hook")
	out := f.launch("--claude")
	eq(t, envVal(out, "ANTHROPIC_BASE_URL"), "https://gw.example", "ANTHROPIC_BASE_URL")
	eq(t, out.Argv[1:], []string{"--dangerously-skip-permissions"}, "argv")
}

func TestDefaultHarnessIsClaude(t *testing.T) {
	f := newFixture(t)
	out := f.launch("hello")
	eq(t, out.Argv[0], filepath.Join(f.tmp, "bin/claude"), "argv0")
	eq(t, out.Argv[len(out.Argv)-1], "hello", "last arg")
	contains(t, strings.Join(out.Argv, " "), "--dangerously-skip-permissions", "skip perms")
}

func TestOmpEnvAndModelFlag(t *testing.T) {
	f := newFixture(t)
	out := f.launchEnv(map[string]string{"HERDR_ENV": "1"}, "--omp", "--model", "review", "hello")
	eq(t, out.Argv, []string{filepath.Join(f.tmp, "bin/omp"), "--model", "gw/rev", "hello"}, "argv")
	eq(t, envVal(out, "PI_CODING_AGENT_DIR"), f.p("omp/agent"), "PI_CODING_AGENT_DIR")
	eq(t, realpath(t, filepath.Join(f.home, envVal(out, "PI_CONFIG_DIR"))), realpath(t, f.p("omp")), "PI_CONFIG_DIR")
	eq(t, envVal(out, "PI_FORCE_IMAGE_PROTOCOL"), "kitty", "image protocol")
	eq(t, f.launch("--codex", "exec", "x").Argv[1:], []string{"exec", "x"}, "codex argv")
	eq(t, envVal(f.launch("--codex"), "CODEX_HOME"), f.p("codex"), "CODEX_HOME")
}

func TestBinaryResolutionSkipsHarnessShims(t *testing.T) {
	f := newFixture(t)
	for _, h := range []string{"omp", "claude", "codex"} {
		eq(t, f.launch("--" + h).Argv[0], filepath.Join(f.tmp, "bin", h), h)
	}
}

func TestEnvGatewayOverridesManagedSettings(t *testing.T) {
	f := newFixture(t)
	out := f.launchEnv(map[string]string{"ANTHROPIC_BASE_URL": "https://env.example", "HARNESS_KEY_CMD": "env-key"}, "--claude")
	eq(t, envVal(out, "ANTHROPIC_BASE_URL"), "https://env.example", "env base wins")
	eq(t, readJSON(t, f.p("claude/settings.json"))["apiKeyHelper"], "env-key", "env key cmd wins")
	f.launchEnv(map[string]string{"ANTHROPIC_BASE_URL": "https://env.example"}, "--omp")
	eq(t, get(readYAML(t, f.p("omp/agent/models.yml")), "providers", "gw", "baseUrl"), "https://env.example/v1", "omp base")
	f.env["ANTHROPIC_BASE_URL"], f.env["HARNESS_KEY_CMD"] = "https://env.example", "env-key"
	doc := f.run("doctor")
	contains(t, doc, "https://env.example (env ANTHROPIC_BASE_URL)", "doctor gateway")
	contains(t, doc, "env-key (env HARNESS_KEY_CMD)", "doctor key")
}

func TestDoctorShowsManagedSources(t *testing.T) {
	f := newFixture(t)
	doc := f.run("doctor")
	contains(t, doc, "https://gw.example ("+filepath.Join(f.managed, "00-env.json")+")", "gateway source")
	contains(t, doc, "gw-key ("+filepath.Join(f.managed, "05-auth.json")+")", "key source")
}

func TestRigExecsRawRig(t *testing.T) {
	f := newFixture(t)
	out := f.launch("--rig")
	eq(t, out.Argv, []string{f.p("rig/npm/node_modules/.bin/rig")}, "bare rig")
	eq(t, envVal(out, "OPENRIG_HOME"), f.p("rig"), "OPENRIG_HOME")
	eq(t, envVal(out, "OPENRIG_NO_KERNEL"), "1", "OPENRIG_NO_KERNEL")
	eq(t, envVal(out, "OPENRIG_RUNTIME_CODEX_HOOKS_ENABLED"), "false", "codex activity hooks off")
	eq(t, envVal(out, "CLAUDE_CONFIG_DIR"), f.p("claude"), "seat CLAUDE_CONFIG_DIR")
	eq(t, envVal(out, "CODEX_HOME"), f.p("codex"), "seat CODEX_HOME")
	eq(t, envVal(out, "ANTHROPIC_BASE_URL"), "https://gw.example", "seat gateway")
	contains(t, read(t, f.p("claude/CLAUDE.md")), "# BASE", "rig renders claude config")
	contains(t, read(t, f.p("codex/config.toml")), "https://gw.example/v1", "rig renders codex config")
	if exists(f.p("rig/state/omp")) {
		t.Error("omp seats seeded")
	}
	eq(t, envVal(out, "HOME"), f.home, "HOME")
	// everything after --rig is rig's, even launcher flags and subcommand names
	eq(t, f.launch("--rig", "up", "x.yaml", "--model", "m", "doctor").Argv[1:], []string{"up", "x.yaml", "--model", "m", "doctor"}, "rig argv")
}

const twoSeats = "name: x\npods:\n  - id: pod\n    members:\n      - id: lead\n      - id: verifier\n"

func TestNothingWrittenOutsideState(t *testing.T) {
	f := newFixture(t)
	snap := func() []string {
		var out []string
		filepath.WalkDir(f.home, func(p string, _ fs.DirEntry, _ error) error { out = append(out, p); return nil })
		return out
	}
	before := snap()
	f.run("sync")
	for _, h := range []string{"omp", "claude", "codex", "rig"} {
		f.launchEnv(map[string]string{"HERDR_ENV": "1"}, "--"+h)
	}
	f.run("doctor")
	eq(t, snap(), before, "home untouched")
	var files []string
	filepath.WalkDir(f.root, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && d.Type().IsRegular() {
			files = append(files, d.Name())
		}
		return nil
	})
	want := []string{"AGENTS.md", "a.md", "i-have-adhd.md", "SKILL.md", "SKILL.md", "SKILL.md",
		"pstack-sheet.yaml", "rig.yaml"}
	sort.Strings(files)
	sort.Strings(want)
	eq(t, files, want, "root files")
}

func TestStandaloneWithEmbeddedModels(t *testing.T) {
	f := newFixture(t)
	delete(f.env, "HARNESS_LAYERS")
	delete(f.env, "HARNESS_MODELS")
	f.run("sync")
	eq(t, names(t, f.p("claude/skills")), []string{"only-base", "tdd", "trace"}, "skills")
	if !strings.HasSuffix(read(t, f.p("claude/skills/trace/SKILL.md")), "harness trace\n") {
		t.Error("harness trace should be used")
	}
	text := read(t, f.p("claude/CLAUDE.md"))
	contains(t, text, "# BASE", "base")
	if strings.Contains(text, "# EXTRA") {
		t.Error("extra layer leaked")
	}
	eq(t, readJSON(t, f.p("claude/settings.json"))["model"], "claude-opus-5-5[1m]", "model")
	eq(t, readTOML(t, f.p("codex/config.toml"))["model"], "gpt-6.1-sol", "codex model")
	eq(t, get(readYAML(t, f.p("omp/agent/config.yml")), "modelRoles", "default"), "bit"+"mex/claude-opus-5-5", "omp default")
}

func TestSuperprojectLayer(t *testing.T) {
	f := newFixture(t)
	sup, root := f.tmp, f.root
	git(t, root, "init", "-q")
	git(t, root, "commit", "-q", "--allow-empty", "-m", "x")
	git(t, sup, "init", "-q")
	git(t, sup, "add", "harness")
	agents := filepath.Join(sup, ".agents")
	md(t, agents+"/AGENTS.md", "# SUPER\n", nil)
	md(t, agents+"/rules/s.md", "RULE-SUPER\n", nil)
	md(t, agents+"/skills/x/SKILL.md", "super x\n", kv{"name": "x"})
	f.run("sync")
	if !strings.HasSuffix(read(t, f.p("claude/skills/x/SKILL.md")), "super x\n") {
		t.Error("super skill missing")
	}
	inOrder(t, read(t, f.p("claude/CLAUDE.md")), "# BASE", "# SUPER", "# EXTRA", "RULE-A", "RULE-SUPER", "RULE-EXTRA")
}

func TestMemoryIsClaudeAutoMemory(t *testing.T) {
	f := newFixture(t)
	f.run("sync")
	cl := readJSON(t, f.p("claude/settings.json"))
	eq(t, cl["autoMemoryEnabled"], true, "autoMemoryEnabled")
	for _, e := range []string{"UserPromptSubmit", "Stop", "SessionEnd"} {
		if get(cl, "hooks", e) != nil {
			t.Errorf("claude %s hook registered", e)
		}
	}
	eq(t, len(get(cl, "hooks", "SessionStart").([]any)), 1, "claude SessionStart groups")
	cx := readTOML(t, f.p("codex/config.toml"))
	cmd := "'" + f.p("bin/harness") + "' memory context"
	eq(t, get(cx, "hooks", "SessionStart", 0, "hooks", 0, "command"), cmd, "codex memory hook")
	eq(t, get(cx, "hooks", "SessionStart", 1), nil, "codex SessionStart groups")
	for e := range get(cx, "hooks").(map[string]any) {
		if e != "SessionStart" && e != "state" {
			t.Errorf("codex %s hook registered", e)
		}
	}
	key := f.p("codex/config.toml") + ":session_start:0:0"
	if h, _ := get(cx, "hooks", "state", key, "trusted_hash").(string); len(h) != len("sha256:")+64 {
		t.Errorf("codex memory hook untrusted: %v", get(cx, "hooks", "state"))
	}
	cfg := readYAML(t, f.p("omp/agent/config.yml"))
	eq(t, cfg["memory"], map[string]any{"backend": "off"}, "omp memory")
	if cfg["mnemopi"] != nil {
		t.Error("omp mnemopi block written")
	}
	for _, h := range []string{"--claude", "--codex", "--omp"} {
		g := newFixture(t)
		if out := g.runEnv(map[string]string{"HARNESS_DRY_RUN": "0", "HARNESS_DREAM": "1"}, h); strings.TrimSpace(out) != "real" {
			t.Fatalf("%s did not exec the harness: %q", h, out)
		}
		if _, err := os.Stat(g.p("memory")); err == nil {
			t.Errorf("%s created a memory dir", h)
		}
	}
}

func TestCodexHookTrustSurvivesRegeneration(t *testing.T) {
	f := newFixture(t)
	f.run("sync")
	cfg := f.p("codex/config.toml")
	write(t, cfg, read(t, cfg)+"\n[hooks.state.\"x:session_start:0:0\"]\ntrusted_hash = \"sha256:abc\"\n")
	f.run("sync")
	cx := readTOML(t, cfg)
	eq(t, get(cx, "hooks", "state", "x:session_start:0:0", "trusted_hash"), "sha256:abc", "trust kept")
}

func TestOpencodeRunsVanilla(t *testing.T) {
	f := newFixture(t)
	for _, flag := range []string{"--opencode", "--oc"} {
		eq(t, f.launch(flag, "run", "x").Argv, []string{filepath.Join(f.tmp, "bin/opencode"), "run", "x"}, flag)
	}
	if exists(f.p("opencode")) {
		t.Error("opencode state written")
	}
}

// ── new features ─────────────────────────────────────────────────────────────

func TestOpencodeVanillaIgnoresModelAndConfig(t *testing.T) {
	f := newFixture(t)
	for _, flag := range []string{"--opencode", "--oc"} {
		out := f.launch(flag, "--model", "review", "--", "run", "--model", "x")
		eq(t, out.Argv, []string{filepath.Join(f.tmp, "bin/opencode"), "run", "--model", "x"}, flag+" argv")
		for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "OPENRIG_HOME"} {
			eq(t, out.Env[k], (*string)(nil), flag+" "+k)
		}
	}
	if exists(f.state) && len(names(t, f.state)) > 0 {
		t.Errorf("opencode wrote state: %v", names(t, f.state))
	}
}

func TestHarnessDefaultPicksHarness(t *testing.T) {
	f := newFixture(t)
	out := f.launchEnv(map[string]string{"HARNESS_DEFAULT": "omp"}, "hello")
	eq(t, out.Argv, []string{filepath.Join(f.tmp, "bin/omp"), "hello"}, "argv")
	eq(t, envVal(out, "PI_CODING_AGENT_DIR"), f.p("omp/agent"), "omp env")
	eq(t, f.launchEnv(map[string]string{"HARNESS_DEFAULT": "omp"}, "--claude").Argv[0], filepath.Join(f.tmp, "bin/claude"), "flag wins")
}

func TestLeaseCapsHeavyRunsAcrossProcesses(t *testing.T) {
	f := newFixture(t)
	exit := func(args ...string) int {
		cmd := exec.Command(harnessBin, append([]string{"lease"}, args...)...)
		for k, v := range f.env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatal(err)
			}
			return ee.ExitCode()
		}
		return 0
	}
	codes := make(chan int, 12)
	for i := range 12 {
		go func() { codes <- exit("acquire", "box", "2", fmt.Sprintf("seat-%d", i)) }()
	}
	won := 0
	for range 12 {
		switch c := <-codes; c {
		case 0:
			won++
		case 75:
		default:
			t.Fatalf("acquire exit %d", c)
		}
	}
	eq(t, won, 2, "processes holding a cap-2 lease")
	lines := strings.Split(strings.TrimSpace(f.run("lease", "list", "box")), "\n")
	eq(t, len(lines), 2, "listed leases")
	holder := strings.Split(lines[0], "\t")[0]
	eq(t, exit("acquire", "box", "2", "late"), 75, "full exits 75")
	eq(t, exit("release", "box", holder), 0, "release")
	eq(t, exit("acquire", "box", "2", "late"), 0, "acquire after release")
	eq(t, exit("release", "box", "nobody"), 1, "release unheld")
	if !exists(f.p("rig/leases/box.json")) {
		t.Error("leases not under the rig state dir")
	}
}

func TestFreshRelaunchesTheCallingSeatDetached(t *testing.T) {
	f := newFixture(t)
	log := filepath.Join(f.tmp, "rig.log")
	bin := filepath.Join(f.tmp, "fakerig")
	write(t, filepath.Join(bin, "rig"), "#!/bin/sh\necho \"$@\" >> "+log+"\n")
	must(t, os.Chmod(filepath.Join(bin, "rig"), 0o755))
	start := time.Now()
	f.runEnv(map[string]string{"PATH": bin + ":" + f.env["PATH"], "OPENRIG_SESSION_NAME": "verify-reviewer-1@gg",
		"HARNESS_FRESH_DELAY": "300ms"}, "fresh")
	if time.Since(start) > 250*time.Millisecond {
		t.Error("fresh blocked on the relaunch")
	}
	if exists(log) {
		t.Fatal("relaunch ran before the delay")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !exists(log) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	eq(t, strings.TrimSpace(read(t, log)), "seat launch verify-reviewer-1@gg --fresh --stop --reason fresh context for the next row", "rig argv")
}

func TestFreshOutsideASeatFails(t *testing.T) {
	f := newFixture(t)
	cmd := exec.Command(harnessBin, "fresh")
	for k, v := range f.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("fresh without OPENRIG_SESSION_NAME succeeded")
	}
	contains(t, string(out), "OPENRIG_SESSION_NAME", "error names the missing seat")
}

func TestDiagramsLintsFilesAndFailsOnIssues(t *testing.T) {
	f := newFixture(t)
	good, bad := filepath.Join(f.tmp, "good.md"), filepath.Join(f.tmp, "bad.md")
	write(t, good, "# ok\n\n    ╭────╮\n    │ db │\n    ╰─┬──╯\n      ▼\n")
	write(t, bad, "# off by one\n\n    ╭────╮\n    ╰─┬──╯\n       │\n")
	eq(t, f.run("diagrams", good), "", "clean file prints nothing")
	cmd := exec.Command(harnessBin, "diagrams", good, bad)
	out, err := cmd.Output()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Fatalf("diagrams on a broken file: err %v, want exit 1", err)
	}
	inOrder(t, string(out), bad+":4:3: ┬ has an open down arm", bad+":5:4: │ reaches up into ─")
	if _, err := exec.Command(harnessBin, "diagrams", filepath.Join(f.tmp, "missing.md")).Output(); err == nil {
		t.Error("missing file succeeded")
	}
}

// claudeKey is Claude's project dir name for a short ASCII path.
func claudeKey(p string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(p, "-")
}

// dreamFixture's claude records the cwd of each headless (-p) call, and is "real" otherwise.
func dreamFixture(t *testing.T) (*fixture, string) {
	f := newFixture(t)
	calls := filepath.Join(f.tmp, "dream-calls")
	write(t, filepath.Join(f.tmp, "bin", "claude"), "#!/bin/sh\n"+
		"if [ \"$1\" = -p ]; then cat > /dev/null; echo \"$PWD\" >> "+calls+"; echo 'added    x: y'; exit 0; fi\necho real\n")
	must(t, os.Chmod(filepath.Join(f.tmp, "bin", "claude"), 0o755))
	proj := filepath.Join(f.tmp, "proj")
	must(t, os.MkdirAll(proj, 0o755))
	rec, _ := json.Marshal(map[string]any{"type": "user", "sessionId": "s1", "cwd": proj,
		"timestamp": time.Now().Add(-time.Hour).Format(time.RFC3339Nano),
		"message":   map[string]any{"role": "user", "content": "always use make up"}})
	write(t, f.p("claude/projects/"+claudeKey(proj)+"/s1.jsonl"), string(rec)+"\n")
	return f, calls
}

// waitFor polls until cond holds or 10s pass.
func waitFor(cond func() bool) bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func TestStaleDreamSpawnsOnceAndLaunchDoesNotWait(t *testing.T) {
	f, calls := dreamFixture(t)
	real := map[string]string{"HARNESS_DRY_RUN": "0"}
	start := time.Now()
	for _, h := range []string{"--claude", "--codex", "--omp"} {
		eq(t, strings.TrimSpace(f.runEnv(real, h)), "real", h+" execs")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("three launches took %s", d)
	}
	if !waitFor(func() bool { return strings.Contains(read(t, f.p("dream/dream.log")), "dream done") }) {
		t.Fatalf("dream never finished: %s", read(t, f.p("dream/dream.log")))
	}
	got := strings.Fields(read(t, calls))
	want := filepath.Join(f.p("claude/projects"), claudeKey(filepath.Join(f.tmp, "proj")), "memory")
	eq(t, got, []string{want}, "dream calls across three launches")
	if !exists(f.p("dream/last")) {
		t.Error("last not stamped")
	}
	reports, _ := filepath.Glob(f.p("dream/*/*.md"))
	if len(reports) != 1 || !strings.Contains(read(t, reports[0]), "added    x: y") {
		t.Errorf("reports = %v", reports)
	}
}

func TestNoDreamSpawn(t *testing.T) {
	for name, c := range map[string]struct {
		env  map[string]string
		args []string
		last bool
	}{
		"fresh stamp":    {map[string]string{"HARNESS_DRY_RUN": "0"}, []string{"--claude"}, true},
		"inside a dream": {map[string]string{"HARNESS_DRY_RUN": "0", "HARNESS_DREAM": "1"}, []string{"--claude"}, false},
		"opencode":       {map[string]string{"HARNESS_DRY_RUN": "0"}, []string{"--opencode"}, false},
		"dry run":        {nil, []string{"--claude"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			f, calls := dreamFixture(t)
			if c.last {
				write(t, f.p("dream/last"), time.Now().UTC().Format(time.RFC3339Nano)+"\n")
			}
			f.runEnv(c.env, c.args...)
			time.Sleep(500 * time.Millisecond)
			if exists(f.p("dream/dream.log")) || exists(calls) {
				t.Error("a dream was spawned")
			}
		})
	}
}

func TestMemoryCLIWithoutRoot(t *testing.T) {
	f := newFixture(t)
	proj := filepath.Join(f.tmp, "proj")
	must(t, os.MkdirAll(filepath.Join(proj, "sub"), 0o755))
	git(t, proj, "init", "-q")
	memdir := f.p("claude/projects/" + claudeKey(proj) + "/memory")
	run := func(dir, stdin string, args ...string) (string, string, error) {
		cmd := exec.Command(harnessBin, args...)
		cmd.Dir, cmd.Env = dir, []string{"HOME=" + f.home, "HARNESS_STATE=" + f.state, "PATH=/usr/bin:/bin"}
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		return out.String(), errb.String(), err
	}
	out, _, err := run(filepath.Join(proj, "sub"), "", "memory", "path")
	must(t, err)
	eq(t, out, memdir+"\n", "memory path from a subdir")
	out, _, err = run(f.tmp, "", "memory", "path", "proj/sub")
	must(t, err)
	eq(t, out, memdir+"\n", "memory path for a relative dir")

	payload, _ := json.Marshal(map[string]any{"cwd": proj, "hook_event_name": "SessionStart"})
	out, _, err = run(f.tmp, string(payload), "memory", "context")
	must(t, err)
	eq(t, out, "", "empty memory injects nothing")
	write(t, filepath.Join(memdir, "MEMORY.md"), "- [Make](make.md) - always use make up\n")
	out, _, err = run(f.tmp, string(payload), "memory", "context")
	must(t, err)
	var hook map[string]any
	must(t, json.Unmarshal([]byte(out), &hook))
	ctx, _ := get(hook, "hookSpecificOutput", "additionalContext").(string)
	contains(t, ctx, "- [Make](make.md) - always use make up", "injected index")
	contains(t, ctx, memdir, "injected dir")

	out, stderr, err := run(f.tmp, "not json", "memory", "context")
	if err != nil || out != "" || !strings.Contains(stderr, "hook payload") {
		t.Errorf("bad payload: err %v, out %q, stderr %q; want exit 0 with the error on stderr", err, out, stderr)
	}
	if _, _, err := run(f.tmp, "", "memory", "bogus"); err == nil {
		t.Error("memory bogus succeeded")
	}
}

func TestDreamGuardExitCodes(t *testing.T) {
	dir := "/st/claude/projects/k/memory"
	for payload, want := range map[string]int{
		`{"tool_name":"Write","tool_input":{"file_path":"/st/claude/projects/k/memory/a.md"}}`: 0,
		`{"tool_name":"Write","tool_input":{"file_path":"/tmp/a.md"}}`:                         2,
		`garbage`: 2,
	} {
		cmd := exec.Command(harnessBin, "dream", "guard", dir)
		cmd.Env, cmd.Stdin = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}, strings.NewReader(payload)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatal(err)
			}
			code = ee.ExitCode()
		}
		if code != want || want == 2 && !strings.Contains(stderr.String(), "dream guard") {
			t.Errorf("guard %s: exit %d stderr %q, want exit %d", payload, code, stderr.String(), want)
		}
	}
}
