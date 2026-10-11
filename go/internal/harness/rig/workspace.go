package rig

import (
	"fmt"
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
