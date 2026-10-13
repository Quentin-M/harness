// Package omp renders oh-my-pi config into $HARNESS_STATE/omp and launches omp.
package omp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	assets "github.com/Quentin-M/harness"
	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/model"
)

// H is the omp harness.
type H struct{}

func init() { harness.Register(H{}) }

// Roles maps omp modelRoles keys to models.yaml roles.
var Roles = [][2]string{
	{"default", "default"}, {"smol", "fast"}, {"slow", "review"}, {"plan", "default"}, {"task", "default"},
	{"advisor", "default"}, {"vision", "vision"}, {"image", "vision"}, {"commit", "vision"}, {"tiny", "tiny"},
	{"memory", "tiny"}, {"review", "review"}, {"fast", "fast"},
}

// API maps a models.yaml api to omp's provider api.
var API = map[string]string{"anthropic": "anthropic-messages", "openai": "openai-responses"}

var modelKeys = map[string]string{"name": "name", "context": "contextWindow", "max_tokens": "maxTokens",
	"reasoning": "reasoning", "input": "input"}

func (H) Name() string { return "omp" }

// AgentDir is omp's PI_CODING_AGENT_DIR under the state dir.
func AgentDir(state string) string { return filepath.Join(state, "omp", "agent") }

// StyleBody is the output style without frontmatter, left-trimmed (omp APPEND_SYSTEM.md).
func StyleBody(ctx *harness.Ctx) string {
	_, body := fsx.SplitFrontmatter(ctx.Layers.Style())
	return strings.TrimLeftFunc(body, unicode.IsSpace)
}

// Providers is omp's models.yml providers map for the gateway; empty without one.
func Providers(m *model.Config) map[string]any {
	if m.OpenAIURL() == "" {
		return fsx.OMap()
	}
	key := any("not-required")
	if m.KeyCmd != "" {
		key = "!" + m.KeyCmd
	}
	prov := fsx.OMap("baseUrl", m.OpenAIURL(), "apiKey", key, "api", api(str(m.Raw["api"], "openai")))
	if b, _ := m.Raw["disable_strict_tools"].(bool); b {
		prov["disableStrictTools"] = true
	}
	models := []any{}
	for _, ms := range m.Models() {
		kv := []any{"id", ms.ID}
		if a, ok := ms.Attrs["api"]; ok {
			kv = append(kv, "api", api(fmt.Sprint(a)))
		}
		for _, k := range fsx.Keys(ms.Attrs) {
			if to, ok := modelKeys[k]; ok {
				kv = append(kv, to, ms.Attrs[k])
			}
		}
		models = append(models, fsx.OMap(kv...))
	}
	prov["models"] = models
	return fsx.OMap(m.Provider(), prov)
}

func (H) Sync(ctx *harness.Ctx) error {
	d := AgentDir(ctx.State)
	var keys []string
	roles := map[string]string{}
	add := func(k, r string) {
		if _, ok := roles[k]; !ok {
			keys = append(keys, k)
		}
		roles[k] = r
	}
	for _, r := range ctx.Models.RoleNames() {
		add(r, r)
	}
	for _, kr := range Roles {
		add(kr[0], kr[1])
	}
	var kv []any
	for _, k := range keys {
		kv = append(kv, k, ctx.Models.Resolve("omp", roles[k]))
	}
	base, err := fsx.ParseYAML(assets.OmpBase)
	if err != nil {
		return err
	}
	over := fsx.OMap("modelRoles", fsx.OMap(kv...), "skills", fsx.OMap("enableAgentsUser", false),
		"memory", fsx.OMap("backend", "off"))
	files := []struct {
		name string
		data []byte
	}{
		{"config.yml", fsx.DumpYAML(fsx.Merge(base, over))},
		{"models.yml", fsx.DumpYAML(fsx.OMap("providers", Providers(ctx.Models)))},
		{"AGENTS.md", []byte(ctx.Layers.Instructions(ctx.Models, "omp"))},
		{"APPEND_SYSTEM.md", []byte(StyleBody(ctx))},
	}
	for _, f := range files {
		if err := fsx.Write(filepath.Join(d, f.name), f.data); err != nil {
			return err
		}
	}
	return ctx.Layers.LinkSkills(filepath.Join(d, "skills"))
}

func (H) Env(ctx *harness.Ctx) error {
	delete(ctx.Env, "OMP_PROFILE")
	delete(ctx.Env, "PI_PROFILE")
	ctx.Env["PI_CODING_AGENT_DIR"] = AgentDir(ctx.State)
	rel, err := filepath.Rel(ctx.Sys.Home, filepath.Join(ctx.State, "omp"))
	if err != nil {
		return err
	}
	ctx.Env["PI_CONFIG_DIR"] = rel
	if ctx.Env["HERDR_ENV"] == "1" || ctx.Env["TERM_PROGRAM"] == "herdr" {
		if _, ok := ctx.Env["PI_FORCE_IMAGE_PROTOCOL"]; !ok {
			ctx.Env["PI_FORCE_IMAGE_PROTOCOL"] = "kitty"
		}
	}
	return nil
}

func (H) Argv(ctx *harness.Ctx, mdl string, args []string) ([]string, error) {
	herdrInstall(ctx)
	bin := ctx.Sys.FindBin("omp")
	if bin == "" {
		return nil, errors.New("harness: omp not found on PATH")
	}
	argv := []string{bin}
	if mdl != "" {
		argv = append(argv, "--model", ctx.Models.ResolveFlag("omp", mdl))
	}
	return append(argv, args...), nil
}

const herdrExt = "extensions/herdr-omp-agent-state.ts"

// herdrInstall puts herdr's omp agent-state extension in the state dir. herdr maps
// PI_CODING_AGENT_DIR to pi, so it installs under a scratch HOME and the file is copied over.
func herdrInstall(ctx *harness.Ctx) {
	dst := filepath.Join(AgentDir(ctx.State), herdrExt)
	herdr := ctx.Sys.Which("herdr")
	if ctx.Env["HERDR_ENV"] != "1" || herdr == "" || exists(dst) {
		return
	}
	scratch := filepath.Join(ctx.State, "omp", "herdr-home")
	if os.MkdirAll(filepath.Join(scratch, ".omp", "agent"), 0o755) != nil {
		return
	}
	env := ctx.Env.Clone()
	env.Unset("PI_CODING_AGENT_DIR")
	env.Unset("PI_CONFIG_DIR")
	env.Set("HOME", scratch)
	cmd := exec.Command(herdr, "integration", "install", "omp")
	cmd.Env = env.Environ()
	_ = cmd.Run()
	if data, err := os.ReadFile(filepath.Join(scratch, ".omp", "agent", herdrExt)); err == nil {
		_ = fsx.Write(dst, data)
	}
}

func api(name string) string {
	if v, ok := API[name]; ok {
		return v
	}
	return name
}

func str(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
