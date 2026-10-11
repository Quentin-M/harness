// Package codex renders CODEX_HOME and launches codex.
package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/harness/claude"
)

type H struct{}

func init() { harness.Register(H{}) }

func (H) Name() string { return "codex" }

type config struct {
	Model           string                    `toml:"model"`
	ReasoningEffort string                    `toml:"model_reasoning_effort"`
	ModelProvider   string                    `toml:"model_provider,omitempty"`
	ModelProviders  map[string]map[string]any `toml:"model_providers,omitempty"`
	Projects        any                       `toml:"projects,omitempty"`
	TUI             any                       `toml:"tui,omitempty"`
	Features        map[string]any            `toml:"features"`
	Hooks           map[string]any            `toml:"hooks,omitempty"`
}

func (H) Sync(ctx *harness.Ctx) error {
	d := filepath.Join(ctx.State, "codex")
	m := ctx.Models
	old := map[string]any{}
	if b, err := os.ReadFile(filepath.Join(d, "config.toml")); err == nil {
		if err := toml.Unmarshal(b, &old); err != nil {
			return fmt.Errorf("codex/config.toml: %w", err)
		}
	}
	cfg := config{Model: m.Resolve("codex", "default"), ReasoningEffort: "high"}
	if u := m.OpenAIURL(); u != "" {
		pid := m.Provider()
		prov := map[string]any{"name": pid, "base_url": u, "wire_api": "responses"}
		if kc := m.KeyCmd; kc != "" {
			prov["auth"] = map[string]any{"command": kc, "refresh_interval_ms": 86400000}
		}
		cfg.ModelProvider = pid
		cfg.ModelProviders = map[string]map[string]any{pid: prov}
	}
	// codex and herdr write project trust and hook trust here; keep them across regenerations
	cfg.Projects, cfg.TUI = old["projects"], old["tui"]
	cfg.Features, _ = old["features"].(map[string]any)
	if cfg.Features == nil {
		cfg.Features = map[string]any{}
	}
	cfg.Features["hooks"] = true
	hooks := map[string]any{}
	for e, g := range claude.MemoryHooks(ctx, "codex") {
		hooks[e] = g
	}
	st := map[string]any{}
	if oh, ok := old["hooks"].(map[string]any); ok {
		if o, ok := oh["state"].(map[string]any); ok {
			st = o
		}
	}
	// harness installs these hooks itself; recording their trust keeps fresh codex seats off codex's
	// startup hook-review prompt, which blocks an unattended seat
	owned, err := ownedHooks(d, hooks)
	if err != nil {
		return err
	}
	for k, h := range owned {
		s, _ := st[k].(map[string]any)
		if s == nil {
			s = map[string]any{}
		}
		s["trusted_hash"] = h
		st[k] = s
	}
	if len(st) > 0 {
		hooks["state"] = st
	}
	cfg.Hooks = hooks
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	if err := fsx.Write(filepath.Join(d, "config.toml"), buf.Bytes()); err != nil {
		return err
	}
	if err := fsx.Write(filepath.Join(d, "AGENTS.md"), []byte(ctx.Layers.Instructions(m, "codex"))); err != nil {
		return err
	}
	return ctx.Layers.LinkSkills(filepath.Join(d, "skills"))
}

func (H) Env(ctx *harness.Ctx) error {
	ctx.Env["CODEX_HOME"] = filepath.Join(ctx.State, "codex")
	return claude.HerdrInstall(ctx, "codex", "codex/herdr-agent-state.sh")
}

func (H) Argv(ctx *harness.Ctx, m string, args []string) ([]string, error) {
	bin := ctx.Sys.FindBin("codex")
	if bin == "" {
		return nil, fmt.Errorf("harness: codex not found on PATH")
	}
	argv := []string{bin}
	if m != "" {
		argv = append(argv, "--model", ctx.Models.ResolveFlag("codex", m))
	}
	return append(argv, args...), nil
}

var eventKeys = map[string]string{
	"PreToolUse": "pre_tool_use", "PermissionRequest": "permission_request", "PostToolUse": "post_tool_use",
	"PreCompact": "pre_compact", "PostCompact": "post_compact", "SessionStart": "session_start",
	"SessionEnd": "session_end", "UserPromptSubmit": "user_prompt_submit", "SubagentStart": "subagent_start",
	"SubagentStop": "subagent_stop", "Stop": "stop", "Interrupt": "interrupt",
}

// ownedHooks maps codex hook-state keys to trust hashes for the memory hooks in config.toml and the
// herdr hooks in hooks.json that run harness' own herdr script. Any other hook stays for a human to review.
func ownedHooks(d string, memory map[string]any) (map[string]string, error) {
	out := map[string]string{}
	add := func(src, event string, gi, hi int, h claude.Hook) {
		if ev, ok := eventKeys[event]; ok {
			out[fmt.Sprintf("%s:%s:%d:%d", filepath.Join(d, src), ev, gi, hi)] = hookHash(ev, h)
		}
	}
	for event, g := range memory {
		for gi, grp := range g.([]claude.HookGroup) {
			for hi, h := range grp.Hooks {
				if grp.Matcher == "" {
					add("config.toml", event, gi, hi, h)
				}
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(d, "hooks.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	} else if err != nil {
		return nil, err
	}
	var hf struct {
		Hooks map[string][]claude.HookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(b, &hf); err != nil {
		return nil, fmt.Errorf("codex/hooks.json: %w", err)
	}
	herdr := "bash '" + filepath.Join(d, "herdr-agent-state.sh") + "' "
	for event, groups := range hf.Hooks {
		for gi, grp := range groups {
			for hi, h := range grp.Hooks {
				if grp.Matcher == "" && h.Type == "command" && strings.HasPrefix(h.Command, herdr) {
					add("hooks.json", event, gi, hi, h)
				}
			}
		}
	}
	return out, nil
}

// hookHash reproduces codex's trust identity (hooks discovery.rs hook_hash): sha256 of key-sorted JSON
// for the event label plus the normalized handler, timeouts clamped as codex clamps them.
func hookHash(event string, h claude.Hook) string {
	t := h.Timeout
	switch {
	case event == "interrupt" || event == "session_end":
		t = min(max(t, 1), 3)
		if h.Timeout == 0 {
			t = 1
		}
	case t == 0:
		t = 600
	}
	id := map[string]any{"event_name": event, "hooks": []map[string]any{
		{"type": h.Type, "command": h.Command, "timeout": t, "async": h.Async},
	}}
	b, _ := json.Marshal(id) // map keys marshal sorted
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
