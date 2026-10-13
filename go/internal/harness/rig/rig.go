// Package rig runs the OpenRig CLI; its claude-code and codex seats read harness' claude and codex config.
package rig

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/harness/claude"
	"github.com/Quentin-M/harness/internal/harness/codex"
)

// H is the rig harness.
type H struct{}

func init() { harness.Register(H{}) }

func (H) Name() string { return "rig" }

func dir(state string) string { return filepath.Join(state, "rig") }

func bin(state string) string { return filepath.Join(dir(state), "npm/node_modules/.bin/rig") }

// Env exports CLAUDE_CONFIG_DIR and CODEX_HOME to the daemon, which selects them for its seats.
func (H) Env(ctx *harness.Ctx) error {
	if err := (claude.H{}).Env(ctx); err != nil {
		return err
	}
	if err := (codex.H{}).Env(ctx); err != nil {
		return err
	}
	ctx.Env["OPENRIG_HOME"] = dir(ctx.State)
	// the workspace specs root holds workflows/; the library root below holds agents/ and rigs/
	ctx.Env["OPENRIG_WORKSPACE_SPECS_ROOT"] = ctx.Root
	ctx.Env["OPENRIG_NO_KERNEL"] = "1" // the kernel auto-boots its own claude-code operator seats
	// harness sync rewrites config.toml on every launch, dropping the daemon's codex activity hooks block
	ctx.Env["OPENRIG_RUNTIME_CODEX_HOOKS_ENABLED"] = "false"
	// seats reach rig itself and `harness lease` (the launcher binary main.sh builds into state/bin)
	ctx.Env["PATH"] = filepath.Join(dir(ctx.State), "npm/node_modules/.bin") + string(os.PathListSeparator) +
		filepath.Join(ctx.State, "bin") + string(os.PathListSeparator) + ctx.Env["PATH"]
	return nil
}

func (H) Sync(ctx *harness.Ctx) error {
	if err := (claude.H{}).Sync(ctx); err != nil {
		return err
	}
	if err := (codex.H{}).Sync(ctx); err != nil {
		return err
	}
	if err := linkSpecLibrary(ctx); err != nil {
		return err
	}
	return syncCatalog(ctx)
}

func (H) Argv(ctx *harness.Ctx, _ string, args []string) ([]string, error) {
	if !ctx.DryRun {
		if err := ensureRig(ctx); err != nil {
			return nil, err
		}
	}
	return append([]string{bin(ctx.State)}, args...), nil
}

func ensureRig(ctx *harness.Ctx) error {
	if _, err := os.Stat(bin(ctx.State)); err == nil {
		return nil
	}
	npm := ctx.Sys.FindBin("npm")
	if npm == "" {
		return errors.New("harness: npm not found; --rig needs node >= 22")
	}
	cmd := exec.Command(npm, "install", "--prefix", filepath.Join(dir(ctx.State), "npm"), "--no-fund", "--no-audit", "@openrig/cli")
	cmd.Env, cmd.Stdout, cmd.Stderr = ctx.Env.Environ(), os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("harness: npm install @openrig/cli: %w", err)
	}
	return nil
}
