package opencode

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Quentin-M/harness/internal/harness"
	"github.com/Quentin-M/harness/internal/sys"
)

func ctxWith(t *testing.T, withBin bool) *harness.Ctx {
	tmp := t.TempDir()
	bins := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bins, 0o755); err != nil {
		t.Fatal(err)
	}
	if withBin {
		if err := os.WriteFile(filepath.Join(bins, "opencode"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := sys.Env{"PATH": bins, "HOME": filepath.Join(tmp, "home")}
	return &harness.Ctx{State: filepath.Join(tmp, "state"), Env: env, Sys: &sys.Sys{Env: env, Home: env["HOME"]}}
}

func TestVanilla(t *testing.T) {
	ctx := ctxWith(t, true)
	before := ctx.Env.Clone()
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := (H{}).Env(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Env) != len(before) {
		t.Errorf("env changed: %v", ctx.Env)
	}
	got, err := (H{}).Argv(ctx, "review", []string{"run", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[1:], []string{"run", "x"}) || filepath.Base(got[0]) != "opencode" {
		t.Errorf("argv = %v", got)
	}
	if _, err := os.Stat(filepath.Join(ctx.State, "opencode")); !os.IsNotExist(err) {
		t.Error("state written")
	}
}

func TestMissing(t *testing.T) {
	ctx := ctxWith(t, false)
	if b := ctx.Sys.FindBin("opencode"); b != "" {
		t.Skipf("opencode installed at %s", b)
	}
	if _, err := (H{}).Argv(ctx, "", nil); err == nil || err.Error() != "harness: opencode not found on PATH" {
		t.Errorf("err = %v", err)
	}
}
