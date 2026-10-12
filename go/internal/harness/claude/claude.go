// Package claude renders Claude Code's config dir and launches it.
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/model"
)

// Style is the output style every harness ships.
const Style = "i-have-adhd"

const herdrHook = `f="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hooks/herdr-agent-state.sh"; ` +
	`[ -f "$f" ] || exit 0; bash "$f" session`

type H struct{}

func init() { harness.Register(H{}) }

func (H) Name() string { return "claude" }

type settings struct {
	Model        string              `json:"model"`
	OutputStyle  string              `json:"outputStyle"`
	TUI          string              `json:"tui"`
	SkipPrompt   bool                `json:"skipDangerousModePermissionPrompt"`
	AutoMemory   bool                `json:"autoMemoryEnabled"` // mnemopi is the one memory, shared with codex, omp and rig
	Permissions  map[string][]string `json:"permissions"`
	Attribution  attribution         `json:"attribution"` // empty strings drop the Co-Authored-By and PR footer
	Hooks        orderedHooks        `json:"hooks"`
	APIKeyHelper string              `json:"apiKeyHelper,omitempty"`
}

type attribution struct {
	Commit string `json:"commit"`
	PR     string `json:"pr"`
}

func (H) Sync(ctx *harness.Ctx) error {
	d := filepath.Join(ctx.State, "claude")
	m := ctx.Models
	hooks := orderedHooks{{"SessionStart", []HookGroup{{
		Matcher: "^(startup|resume|clear|compact|fork)$",
		Hooks:   []Hook{{Type: "command", Command: herdrHook, Timeout: 10}},
	}}}}
	mem := MemoryHooks(ctx, "claude")
	for _, e := range memoryEvents {
		if groups, ok := mem[e.name]; ok {
			hooks = hooks.add(e.name, groups)
		}
	}
	s := settings{
		Model:        Model(m),
		OutputStyle:  Style,
		TUI:          "fullscreen",
		SkipPrompt:   true,
		APIKeyHelper: m.KeyCmd,
		Permissions: map[string][]string{"allow": {
			"Bash(git push --force-with-lease *)", "Bash(git push --force-with-lease=*)"}},
		Hooks: hooks,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return err
	}
	if err := fsx.Write(filepath.Join(d, "settings.json"), buf.Bytes()); err != nil {
		return err
	}
	if err := fsx.Write(filepath.Join(d, "CLAUDE.md"), []byte(ctx.Layers.Instructions(m, "claude"))); err != nil {
		return err
	}
	if err := fsx.Write(filepath.Join(d, "output-styles", Style+".md"), []byte(ctx.Layers.Style())); err != nil {
		return err
	}
	return ctx.Layers.LinkSkills(filepath.Join(d, "skills"))
}

func (H) Env(ctx *harness.Ctx) error {
	ctx.Env["CLAUDE_CONFIG_DIR"] = filepath.Join(ctx.State, "claude")
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
		delete(ctx.Env, k)
	}
	if u := ctx.Models.BaseURL; u != "" {
		ctx.Env["ANTHROPIC_BASE_URL"] = u
	}
	return HerdrInstall(ctx, "claude", "claude/hooks/herdr-agent-state.sh")
}

func (H) Argv(ctx *harness.Ctx, m string, args []string) ([]string, error) {
	bin := ctx.Sys.FindBin("claude")
	if bin == "" {
		return nil, fmt.Errorf("harness: claude not found on PATH")
	}
	argv := []string{bin}
	if m != "" {
		argv = append(argv, "--model", ctx.Models.ResolveFlag("claude", m))
	}
	if !slices.Contains(args, "--dangerously-skip-permissions") {
		argv = append(argv, "--dangerously-skip-permissions")
	}
	return append(argv, args...), nil
}

// Model is the default model id, with "[1m]" for an anthropic model of 1M context or more.
func Model(m *model.Config) string {
	id := m.RoleModel("claude", "default")
	attrs := m.Attrs(id)
	if api, _ := attrs["api"].(string); api == "anthropic" && num(attrs["context"]) >= 1_000_000 {
		return id + "[1m]"
	}
	return id
}

type event struct {
	name   string
	groups []HookGroup
}

// orderedHooks keeps events in insertion order in settings.json, like the Python launcher.
type orderedHooks []event

func (o orderedHooks) add(name string, groups []HookGroup) orderedHooks {
	for i := range o {
		if o[i].name == name {
			o[i].groups = append(o[i].groups, groups...)
			return o
		}
	}
	return append(o, event{name, groups})
}

func (o orderedHooks) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(e.name)
		v, err := json.Marshal(e.groups)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Hook is one command hook, shared by claude settings.json and codex config.toml.
type Hook struct {
	Type    string `json:"type" toml:"type"`
	Command string `json:"command" toml:"command"`
	Timeout int    `json:"timeout" toml:"timeout"`
	Async   bool   `json:"async,omitempty" toml:"async,omitempty"`
}

// HookGroup is a matcher plus its hooks.
type HookGroup struct {
	Matcher string `json:"matcher,omitempty" toml:"matcher,omitempty"`
	Hooks   []Hook `json:"hooks" toml:"hooks"`
}

// memoryEvents maps hook event -> async. claude and codex reach omp's mnemopi bank through memory/hook.ts.
var memoryEvents = []struct {
	name  string
	async bool
}{{"SessionStart", true}, {"UserPromptSubmit", false}, {"Stop", false}} // sync: an async Stop hook dies with `claude -p` before the turn is retained

// MemoryHooks returns hook groups per event; empty when bun is missing (memory then stays omp-only).
func MemoryHooks(ctx *harness.Ctx, h string) map[string][]HookGroup {
	bun := ctx.Sys.FindBin("bun")
	if bun == "" || !executable(bun) {
		return map[string][]HookGroup{}
	}
	cmd := ShellQuote(bun) + " " + ShellQuote(filepath.Join(ctx.State, "memory", "hook.ts")) + " " + h
	out := map[string][]HookGroup{}
	for _, e := range memoryEvents {
		out[e.name] = []HookGroup{{Hooks: []Hook{{Type: "command", Command: cmd, Timeout: 30, Async: e.async}}}}
	}
	return out
}

// HerdrInstall installs herdr's agent-state hooks into the state dir instead of $HOME.
// file is the hook path herdr writes, relative to the state dir.
func HerdrInstall(ctx *harness.Ctx, h, file string) error {
	if ctx.Env["HERDR_ENV"] != "1" {
		return nil
	}
	herdr := ctx.Sys.Which("herdr")
	if herdr == "" {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(ctx.State, file)); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(ctx.State, h), 0o755); err != nil {
		return err
	}
	cmd := exec.Command(herdr, "integration", "install", h)
	cmd.Env = ctx.Env.Environ()
	_ = cmd.Run() // best effort, like the Python launcher
	return nil
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// ShellQuote matches Python's shlex.quote.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func num(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

func executable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}
