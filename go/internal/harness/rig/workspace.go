package rig

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/harness"
)

// syncCatalog renders openrig.yaml over the daemon's default project catalog.
// OpenRig resolves a project root only against the catalog's dir, so `~/` roots are expanded here.
func syncCatalog(ctx *harness.Ctx) error {
	src := filepath.Join(ctx.Root, "openrig.yaml")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	spec, err := fsx.ReadYAML(src)
	if err != nil {
		return err
	}
	projects, _ := spec["projects"].([]any)
	for _, p := range projects {
		pm, ok := p.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: project %v is not a mapping", src, p)
		}
		if root, _ := pm["root"].(string); strings.HasPrefix(root, "~/") {
			pm["root"] = filepath.Join(ctx.Sys.Home, root[2:])
		}
	}
	return fsx.Write(filepath.Join(dir(ctx.State), "workspace/workspace.yaml"), fsx.DumpYAML(spec))
}

// specLibraryDirs are the harness directories the daemon lists as user specs.
var specLibraryDirs = []string{"agents", "rigs"}

// linkSpecLibrary mirrors the harness agents/ and rigs/ into rig/specs, one way, on every launch.
// The daemon's spec scan and instance check see real files only (Dirent.isDirectory() is false for
// symlinks, and a symlinked specs/ fails the init lstat), so a link cannot stand in for the copy.
// Edit specs in the harness: rig/specs is overwritten and its stale files are removed.
func linkSpecLibrary(ctx *harness.Ctx) error {
	lib := filepath.Join(dir(ctx.State), "specs")
	if fi, err := os.Lstat(lib); err == nil && !fi.IsDir() {
		if err := os.Remove(lib); err != nil { // a legacy symlink or file: removes the link, never its target
			return err
		}
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, d := range specLibraryDirs {
		src := filepath.Join(ctx.Root, d)
		if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
			continue
		}
		err := filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(ctx.Root, path)
			if e.IsDir() {
				want[rel] = true
				return os.MkdirAll(filepath.Join(lib, rel), 0o755)
			}
			if !e.Type().IsRegular() {
				return nil
			}
			want[rel] = true
			return mirrorFile(path, filepath.Join(lib, rel))
		})
		if err != nil {
			return err
		}
	}
	return pruneSpecLibrary(lib, want)
}

// mirrorFile copies src over dst unless dst is already a regular file with the same bytes.
func mirrorFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode().IsRegular() {
			if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, data) {
				return nil
			}
		} else if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	return fsx.Write(dst, data)
}

// pruneSpecLibrary removes everything under lib that the mirror did not produce. It lists with Lstat
// semantics, so a symlink is unlinked, never followed.
func pruneSpecLibrary(lib string, want map[string]bool) error {
	return filepath.WalkDir(lib, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(lib, path)
		if rel == "." || want[rel] && (e.IsDir() || e.Type().IsRegular()) {
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if e.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}
