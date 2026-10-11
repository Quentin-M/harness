// Package harness defines the contract every front-end (claude, omp, codex, opencode, rig) implements.
package harness

import (
	"sort"

	"github.com/Quentin-M/harness/internal/layer"
	"github.com/Quentin-M/harness/internal/model"
	"github.com/Quentin-M/harness/internal/sys"
)

// Ctx is everything a harness needs to render config and build its launch.
type Ctx struct {
	Root   string        // harness checkout, the base layer (HARNESS_ROOT)
	State  string        // $HARNESS_STATE
	Cwd    string        // where the user launched
	Layers *layer.Set    // resolved layers, lowest precedence first
	Models *model.Config // model catalog, roles and gateway
	Env    sys.Env       // launch env; harnesses mutate it in Env()
	Sys    *sys.Sys      // binaries and commands
	DryRun bool
}

// Harness renders its config into the state dir and describes how to exec it.
type Harness interface {
	Name() string
	// Sync writes generated config under ctx.State. Idempotent.
	Sync(ctx *Ctx) error
	// Env adjusts ctx.Env for launch (config dirs, gateway vars).
	Env(ctx *Ctx) error
	// Argv returns the full command line; model is a role or a model id, "" for the default.
	Argv(ctx *Ctx, model string, args []string) ([]string, error)
}

var registry = map[string]Harness{}

// Register adds a harness; call from an init() in each harness package.
func Register(h Harness) { registry[h.Name()] = h }

// Get returns a registered harness by name.
func Get(name string) (Harness, bool) {
	h, ok := registry[name]
	return h, ok
}

// Synced lists harnesses that `harness sync` regenerates (everything except launch-only ones).
func Synced() []string {
	var out []string
	for name := range registry {
		if name != "rig" && name != "opencode" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
