// Package assets embeds the launcher's data files so the binary is self-contained.
package assets

import _ "embed"

//go:embed models.yaml
var Models []byte

//go:embed assets/omp-base.yml
var OmpBase []byte

//go:embed assets/memory-hook.ts
var MemoryHook []byte
