// Package memory keeps the one mnemopi bank per device at $HARNESS_STATE/memory.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	assets "github.com/Quentin-M/harness"
	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/sys"
)

var versionRe = regexp.MustCompile(`omp/(\d+\.\d+\.\d+)`)

// OmpVersion is the installed omp's version, "" when omp is missing or unparseable.
func OmpVersion(s *sys.Sys) string {
	bin := s.FindBin("omp")
	if bin == "" {
		return ""
	}
	out, _ := exec.Command(bin, "--version").Output()
	if m := versionRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

// Installed is the version of the mnemopi library under state, "" when absent.
func Installed(state string) string {
	data, err := os.ReadFile(filepath.Join(state, "memory/node_modules/@oh-my-pi/pi-mnemopi/package.json"))
	if err != nil {
		return ""
	}
	var pkg struct{ Version string }
	_ = json.Unmarshal(data, &pkg)
	return pkg.Version
}

// Ensure copies the hook next to the bank, seeds the bank and embedding cache once from a
// pre-harness ~/.omp, and pins the library to the installed omp's version so both share a schema.
func Ensure(state string, s *sys.Sys, env sys.Env) error {
	d := filepath.Join(state, "memory")
	if err := fsx.Write(filepath.Join(d, "hook.ts"), assets.MemoryHook); err != nil {
		return err
	}
	db, legacy := filepath.Join(d, "mnemopi.db"), filepath.Join(s.Home, ".omp/agent/memories/mnemopi/mnemopi.db")
	if !exists(db) && isFile(legacy) {
		if err := backup(s, legacy, db); err != nil {
			return err
		}
	}
	cache, legacyCache := filepath.Join(state, "omp/cache/fastembed"), filepath.Join(s.Home, ".omp/cache/fastembed")
	models, _ := filepath.Glob(filepath.Join(legacyCache, "fast-*"))
	for _, m := range models {
		if dst := filepath.Join(cache, filepath.Base(m)); isDir(m) && !isDir(dst) {
			if err := os.CopyFS(dst, os.DirFS(m)); err != nil {
				return err
			}
		}
	}
	have, bun, want := Installed(state), s.FindBin("bun"), OmpVersion(s)
	if bun == "" || want == "" || have == want {
		return nil
	}
	cmd := exec.Command(bun, "add", "--cwd", d, "@oh-my-pi/pi-mnemopi@"+want)
	cmd.Env = env.Environ()
	if out, err := cmd.CombinedOutput(); err != nil && have == "" {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 200 {
			msg = msg[len(msg)-200:]
		}
		fmt.Fprintf(os.Stderr, "harness: memory library @oh-my-pi/pi-mnemopi@%s not installed, keeping none: %s\n", want, msg)
	}
	return nil
}

// backup copies a live sqlite db consistently via the sqlite3 CLI, else as a plain file copy.
func backup(s *sys.Sys, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if bin := s.Which("sqlite3"); bin != "" {
		if exec.Command(bin, src, ".backup '"+strings.ReplaceAll(dst, "'", "''")+"'").Run() == nil {
			return nil
		}
		_ = os.Remove(dst)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return fsx.Write(dst, data)
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
