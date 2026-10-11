package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quentin-M/harness/internal/sys"
)

func write(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

// fixture: a HOME holding a legacy bank and fastembed cache, fake omp and bun.
func fixture(t *testing.T) (state string, s *sys.Sys, log string) {
	t.Helper()
	tmp := t.TempDir()
	state, home, bins := filepath.Join(tmp, "state"), filepath.Join(tmp, "home"), filepath.Join(tmp, "bin")
	log = filepath.Join(tmp, "bun.log")
	write(t, filepath.Join(home, ".omp/agent/memories/mnemopi/mnemopi.db"), "LEGACY", 0o644)
	write(t, filepath.Join(home, ".omp/cache/fastembed/fast-bge/model.onnx"), "ONNX", 0o644)
	write(t, filepath.Join(home, ".omp/cache/fastembed/other/x"), "x", 0o644)
	write(t, filepath.Join(bins, "omp"), "#!/bin/sh\necho 'omp/14.2.1 darwin'\n", 0o755)
	write(t, filepath.Join(bins, "bun"), "#!/bin/sh\necho \"$@\" >> "+log+"\n", 0o755)
	// no sqlite3 on PATH: the plain-copy path is exercised deterministically
	env := sys.Env{"PATH": bins, "HOME": home}
	return state, &sys.Sys{Env: env, Home: home}, log
}

func TestOmpVersion(t *testing.T) {
	_, s, _ := fixture(t)
	if v := OmpVersion(s); v != "14.2.1" {
		t.Errorf("version = %q", v)
	}
}

func TestEnsure(t *testing.T) {
	state, s, log := fixture(t)
	if err := Ensure(state, s, s.Env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "memory/hook.ts")); !strings.Contains(string(b), "harness memory hook") {
		t.Errorf("hook = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "memory/mnemopi.db")); string(b) != "LEGACY" {
		t.Errorf("db = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "omp/cache/fastembed/fast-bge/model.onnx")); string(b) != "ONNX" {
		t.Errorf("cache = %q", b)
	}
	if _, err := os.Stat(filepath.Join(state, "omp/cache/fastembed/other")); !os.IsNotExist(err) {
		t.Error("non fast-* cache copied")
	}
	b, _ := os.ReadFile(log)
	if want := "add --cwd " + filepath.Join(state, "memory") + " @oh-my-pi/pi-mnemopi@14.2.1\n"; string(b) != want {
		t.Errorf("bun = %q, want %q", b, want)
	}

	// an existing bank is never overwritten; a pinned library skips bun
	write(t, filepath.Join(state, "memory/mnemopi.db"), "MINE", 0o644)
	write(t, filepath.Join(state, "memory/node_modules/@oh-my-pi/pi-mnemopi/package.json"), `{"version":"14.2.1"}`, 0o644)
	os.Remove(log)
	if err := Ensure(state, s, s.Env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "memory/mnemopi.db")); string(b) != "MINE" {
		t.Errorf("db overwritten: %q", b)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("bun ran with the library pinned")
	}
	if Installed(state) != "14.2.1" {
		t.Errorf("installed = %q", Installed(state))
	}
}

func TestBackupWithSqlite(t *testing.T) {
	_, s, _ := fixture(t)
	sqlite := "/usr/bin/sqlite3"
	if _, err := os.Stat(sqlite); err != nil {
		t.Skip("no sqlite3")
	}
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src.db"), filepath.Join(tmp, "out", "dst.db")
	s.Env["PATH"] = "/usr/bin"
	if out, err := exec.Command(sqlite, src, "create table m(v); insert into m values('kept');").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if err := backup(s, src, dst); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(sqlite, dst, "select v from m").Output()
	if err != nil || strings.TrimSpace(string(out)) != "kept" {
		t.Errorf("backup = %q %v", out, err)
	}
}
