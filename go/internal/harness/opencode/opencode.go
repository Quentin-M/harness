// Package opencode launches opencode vanilla: no generated config, no env changes.
package opencode

import (
	"errors"

	"github.com/Quentin-M/harness/internal/harness"
)

// H is the opencode harness.
type H struct{}

func init() { harness.Register(H{}) }

func (H) Name() string            { return "opencode" }
func (H) Sync(*harness.Ctx) error { return nil }
func (H) Env(*harness.Ctx) error  { return nil }

func (H) Argv(ctx *harness.Ctx, _ string, args []string) ([]string, error) {
	bin := ctx.Sys.FindBin("opencode")
	if bin == "" {
		return nil, errors.New("harness: opencode not found on PATH")
	}
	return append([]string{bin}, args...), nil
}
