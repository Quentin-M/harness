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

func TestSyncMirrorsSpecLibraryAsRealFiles(t *testing.T) {
	ctx := fixture(t)
	write(t, filepath.Join(ctx.Root, "agents/lead/agent.yaml"), "name: lead\n")
	write(t, filepath.Join(ctx.Root, "rigs/gg/rig.yaml"), "name: gg\n")
	write(t, filepath.Join(ctx.Root, "skills/other/SKILL.md"), "not a spec\n")
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(ctx.State, "rig/specs")
	if fi, err := os.Lstat(lib); err != nil || !fi.IsDir() {
		t.Fatalf("rig/specs must be a real directory, got %v (%v)", fi, err)
	}
	for rel, want := range map[string]string{"agents/lead/agent.yaml": "name: lead\n", "rigs/gg/rig.yaml": "name: gg\n"} {
		fi, err := os.Lstat(filepath.Join(lib, rel))
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s must be a regular file, got %v (%v)", rel, fi, err)
		}
		if got := read(t, filepath.Join(lib, rel)); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(lib, "skills")); !os.IsNotExist(err) {
		t.Error("only agents/ and rigs/ belong in the spec library")
	}
}

func TestSyncReplacesLegacySpecLibrarySymlink(t *testing.T) {
	ctx := fixture(t)
	write(t, filepath.Join(ctx.Root, "agents/lead/agent.yaml"), "name: lead\n")
	lib := filepath.Join(ctx.State, "rig/specs")
	if err := os.MkdirAll(filepath.Dir(lib), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ctx.Root, lib); err != nil {
		t.Fatal(err)
	}
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(lib); err != nil || !fi.IsDir() {
		t.Fatalf("legacy symlink not replaced by a directory: %v (%v)", fi, err)
	}
	if got := read(t, filepath.Join(ctx.Root, "agents/lead/agent.yaml")); got != "name: lead\n" {
		t.Errorf("harness source was touched: %q", got)
	}
}

func TestSyncDropsStaleSpecsOnlyInsideLibrary(t *testing.T) {
	ctx := fixture(t)
	write(t, filepath.Join(ctx.Root, "agents/lead/agent.yaml"), "name: lead\n")
	lib := filepath.Join(ctx.State, "rig/specs")
	outside := filepath.Join(ctx.State, "outside.yaml")
	write(t, outside, "keep\n")
	write(t, filepath.Join(lib, "agents/gone/agent.yaml"), "stale\n")
	write(t, filepath.Join(lib, "agents/lead/agent.yaml"), "edited in the copy\n")
	if err := os.Symlink(outside, filepath.Join(lib, "agents/lead/link.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"agents/gone", "agents/lead/link.yaml"} {
		if _, err := os.Lstat(filepath.Join(lib, rel)); !os.IsNotExist(err) {
			t.Errorf("%s survived the mirror", rel)
		}
	}
	if got := read(t, filepath.Join(lib, "agents/lead/agent.yaml")); got != "name: lead\n" {
		t.Errorf("copy = %q, want the harness version", got)
	}
	if got := read(t, outside); got != "keep\n" {
		t.Error("a file outside rig/specs was touched")
	}
}

func TestSyncSpecLibraryIsIdempotent(t *testing.T) {
	ctx := fixture(t)
	write(t, filepath.Join(ctx.Root, "rigs/gg/rig.yaml"), "name: gg\n")
	f := filepath.Join(ctx.State, "rig/specs/rigs/gg/rig.yaml")
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := (H{}).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(f)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("unchanged spec was rewritten: %v -> %v (%v)", before.ModTime(), after.ModTime(), err)
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
