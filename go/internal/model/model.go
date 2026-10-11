// Package model loads the model catalog and the gateway, and answers per-harness model questions.
package model

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	assets "github.com/Quentin-M/harness"
	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/sys"
)

// ManagedSettings is Claude Code's managed settings drop-in dir, the gateway's source of truth.
const ManagedSettings = "/Library/Application Support/ClaudeCode/managed-settings.d"

// Config is models.yaml plus the gateway it is served through.
type Config struct {
	Raw     map[string]any
	BaseURL string // Anthropic-style gateway root, no /v1
	KeyCmd  string
	BaseSrc string // where BaseURL came from
	KeySrc  string // where KeyCmd came from
}

// ModelSpec is one entry of `models`.
type ModelSpec struct {
	ID    string
	Attrs map[string]any
}

// Load reads HARNESS_MODELS or the embedded models.yaml, then resolves the gateway.
func Load(env sys.Env) (*Config, error) {
	data, src := assets.Models, "embedded models.yaml"
	if p := env.Get("HARNESS_MODELS"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		data, src = b, p
	}
	raw, err := fsx.ParseYAML(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	c := &Config{Raw: raw}
	dir := env.Get("HARNESS_MANAGED_SETTINGS")
	if dir == "" {
		dir = ManagedSettings
	}
	menv, helper, err := managed(dir)
	if err != nil {
		return nil, err
	}
	c.BaseURL, c.BaseSrc = pick(env.Get("ANTHROPIC_BASE_URL"), "env ANTHROPIC_BASE_URL", menv["ANTHROPIC_BASE_URL"])
	c.KeyCmd, c.KeySrc = pick(env.Get("HARNESS_KEY_CMD"), "env HARNESS_KEY_CMD", helper)
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	return c, nil
}

type sourced struct{ val, src string }

func pick(envVal, envSrc string, m sourced) (string, string) {
	if envVal != "" {
		return envVal, envSrc
	}
	return m.val, m.src
}

// managed merges dir/*.json in filename order: `env` keys and `apiKeyHelper`, later files win.
func managed(dir string) (map[string]sourced, sourced, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	env, helper := map[string]sourced{}, sourced{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, helper, err
		}
		var s struct {
			Env          map[string]string `json:"env"`
			APIKeyHelper string            `json:"apiKeyHelper"`
		}
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, helper, fmt.Errorf("%s: %w", f, err)
		}
		for k, v := range s.Env {
			env[k] = sourced{v, f}
		}
		if s.APIKeyHelper != "" {
			helper = sourced{s.APIKeyHelper, f}
		}
	}
	return env, helper, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// Str is a top-level field as a string, "" when unset.
func (c *Config) Str(key string) string { return str(c.Raw[key]) }

func (c *Config) Map(key string) map[string]any {
	m, _ := c.Raw[key].(map[string]any)
	return m
}

func (c *Config) Provider() string { return c.Str("provider") }

// OpenAIURL is the gateway's OpenAI-style base, "" without a gateway.
func (c *Config) OpenAIURL() string {
	if c.BaseURL == "" {
		return ""
	}
	return c.BaseURL + "/v1"
}

// roles is `roles` with `overrides[harness]` on top.
func (c *Config) roles(harness string) map[string]any {
	over, _ := c.Map("overrides")[harness].(map[string]any)
	roles := c.Map("roles")
	if roles == nil {
		roles = map[string]any{}
	}
	if over == nil {
		return roles
	}
	return fsx.Merge(roles, over)
}

// RoleNames lists `roles` keys in yaml order.
func (c *Config) RoleNames() []string { return fsx.Keys(c.Map("roles")) }

// RoleModel is the model id for role, falling back to the default role when unset or empty.
func (c *Config) RoleModel(harness, role string) string {
	roles := c.roles(harness)
	if v := str(roles[role]); v != "" {
		return v
	}
	return str(roles["default"])
}

// Ref names id the way harness expects: omp `provider/id`, everything else the bare id.
func (c *Config) Ref(harness, id string) string {
	if harness == "omp" {
		return c.Provider() + "/" + id
	}
	return id
}

// Resolve is the harness reference for role.
func (c *Config) Resolve(harness, role string) string {
	return c.Ref(harness, c.RoleModel(harness, role))
}

// ResolveFlag maps a --model value: a known role resolves, anything else passes through as an id.
func (c *Config) ResolveFlag(harness, v string) string {
	if id, ok := c.roles(harness)[v]; ok {
		return c.Ref(harness, str(id))
	}
	return v
}

// Models lists `models` in yaml order; a null entry has empty Attrs.
func (c *Config) Models() []ModelSpec {
	models := c.Map("models")
	out := make([]ModelSpec, 0, len(models))
	for _, id := range fsx.Keys(models) {
		attrs, _ := models[id].(map[string]any)
		if attrs == nil {
			attrs = map[string]any{}
		}
		out = append(out, ModelSpec{ID: id, Attrs: attrs})
	}
	return out
}

// Attrs is the catalog entry for id, nil when unknown.
func (c *Config) Attrs(id string) map[string]any {
	m, _ := c.Map("models")[id].(map[string]any)
	return m
}

// KeyValue is the gateway key: KeyCmd's trimmed stdout, "" without one.
func (c *Config) KeyValue(s *sys.Sys) string {
	if c.KeyCmd == "" {
		return ""
	}
	return s.Shell(c.KeyCmd)
}
