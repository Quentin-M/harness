package sys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exe(t *testing.T, path, body string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindBinSkipsHarnessShims(t *testing.T) {
	d := t.TempDir()
	shims, bins := filepath.Join(d, "shims"), filepath.Join(d, "bin")
	exe(t, filepath.Join(shims, "omp"), "#!/bin/sh\nexec harness --omp \"$@\"\n")
	exe(t, filepath.Join(shims, "claude"), "#!/usr/bin/env bash\nexec \"$HOME/x/harness\" \"$@\"\n")
	exe(t, filepath.Join(shims, "codex"), "#!/bin/sh\nexec 'harness' --codex\n")
	exe(t, filepath.Join(shims, "keep"), "#!/bin/sh\nexec harnessx\n")
	exe(t, filepath.Join(shims, "opencode"), "#!/bin/sh\nexec /w/q/tools/harness/go/cmd/claude/main.sh --opencode \"$@\"\n")
	os.WriteFile(filepath.Join(shims, "noexec"), []byte("#!/bin/sh\n"), 0o644)
	for _, n := range []string{"omp", "claude", "codex", "opencode", "noexec"} {
		exe(t, filepath.Join(bins, n), "#!/bin/sh\necho real\n")
	}
	s := &Sys{Env: Env{"PATH": shims + ":" + bins}, Home: filepath.Join(d, "home")}
	for _, n := range []string{"omp", "claude", "codex", "opencode", "noexec"} {
		if got := s.FindBin(n); got != filepath.Join(bins, n) {
			t.Errorf("%s: %s", n, got)
		}
	}
	if got := s.FindBin("keep"); got != filepath.Join(shims, "keep") {
		t.Errorf("keep: %s", got)
	}
	if got := s.FindBin("definitely-not-here-xyz"); got != "" {
		t.Errorf("missing: %s", got)
	}
}

func TestFindBinOverrideHomeDirsAndSelf(t *testing.T) {
	d := t.TempDir()
	home := filepath.Join(d, "home")
	want := exe(t, filepath.Join(home, ".local/bin/zzbin"), "#!/bin/sh\n")
	s := &Sys{Env: Env{"PATH": filepath.Join(d, "empty")}, Home: home}
	if got := s.FindBin("zzbin"); got != want {
		t.Fatal(got)
	}
	s.Env["HARNESS_ZZBIN_BIN"] = "/no/such"
	if got := s.FindBin("zzbin"); got != "/no/such" {
		t.Fatal(got)
	}
	delete(s.Env, "HARNESS_ZZBIN_BIN")
	self := exe(t, filepath.Join(d, "harness-real"), "\x7fELF")
	os.MkdirAll(filepath.Join(d, "p"), 0o755)
	os.Symlink(self, filepath.Join(d, "p/zzbin"))
	s.Env["PATH"] = filepath.Join(d, "p")
	s.Self = self
	if got := s.FindBin("zzbin"); got != want {
		t.Fatalf("symlink to harness not skipped: %s", got)
	}
}

func TestEnvHelpers(t *testing.T) {
	e := Env{"B": "2", "A": "1"}
	c := e.Clone()
	c.Set("C", "3")
	c.Unset("A")
	c.SetDefault("B", "x")
	if _, ok := e["C"]; ok || e["A"] != "1" {
		t.Fatal("clone shares storage")
	}
	if got := strings.Join(c.Environ(), ","); got != "B=2,C=3" {
		t.Fatal(got)
	}
}

func TestOutputAndShell(t *testing.T) {
	s := &Sys{Env: Env{"PATH": "/usr/bin:/bin", "X": "hi"}}
	out, err := s.Output("sh", "-c", "echo $X")
	if err != nil || out != "hi\n" {
		t.Fatal(out, err)
	}
	if got := s.Shell("echo ' key '; exit 3"); got != "key" {
		t.Fatalf("%q", got)
	}
}
