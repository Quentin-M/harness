package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, cwd string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-C", cwd}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func tmp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// repo is a main checkout with a subdir and a linked worktree, named with dots and underscores.
func repo(t *testing.T) (base, main, wt string) {
	t.Helper()
	base = tmp(t)
	main, wt = filepath.Join(base, "my_repo.v2"), filepath.Join(base, "wt.feature_x")
	if err := os.MkdirAll(filepath.Join(main, "sub", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	git(t, main, "worktree", "add", "-q", wt, "-b", "feat")
	return base, main, wt
}

func TestRoot(t *testing.T) {
	base, main, wt := repo(t)
	plain := filepath.Join(base, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(wt, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ cwd, want string }{
		{main, main},
		{filepath.Join(main, "sub", "dir"), main},
		{wt, main},
		{link, main},
		{plain, plain},
	} {
		if got := Root(c.cwd); got != c.want {
			t.Errorf("Root(%s) = %q, want %q", c.cwd, got, c.want)
		}
	}
}

func TestRootForeignGitFile(t *testing.T) {
	base := tmp(t)
	d := filepath.Join(base, "fake")
	if err := os.MkdirAll(filepath.Join(base, "other", ".git", "worktrees", "fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "other", ".git", "worktrees", "fake")
	for f, s := range map[string]string{
		filepath.Join(d, ".git"):       "gitdir: " + wt + "\n",
		filepath.Join(wt, "commondir"): "../..\n",
		filepath.Join(wt, "gitdir"):    filepath.Join(base, "elsewhere", ".git") + "\n",
	} {
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := Root(d); got != d {
		t.Errorf("Root = %q, want %q: a worktree whose gitdir does not point back stays its own root", got, d)
	}
}

// Expected keys come from claude 2.1.284: short ones from a live `claude -p` run printing its auto
// memory dir, long ones from its key function run under node.
func TestKey(t *testing.T) {
	for _, c := range []struct{ root, want string }{
		{"/Users/jane.doe/Workspace/gg", "-Users-jane-doe-Workspace-gg"},
		{"/private/tmp/memprobe.IiYM/my_repo.v2", "-private-tmp-memprobe-IiYM-my-repo-v2"},
		{"/tmp/a😀b", "-tmp-a--b"},
		{"/Users/q/" + strings.Repeat("deep_dir.x/", 20), "-Users-q-" + strings.Repeat("deep-dir-x-", 17) + "deep-n08018"},
		{"/" + strings.Repeat("a", 219), "-" + strings.Repeat("a", 199) + "-y46xwy"},
		{"/tmp/café/" + strings.Repeat("é", 200), "-tmp-caf-" + strings.Repeat("-", 192) + "oeb72x"},
	} {
		if got := Key(c.root); got != c.want {
			t.Errorf("Key(%q) = %q, want %q", c.root, got, c.want)
		}
	}
}

func TestDir(t *testing.T) {
	_, main, wt := repo(t)
	want := filepath.Join("/st", "claude", "projects", Key(main), "memory")
	for _, cwd := range []string{main, wt, filepath.Join(main, "sub")} {
		if got := Dir("/st", cwd); got != want {
			t.Errorf("Dir(%s) = %q, want %q", cwd, got, want)
		}
	}
	if !strings.Contains(want, "my-repo-v2") {
		t.Errorf("Dir = %q, want the main checkout's key", want)
	}
}

func writeIndex(t *testing.T, dir, s string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIndex(t *testing.T) {
	d := tmp(t)
	if got, err := Index(d); err != nil || got != "" {
		t.Fatalf("missing index = %q, %v", got, err)
	}
	writeIndex(t, d, "\n- [A](a.md) - alpha\n- [B](b.md) - beta\n\n")
	if got, err := Index(d); err != nil || got != "- [A](a.md) - alpha\n- [B](b.md) - beta" {
		t.Errorf("small index = %q, %v", got, err)
	}

	var lines []string
	for i := range 250 {
		lines = append(lines, fmt.Sprintf("- [M%d](m%d.md) - hook", i, i))
	}
	writeIndex(t, d, strings.Join(lines, "\n"))
	got, err := Index(d)
	if err != nil {
		t.Fatal(err)
	}
	body, warn, _ := strings.Cut(got, "\n\n> WARNING: ")
	if n := len(strings.Split(body, "\n")); n != 200 || !strings.HasSuffix(body, "- [M199](m199.md) - hook") {
		t.Errorf("line cap kept %d lines, ends %q", n, body[len(body)-30:])
	}
	if !strings.HasPrefix(warn, "MEMORY.md is 250 lines (limit: 200)") {
		t.Errorf("line warning = %q", warn)
	}

	line := "- [X](x.md) - " + strings.Repeat("y", 186) // 200 bytes
	writeIndex(t, d, strings.TrimSuffix(strings.Repeat(line+"\n", 150), "\n"))
	got, _ = Index(d)
	body, warn, _ = strings.Cut(got, "\n\n> WARNING: ")
	if len(body) > indexBytes || len(body) < indexBytes-len(line) || !strings.HasSuffix(body, line) {
		t.Errorf("byte cap kept %d bytes", len(body))
	}
	if !strings.HasPrefix(warn, "MEMORY.md is 30149 bytes (limit: 25000)") {
		t.Errorf("byte warning = %q", warn)
	}
}

func TestPathCmd(t *testing.T) {
	_, main, wt := repo(t)
	var out bytes.Buffer
	if err := PathCmd("/st", nil, wt, &out); err != nil {
		t.Fatal(err)
	}
	if want := Dir("/st", main) + "\n"; out.String() != want {
		t.Errorf("path = %q, want %q", out.String(), want)
	}
	out.Reset()
	if err := PathCmd("/st", []string{"sub/dir"}, main, &out); err != nil || out.String() != Dir("/st", main)+"\n" {
		t.Errorf("path sub/dir = %q, %v", out.String(), err)
	}
	if err := PathCmd("/st", []string{"a", "b"}, main, &out); err == nil {
		t.Error("two args accepted")
	}
}

func TestContextHook(t *testing.T) {
	_, main, wt := repo(t)
	state := tmp(t)
	payload := func(cwd string) *strings.Reader {
		b, _ := json.Marshal(map[string]any{"cwd": cwd, "hook_event_name": "SessionStart", "source": "startup"})
		return strings.NewReader(string(b))
	}
	var out bytes.Buffer
	if err := ContextHook(state, payload(wt), &out); err != nil || out.Len() != 0 {
		t.Fatalf("empty memory printed %q, %v", out.String(), err)
	}

	dir := Dir(state, main)
	writeIndex(t, dir, "- [Prefers Go](prefers-go.md) - user writes Go by default <b>\n")
	if err := ContextHook(state, payload(wt), &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	ctx := got.HookSpecificOutput.AdditionalContext
	if got.HookSpecificOutput.HookEventName != "SessionStart" ||
		!strings.HasPrefix(ctx, "<memories>\n") || !strings.HasSuffix(ctx, "\n</memories>") ||
		!strings.Contains(ctx, "Memory directory: "+dir+"\n") ||
		!strings.Contains(ctx, "- [Prefers Go](prefers-go.md) - user writes Go by default <b>") {
		t.Errorf("output = %s", out.String())
	}

	for _, bad := range []string{"", "{", `{"cwd": 3}`, `{"cwd": "rel/dir"}`} {
		if err := ContextHook(state, strings.NewReader(bad), &out); err == nil {
			t.Errorf("payload %q accepted", bad)
		}
	}
}
