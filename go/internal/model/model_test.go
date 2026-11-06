package model

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Quentin-M/harness/internal/sys"
)

const models = `provider: gw
models:
  big: {api: anthropic, context: 1000000}
  rev: {context: 500000}
  small:
roles: {default: big, review: rev, fast: small, empty: ""}
overrides:
  codex: {default: rev}
  omp: {fast: "small:low", advisor: "big:medium", slow: "big:high"}
`

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture: a models file and a managed-settings dir split the way the real one is.
func fixture(t *testing.T) sys.Env {
	t.Helper()
	d := t.TempDir()
	write(t, filepath.Join(d, "models.yaml"), models)
	ms := filepath.Join(d, "managed")
	write(t, filepath.Join(ms, "00-env.json"), `{"env": {"ANTHROPIC_BASE_URL": "https://old.example", "X": "1"}}`)
	write(t, filepath.Join(ms, "05-auth.json"), `{"apiKeyHelper": "/usr/bin/gw-key"}`)
	write(t, filepath.Join(ms, "10-env.json"), `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example/"}, "permissions": {"allow": []}}`)
	write(t, filepath.Join(ms, "README"), "not json")
	return sys.Env{"HARNESS_MODELS": filepath.Join(d, "models.yaml"), "HARNESS_MANAGED_SETTINGS": ms}
}

