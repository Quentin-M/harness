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
