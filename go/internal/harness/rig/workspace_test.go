package rig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncRendersProjectCatalog(t *testing.T) {
	ctx := fixture(t)
	write(t, filepath.Join(ctx.Root, "openrig.yaml"),
		"schema: openrig.workspace/v0alpha1\nprojects:\n  - id: default\n    root: .\n  - id: gg\n    root: ~/workspace/gg/.openrig\n    rigs: [gg]\n")
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	want := "schema: openrig.workspace/v0alpha1\nprojects:\n  - id: default\n    root: .\n  - id: gg\n    root: " +
		filepath.Join(ctx.Sys.Home, "workspace/gg/.openrig") + "\n    rigs:\n      - gg\n"
	if got := read(t, filepath.Join(ctx.State, "rig/workspace/workspace.yaml")); got != want {
		t.Errorf("catalog = %q, want %q", got, want)
	}
}

func TestSyncWithoutCatalogKeepsDaemonCatalog(t *testing.T) {
	ctx := fixture(t)
	live := filepath.Join(ctx.State, "rig/workspace/workspace.yaml")
	write(t, live, "projects: []\n")
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(t, live); got != "projects: []\n" {
		t.Errorf("catalog = %q, want it untouched", got)
	}
}

func TestSyncRejectsMalformedProject(t *testing.T) {
	ctx := fixture(t)
	write(t, filepath.Join(ctx.Root, "openrig.yaml"), "projects:\n  - gg\n")
	if err := (H{}).Sync(ctx); err == nil {
		t.Fatal("Sync accepted a project that is not a mapping")
	}
	if _, err := os.Stat(filepath.Join(ctx.State, "rig/workspace/workspace.yaml")); !os.IsNotExist(err) {
		t.Error("malformed catalog was written")
	}
}

func TestSyncLinksSpecLibraryToHarness(t *testing.T) {
	ctx := fixture(t)
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(ctx.State, "rig/specs")); got != ctx.Root {
		t.Errorf("rig/specs -> %q, want %q", got, ctx.Root)
	}
}

func TestEnvPointsWorkspaceSpecsAtHarness(t *testing.T) {
	ctx := fixture(t)
	if err := (H{}).Env(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ctx.Env["OPENRIG_WORKSPACE_SPECS_ROOT"]; got != ctx.Root {
		t.Errorf("OPENRIG_WORKSPACE_SPECS_ROOT = %q, want %q", got, ctx.Root)
	}
}

func TestSyncKeepsPopulatedSpecLibrary(t *testing.T) {
	ctx := fixture(t)
	lib := filepath.Join(ctx.State, "rig/specs")
	write(t, filepath.Join(lib, "mine/agent.yaml"), "name: mine\n")
	if err := (H{}).Sync(ctx); err == nil {
		t.Fatal("Sync replaced a spec library that holds specs")
	}
	if _, err := os.Stat(filepath.Join(lib, "mine/agent.yaml")); err != nil {
		t.Error("spec was deleted")
	}
}