func load(t *testing.T, env sys.Env) *Config {
	t.Helper()
	c, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGatewayFromManagedSettings(t *testing.T) {
	env := fixture(t)
	c := load(t, env)
	ms := env["HARNESS_MANAGED_SETTINGS"]
	if c.BaseURL != "https://gw.example" || c.BaseSrc != filepath.Join(ms, "10-env.json") {
		t.Errorf("base = %q from %q", c.BaseURL, c.BaseSrc)
	}
	if c.KeyCmd != "/usr/bin/gw-key" || c.KeySrc != filepath.Join(ms, "05-auth.json") {
		t.Errorf("key = %q from %q", c.KeyCmd, c.KeySrc)
	}
	if c.OpenAIURL() != "https://gw.example/v1" {
		t.Errorf("openai = %q", c.OpenAIURL())
	}
}

func TestEnvOverridesManagedSettings(t *testing.T) {
	env := fixture(t)
	env["ANTHROPIC_BASE_URL"], env["HARNESS_KEY_CMD"] = "https://env.example", "echo k"
	c := load(t, env)
	if c.BaseURL != "https://env.example" || c.BaseSrc != "env ANTHROPIC_BASE_URL" || c.KeyCmd != "echo k" ||
		c.KeySrc != "env HARNESS_KEY_CMD" {
		t.Errorf("%+v", c)
	}
}

// A base URL from one source and a mint URL from the other would mint a key for the wrong gateway.
func TestKeyEnvPairComesFromTheBaseURLSource(t *testing.T) {
	env := fixture(t)
	write(t, filepath.Join(env["HARNESS_MANAGED_SETTINGS"], "90-canary.json"),
		`{"env": {"ANTHROPIC_BASE_URL": "https://agw.example", "LITELLM_URL": "https://agw-mint.example"}}`)
	managed := map[string]string{"ANTHROPIC_BASE_URL": "https://agw.example", "LITELLM_URL": "https://agw-mint.example",
		KeyEnvTag: "managed"}
	mine := map[string]string{"ANTHROPIC_BASE_URL": "https://mine.example", "LITELLM_URL": "https://mine-mint.example"}
	for _, tc := range []struct {
		name  string
		shell map[string]string
		want  map[string]string
	}{
		{"managed pair", nil, managed},
		{"shell pair", mine, mine},
		{"shell base alone", map[string]string{"ANTHROPIC_BASE_URL": "https://old.example"},
			map[string]string{"ANTHROPIC_BASE_URL": "https://old.example"}},
		{"shell mint alone", map[string]string{"LITELLM_URL": "https://stale-mint.example"}, managed},
		{"pair a parent launch exported from managed", map[string]string{"ANTHROPIC_BASE_URL": "https://old.example",
			"LITELLM_URL": "https://agw-mint.example", KeyEnvTag: "managed"}, managed},
	} {
		e := maps.Clone(env)
		maps.Copy(e, tc.shell)
		if c := load(t, e); !maps.Equal(c.KeyEnv, tc.want) {
			t.Errorf("%s: KeyEnv = %v, want %v", tc.name, c.KeyEnv, tc.want)
		}
	}
}

func TestNoGateway(t *testing.T) {
	env := fixture(t)
	env["HARNESS_MANAGED_SETTINGS"] = t.TempDir()
	c := load(t, env)
	if c.BaseURL != "" || c.KeyCmd != "" || c.OpenAIURL() != "" {
		t.Errorf("%+v", c)
	}
}

func TestBadManagedSettings(t *testing.T) {
	env := fixture(t)
	write(t, filepath.Join(env["HARNESS_MANAGED_SETTINGS"], "20-bad.json"), "{")
	if _, err := Load(env); err == nil {
		t.Fatal("bad json accepted")
	}
}

func TestEmbeddedCatalog(t *testing.T) {
	c := load(t, sys.Env{"HARNESS_MANAGED_SETTINGS": t.TempDir(), "ANTHROPIC_BASE_URL": "https://gw.example"})
	want := map[string]string{"default": "claude-opus-5-5", "review": "gpt-6.1-sol", "fast": "claude-sonnet-5-5",
		"vision": "gpt-5.6-terra", "tiny": "gpt-5.6-luna"}
	for role, id := range want {
		if got := c.RoleModel("claude", role); got != id {
			t.Errorf("%s = %s, want %s", role, got, id)
		}
		if c.Attrs(id) == nil {
			t.Errorf("%s missing from the catalog", id)
		}
	}
	omp := map[string]string{"default": "claude-opus-5-5", "fast": "claude-sonnet-5-5",
		"smol": "deepseek-v4.1-flash-1017:low", "tiny": "deepseek-v4.1-flash-1017",
		"slow": "claude-opus-5-5:high", "advisor": "gpt-6.1-sol:high", "review": "gpt-6.1-sol"}
	for role, ref := range omp {
		if got := c.Resolve("omp", role); got != "bitmex/"+ref {
			t.Errorf("omp %s = %s, want bitmex/%s", role, got, ref)
		}
	}
	for _, h := range []string{"claude", "codex"} {
		if got := c.RoleModel(h, "advisor"); got != c.RoleModel(h, "default") {
			t.Errorf("%s advisor = %s, want the %s default", h, got, h)
		}
	}
	if c.Attrs("deepseek-v4.1-flash-1017") == nil || c.Attrs("claude-opus-5-5") == nil {
		t.Error("omp override ids missing from the catalog")
	}
	if c.RoleModel("codex", "default") != "gpt-6.1-sol" || c.Provider() != "bit"+"mex" {
		t.Error("codex override or provider")
	}
}

// On subscription logins every role must land on a provider that harness can sign in to.
func TestEmbeddedCatalogOnSubscriptions(t *testing.T) {
	c := load(t, sys.Env{"HARNESS_MANAGED_SETTINGS": t.TempDir()})
	for _, role := range []string{"default", "review", "fast", "smol", "advisor", "vision", "tiny", "slow"} {
		if got := c.Resolve("omp", role); !strings.HasPrefix(got, "anthropic/") && !strings.HasPrefix(got, "openai-codex/") {
			t.Errorf("omp %s = %s, want an anthropic/ or openai-codex/ ref", role, got)
		}
		if got := c.RoleModel("claude", role); c.subscription(got) != "anthropic" {
			t.Errorf("claude %s = %s, which no claude login serves", role, got)
		}
		if got := c.RoleModel("codex", role); c.subscription(got) != "openai-codex" {
			t.Errorf("codex %s = %s, which no codex login serves", role, got)
		}
	}
	if got := c.Resolve("omp", "review"); got != "openai-codex/gpt-6.1-sol" {
		t.Errorf("omp review = %s: the other family should stay reachable", got)
	}
	if got := c.Resolve("omp", "smol"); got != "anthropic/claude-sonnet-5-5:low" {
		t.Errorf("omp smol = %s, want the DeepSeek fallback, Sonnet, at its thinking level", got)
	}
}

func TestResolution(t *testing.T) {
	c := load(t, fixture(t))
	cases := [][3]string{
		{"omp", "default", "gw/big"}, {"omp", "review", "gw/rev"}, {"claude", "default", "big"},
		{"codex", "default", "rev"},
		{"omp", "fast", "gw/small:low"}, {"omp", "advisor", "gw/big:medium"}, {"claude", "advisor", "big"}, {"omp", "slow", "gw/big:high"}, {"claude", "slow", "big"},
		{"codex", "advisor", "rev"}, {"claude", "fast", "small"}, {"omp", "unknown-role", "gw/big"}, {"omp", "empty", "gw/big"},
	}
	for _, cs := range cases {
		if got := c.Resolve(cs[0], cs[1]); got != cs[2] {
			t.Errorf("%v: %s", cs, got)
		}
	}
	if c.ResolveFlag("omp", "review") != "gw/rev" || c.ResolveFlag("omp", "some/id") != "some/id" {
		t.Fatal("ResolveFlag")
	}
	var ids []string
	for _, m := range c.Models() {
		ids = append(ids, m.ID)
	}
	if !reflect.DeepEqual(ids, []string{"big", "rev", "small"}) || len(c.Models()[2].Attrs) != 0 {
		t.Fatal(c.Models())
	}
	if got := c.RoleNames(); !reflect.DeepEqual(got, []string{"default", "review", "fast", "empty"}) {
		t.Fatal(got)
	}
}

func TestKeyValue(t *testing.T) {
	s := sys.New(sys.Env{"PATH": "/usr/bin:/bin", "HOME": t.TempDir()})
	if got := (&Config{KeyCmd: "echo '  k1 '"}).KeyValue(s); got != "k1" {
		t.Fatal(got)
	}
	if got := (&Config{}).KeyValue(s); got != "" {
		t.Fatal(got)
	}
}
