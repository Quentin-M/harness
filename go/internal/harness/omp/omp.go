// Package omp renders oh-my-pi config into $HARNESS_STATE/omp and launches omp.
package omp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	{"default", "default"}, {"smol", "fast"}, {"slow", "slow"}, {"plan", "default"}, {"task", "default"},
	{"advisor", "advisor"}, {"vision", "vision"}, {"commit", "fast"}, {"tiny", "tiny"},
	{"memory", "tiny"}, {"judge", "tiny"}, {"review", "review"}, {"fast", "fast"},
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
	have, err := readConfig(filepath.Join(d, "config.yml"))
	if err != nil {
		return err
	}
	// omp's /settings and `omp config set` write here; keep those edits across relaunches.
	// omp-base.yml only seeds defaults, the harness-owned keys below always win.
	cfg := fsx.Merge(fsx.Merge(base, have), fsx.OMap("skills", fsx.OMap("enableAgentsUser", false),
		"memory", fsx.OMap("backend", "off")))
	// models.yaml owns the roles: replace them whole so a role the harness dropped doesn't linger.
	fsx.Put(cfg, "modelRoles", fsx.OMap(kv...))
	files := []struct {
		name string
		data []byte
	}{
		{"config.yml", fsx.DumpYAML(cfg)},
		{"models.yml", fsx.DumpYAML(fsx.OMap("providers", Providers(ctx.Models)))},
		{"AGENTS.md", []byte(ctx.Layers.Instructions(ctx.Models, "omp"))},
		{"APPEND_SYSTEM.md", []byte(StyleBody(ctx))},
		{"WATCHDOG.yml", Watchdog(ctx.Models)},
	}
	for _, f := range files {
		if err := fsx.Write(filepath.Join(d, f.name), f.data); err != nil {
			return err
		}
	}
	return ctx.Layers.LinkSkills(filepath.Join(d, "skills"))
}

// Watchdog is omp's WATCHDOG.yml: a direction advisor on the advisor model and a strict
// reviewer on the review model, both resolved from models.yaml so ids never drift.
func Watchdog(m *model.Config) []byte {
	return fsx.DumpYAML(fsx.OMap("advisors", []any{
		fsx.OMap("name", "Advisor", "model", m.Resolve("omp", "advisor"),
			"instructions", "Steer direction: wrong approach, missed constraints, scope drift, hallucinated APIs.\n"),
		fsx.OMap("name", "Reviewer", "model", m.Resolve("omp", "review")+":high",
			"tools", []any{"read", "grep", "glob", "lsp"}, "maxNotesPerUpdate", 3,
			"instructions", "Review every change the primary makes as a strict code reviewer.\n"+
				"After each edit/write, read the touched code and check correctness, edge cases,\n"+
				"repo conventions (CLAUDE.md), missing/weak tests, and unverified claims.\n"+
				"Raise `blocker` when work is handed off broken, untested, or contradicting the evidence.\n"),
	}))
}

// readConfig parses an existing omp config.yml; a missing file is an empty map.
func readConfig(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := fsx.ParseYAML(b)
	if err != nil {
		return nil, fmt.Errorf("omp/config.yml: %w", err)
	}
	return m, nil
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
	rest, err := resumeBare(ctx, args)
	if err != nil {
		return nil, err
	}
	return append(argv, rest...), nil
}

// resumeBare points `--resume <id>` at the session file in bare omp's ~/.omp/agent when the
// harness store has no session with that id prefix: herdr restores and bare omp write there.
// An explicit --profile or --session-dir picks its own store, so those launches pass through.
func resumeBare(ctx *harness.Ctx, args []string) ([]string, error) {
	at, id := -1, ""
scan:
	for i, a := range args {
		switch {
		case a == "--":
			break scan
		case a == "--profile" || a == "--session-dir" ||
			strings.HasPrefix(a, "--profile=") || strings.HasPrefix(a, "--session-dir="):
			return args, nil
		case at < 0 && (a == "--resume" || a == "-r") && i+1 < len(args):
			at, id = i+1, args[i+1]
		case at < 0 && strings.HasPrefix(a, "--resume="):
			at, id = i, strings.TrimPrefix(a, "--resume=")
		}
	}
	if xdg := ctx.Env["XDG_DATA_HOME"]; xdg != "" && exists(filepath.Join(xdg, "omp")) {
		return args, nil // a harness launch keeps omp's default config root, so it shares this store
	}
	if at < 0 || !sessionID.MatchString(id) || len(sessionFiles(filepath.Join(AgentDir(ctx.State), "sessions"), id)) > 0 {
		return args, nil
	}
	bareDir := filepath.Join(ctx.Sys.Home, ".omp", "agent")
	bare := sessionFiles(filepath.Join(bareDir, "sessions"), id)
	if len(bare) != 1 {
		return args, nil
	}
	if err := linkBlobs(bare[0], filepath.Join(bareDir, "blobs"), filepath.Join(AgentDir(ctx.State), "blobs")); err != nil {
		return nil, fmt.Errorf("omp: resume %s: %w", bare[0], err)
	}
	out := slices.Clone(args)
	if out[at] = bare[0]; strings.HasPrefix(args[at], "--resume=") {
		out[at] = "--resume=" + bare[0]
	}
	return out, nil
}

// sessionID is an omp session id or id prefix; anything else (paths, flags) is omp's to resolve.
var sessionID = regexp.MustCompile(`^[0-9a-fA-F][0-9a-fA-F-]{3,35}$`)

// sessionFiles are the top-level session files under dir whose id starts with prefix.
func sessionFiles(dir, prefix string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "*", "*_"+strings.ToLower(prefix)+"*.jsonl"))
	return m
}

var blobRef = regexp.MustCompile(`blob:sha256:([0-9a-f]{64})`)

// linkBlobs hard-links the blobs a session references from src into dst, where omp reads them.
// Blobs are content-addressed, so an existing name in dst already holds the same bytes.
func linkBlobs(session, src, dst string) error {
	data, err := os.ReadFile(session)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, m := range blobRef.FindAllSubmatch(data, -1) {
		hash := string(m[1])
		if seen[hash] {
			continue
		}
		seen[hash] = true
		files, _ := filepath.Glob(filepath.Join(src, hash+"*")) // the blob and its .ext display copy
		for _, from := range files {
			to := filepath.Join(dst, filepath.Base(from))
			if _, err := os.Lstat(to); err == nil {
				continue
			}
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			if err := os.Link(from, to); err != nil && !errors.Is(err, os.ErrExist) {
				if err := copyBlob(from, to); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// copyBlob publishes from at to through a private temp file, so a concurrent resume sees the whole
// blob or none; a rename over a racing copy replaces it with identical bytes.
func copyBlob(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".blob-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
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
