package claude

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const bareID = "0b6f2c1e-4d3a-4e8b-9f1a-7c2d5e6f8a90"

// bareSession writes a bare ~/.claude session with a sidecar dir and file history.
func bareSession(t *testing.T, home, project, id string) string {
	t.Helper()
	bare := filepath.Join(home, ".claude")
	write(t, filepath.Join(bare, "projects", project, id+".jsonl"), `{"type":"user"}`+"\n", 0o600)
	write(t, filepath.Join(bare, "projects", project, id, "subagents", "agent-a1.jsonl"), "sub\n", 0o600)
	write(t, filepath.Join(bare, "projects", project, id, "tool-results", "r1.txt"), "big output\n", 0o600)
	write(t, filepath.Join(bare, "file-history", id, "abc@v1"), "v1\n", 0o600)
	return bare
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestArgvResumeImportsBareSession(t *testing.T) {
	for _, args := range [][]string{
		{"--resume", bareID},
		{"-r", bareID},
		{"--resume=" + bareID},
		{"--model", "x", "--resume", bareID, "-p", "hi"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ctx := fixture(t, gateway())
			bare := bareSession(t, ctx.Sys.Home, "-Users-q", bareID)
			got, err := (H{}).Argv(ctx, "", args)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got[len(got)-len(args):], args) {
				t.Errorf("argv = %v, want it to end with %v", got, args)
			}
			store := filepath.Join(ctx.State, "claude")
			for _, rel := range []string{
				"projects/-Users-q/" + bareID + ".jsonl",
				"projects/-Users-q/" + bareID + "/subagents/agent-a1.jsonl",
				"projects/-Users-q/" + bareID + "/tool-results/r1.txt",
				"file-history/" + bareID + "/abc@v1",
			} {
				if a, b := read(t, filepath.Join(bare, rel)), read(t, filepath.Join(store, rel)); a != b {
					t.Errorf("%s = %q, want %q", rel, b, a)
				}
			}
			// One file in both stores: turns appended after the resume show up in the bare store too.
			rel := filepath.Join("projects", "-Users-q", bareID+".jsonl")
			f, err := os.OpenFile(filepath.Join(store, rel), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(`{"type":"assistant"}` + "\n"); err != nil {
				t.Fatal(err)
			}
			f.Close()
			if got := read(t, filepath.Join(bare, rel)); !strings.Contains(got, "assistant") {
				t.Errorf("bare transcript = %q, want the appended turn", got)
			}
		})
	}
}

func TestArgvResumeLeavesOtherLaunchesAlone(t *testing.T) {
	cases := map[string]func(t *testing.T, home, state string) []string{
		"no resume": func(t *testing.T, home, _ string) []string {
			bareSession(t, home, "-Users-q", bareID)
			return []string{"-p", bareID}
		},
		"resume picker": func(t *testing.T, home, _ string) []string {
			bareSession(t, home, "-Users-q", bareID)
			return []string{"--resume"}
		},
		"not a session id": func(t *testing.T, home, _ string) []string {
			write(t, filepath.Join(home, ".claude", "projects", "-q", "x.jsonl"), "x\n", 0o600)
			return []string{"--resume", "../-q/x"}
		},
		"unknown id": func(t *testing.T, home, _ string) []string {
			bareSession(t, home, "-Users-q", bareID)
			return []string{"--resume", "ffffffff-4d3a-4e8b-9f1a-7c2d5e6f8a90"}
		},
		"ambiguous in bare store": func(t *testing.T, home, _ string) []string {
			bareSession(t, home, "-Users-q", bareID)
			bareSession(t, home, "-Users-q-w", bareID)
			return []string{"--resume", bareID}
		},
		"already in harness store": func(t *testing.T, home, state string) []string {
			bareSession(t, home, "-Users-q", bareID)
			write(t, filepath.Join(state, "claude", "projects", "-Users-q", bareID+".jsonl"), "ours\n", 0o600)
			return []string{"--resume", bareID}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := fixture(t, gateway())
			args := setup(t, ctx.Sys.Home, ctx.State)
			if _, err := (H{}).Argv(ctx, "", args); err != nil {
				t.Fatal(err)
			}
			m, _ := filepath.Glob(filepath.Join(ctx.State, "claude", "projects", "*", "*"))
			if name == "already in harness store" {
				if len(m) != 1 || read(t, m[0]) != "ours\n" {
					t.Errorf("harness store = %v, want only its own transcript untouched", m)
				}
				return
			}
			if len(m) != 0 {
				t.Errorf("harness store = %v, want nothing imported", m)
			}
		})
	}
}

func TestArgvResumeCopiesWhenLinkFails(t *testing.T) {
	ctx := fixture(t, gateway())
	bare := bareSession(t, ctx.Sys.Home, "-Users-q", bareID)
	old := link
	link = func(string, string) error { return &os.LinkError{Op: "link", Err: errors.New("cross-device link")} }
	t.Cleanup(func() { link = old })
	if _, err := (H{}).Argv(ctx, "", []string{"--resume", bareID}); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("projects", "-Users-q", bareID+".jsonl")
	if a, b := read(t, filepath.Join(bare, rel)), read(t, filepath.Join(ctx.State, "claude", rel)); a != b {
		t.Errorf("copied transcript = %q, want %q", b, a)
	}
	st, err := os.Stat(filepath.Join(ctx.State, "claude", rel))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("copied transcript mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestArgvResumeReportsImportFailure(t *testing.T) {
	ctx := fixture(t, gateway())
	bareSession(t, ctx.Sys.Home, "-Users-q", bareID)
	projects := filepath.Join(ctx.State, "claude", "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(projects, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(projects, 0o755) })
	_, err := (H{}).Argv(ctx, "", []string{"--resume", bareID})
	if err == nil || !strings.Contains(err.Error(), bareID) {
		t.Fatalf("err = %v, want an error naming the session", err)
	}
}
