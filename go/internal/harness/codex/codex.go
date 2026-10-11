// Package codex renders CODEX_HOME and launches codex.
package codex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

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
	if oh, ok := old["hooks"].(map[string]any); ok {
		if st, ok := oh["state"].(map[string]any); ok && len(st) > 0 {
			hooks["state"] = st
		}
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
