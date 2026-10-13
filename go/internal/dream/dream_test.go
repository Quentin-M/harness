package dream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Quentin-M/harness/internal/lease"
	"github.com/Quentin-M/harness/internal/memory"
	"github.com/Quentin-M/harness/internal/sys"
	"github.com/Quentin-M/harness/internal/transcript"
)

// fakeClaude records each call under $FAKE_LOG/<n>/ and writes a memory file into its cwd.
// FAKE_FAIL fails a call whose cwd contains it; FAKE_HANG makes every call hang.
const fakeClaude = `#!/bin/sh
d="$FAKE_LOG/$$"
mkdir -p "$d"
[ -n "$FAKE_HANG" ] && { sleep 30 & echo $! > "$d/child"; wait; }
printf '%s\n' "$@" > "$d/argv"
env > "$d/env"
pwd -P > "$d/cwd"
cat > "$d/stdin"
if [ -n "$FAKE_FAIL" ]; then
  case "$(pwd -P)" in *"$FAKE_FAIL"*) echo boom >&2; exit 1;; esac
fi
printf -- '---\nname: dreamt\n---\nfact\n' > dreamt.md
: > emptied.md
echo "added    dreamt: fact"
`

var now = time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte(text), 0o644))
}

func tmp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	return d
}

func git(t *testing.T, cwd string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-C", cwd}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

type world struct {
	t                     *testing.T
	base, state, log      string
	main, wt, plain, gone string
	runner                *Runner
	logBuf, out           *bytes.Buffer
}

// newWorld is a repo with a linked worktree, a plain dir, a deleted dir and a state dir.
func newWorld(t *testing.T) *world {
	base := tmp(t)
	w := &world{t: t, base: base, state: filepath.Join(base, "state"), log: filepath.Join(base, "calls"),
		main: filepath.Join(base, "repo"), wt: filepath.Join(base, "repo-wt"), plain: filepath.Join(base, "plain"),
		gone: filepath.Join(base, "gone"), logBuf: &bytes.Buffer{}, out: &bytes.Buffer{}}
	must(t, os.MkdirAll(w.main, 0o755))
	must(t, os.MkdirAll(w.plain, 0o755))
	git(t, w.main, "init", "-q")
	git(t, w.main, "commit", "-q", "--allow-empty", "-m", "x")
	git(t, w.main, "worktree", "add", "-q", w.wt, "-b", "feat")
	write(t, filepath.Join(w.main, "AGENTS.md"), "PROJECT-RULES\n")
	bin := filepath.Join(base, "claude")
	write(t, bin, fakeClaude)
	must(t, os.Chmod(bin, 0o755))
	w.runner = &Runner{State: w.state, Claude: bin, Self: "/opt/it's/harness", Model: "fast-model",
		Env:     sys.Env{"PATH": "/usr/bin:/bin", "FAKE_LOG": w.log, "CLAUDECODE": "1", "CLAUDE_CODE_ENTRYPOINT": "cli", "CLAUDE_CONFIG_DIR": filepath.Join(w.state, "claude")},
		Lease:   &lease.Store{Dir: filepath.Join(w.state, "rig", "leases"), TTL: time.Hour, Now: func() time.Time { return now }},
		Timeout: 10 * time.Second, Now: func() time.Time { return now }, Log: w.logBuf, Out: w.out}
	return w
}

var sessionN int

// claudeSession writes a Claude transcript in cwd with one user turn per text, at minutes before now.
func (w *world) claudeSession(cwd string, ago time.Duration, texts ...string) {
	w.t.Helper()
	sessionN++
	var b strings.Builder
	for i, text := range texts {
		rec := map[string]any{"type": "user", "sessionId": fmt.Sprintf("s%d", sessionN), "cwd": cwd,
			"timestamp": now.Add(-ago + time.Duration(i)*time.Second).Format(time.RFC3339Nano),
			"message":   map[string]any{"role": "user", "content": text}}
		line, _ := json.Marshal(rec)
		b.Write(line)
		b.WriteByte('\n')
	}
	path := filepath.Join(w.state, "claude", "projects", memory.Key(cwd), fmt.Sprintf("s%d.jsonl", sessionN))
	write(w.t, path, b.String())
	must(w.t, os.Chtimes(path, now.Add(-ago+time.Minute), now.Add(-ago+time.Minute)))
}

type call struct {
	argv                 []string
	dir, env, cwd, stdin string
}

func (w *world) calls() []call {
	w.t.Helper()
	ents, _ := os.ReadDir(w.log)
	var out []call
	for _, e := range ents {
		d := filepath.Join(w.log, e.Name())
		read := func(f string) string { b, _ := os.ReadFile(filepath.Join(d, f)); return string(b) }
		out = append(out, call{strings.Split(strings.TrimSpace(read("argv")), "\n"), d, read("env"),
			strings.TrimSpace(read("cwd")), read("stdin")})
	}
	return out
}

func (w *world) callFor(dir string) (call, bool) {
	for _, c := range w.calls() {
		if c.cwd == dir {
			return c, true
		}
	}
	return call{}, false
}

func (w *world) reset() { must(w.t, os.RemoveAll(w.log)) }

func flagValue(argv []string, name string) string {
	if i := slices.Index(argv, name); i >= 0 && i+1 < len(argv) {
		return argv[i+1]
	}
	return ""
}

func TestRunDreamsEachProjectOnce(t *testing.T) {
	w := newWorld(t)
	w.claudeSession(w.main, 3*time.Hour, "MAIN-ASK keep diffs minimal")
	w.claudeSession(w.wt, 2*time.Hour, "WT-ASK never run tests on the laptop")
	w.claudeSession(w.plain, time.Hour, "PLAIN-ASK use fixed-point money")
	w.claudeSession(filepath.Join(w.state, "claude"), time.Hour, "STATE-ASK")
	must(t, os.MkdirAll(w.gone, 0o755))
	w.claudeSession(w.gone, time.Hour, "GONE-ASK")
	must(t, os.RemoveAll(w.gone))

	must(t, w.runner.Run(Options{}))

	calls := w.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2 (repo with its worktree, plain): %+v", len(calls), calls)
	}
	repoDir, plainDir := memory.Dir(w.state, w.main), memory.Dir(w.state, w.plain)
	c, ok := w.callFor(repoDir)
	if !ok {
		t.Fatalf("no call in %s", repoDir)
	}
	for _, s := range []string{"MAIN-ASK", "WT-ASK", "PROJECT-RULES", "Root: " + w.main, "<transcripts>"} {
		if !strings.Contains(c.stdin, s) {
			t.Errorf("repo prompt lacks %q", s)
		}
	}
	for _, s := range []string{"PLAIN-ASK", "GONE-ASK", "STATE-ASK"} {
		if strings.Contains(c.stdin, s) {
			t.Errorf("repo prompt holds %q", s)
		}
	}
	if strings.Contains(strings.Join(c.argv, " "), "ASK") {
		t.Errorf("transcript text in argv: %v", c.argv)
	}
	for flag, want := range map[string]string{"--model": "fast-model", "--tools": Tools, "--add-dir": repoDir,
		"--permission-mode": "acceptEdits"} {
		if got := flagValue(c.argv, flag); got != want {
			t.Errorf("argv %s = %q, want %q", flag, got, want)
		}
	}
	var settings struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string
				Hooks   []struct{ Command string }
			}
		}
	}
	must(t, json.Unmarshal([]byte(flagValue(c.argv, "--settings")), &settings))
	if pre := settings.Hooks.PreToolUse; len(pre) != 1 || pre[0].Matcher != ".*" || len(pre[0].Hooks) != 1 ||
		pre[0].Hooks[0].Command != `'/opt/it'\''s/harness' dream guard '`+repoDir+`'` {
		t.Errorf("guard hook = %+v", settings.Hooks.PreToolUse)
	}
	for _, f := range []string{"-p", "--strict-mcp-config", "--no-session-persistence"} {
		if !slices.Contains(c.argv, f) {
			t.Errorf("argv lacks %s: %v", f, c.argv)
		}
	}
	env := "\n" + c.env
	for _, s := range []string{"\nHARNESS_DREAM=1\n", "\nCLAUDE_CONFIG_DIR=" + filepath.Join(w.state, "claude") + "\n"} {
		if !strings.Contains(env, s) {
			t.Errorf("env lacks %q", strings.TrimSpace(s))
		}
	}
	for _, k := range []string{"CLAUDECODE=", "CLAUDE_CODE_ENTRYPOINT="} {
		if strings.Contains(env, "\n"+k) {
			t.Errorf("env keeps %s", k)
		}
	}
	if pc, ok := w.callFor(plainDir); !ok || !strings.Contains(pc.stdin, "PLAIN-ASK") {
		t.Errorf("plain project not dreamt with its session: %v", ok)
	}

	if _, err := os.Stat(filepath.Join(repoDir, "dreamt.md")); err != nil {
		t.Errorf("memory file the dream wrote is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "emptied.md")); err == nil {
		t.Error("emptied memory file not pruned")
	}
	report, err := os.ReadFile(reportPath(w.state, memory.Key(w.main), now))
	must(t, err)
	if !strings.Contains(string(report), "added    dreamt: fact") || !strings.Contains(string(report), w.main) {
		t.Errorf("report = %q", report)
	}
	for _, p := range []string{lastPath(w.state), stampPath(w.state, memory.Key(w.main)), stampPath(w.state, memory.Key(w.plain))} {
		if got, err := readStamp(p); err != nil || !got.Equal(now) {
			t.Errorf("stamp %s = %v, %v; want %v", p, got, err, now)
		}
	}

	w.reset()
	later := now.Add(Stale + time.Hour)
	w.runner.Now = func() time.Time { return later }
	must(t, w.runner.Run(Options{}))
	if n := len(w.calls()); n != 0 {
		t.Errorf("second run with no new turns made %d calls", n)
	}
}

func TestFailedProjectIsRetriedAlone(t *testing.T) {
	w := newWorld(t)
	w.claudeSession(w.main, 2*time.Hour, "MAIN-ASK")
	w.claudeSession(w.plain, time.Hour, "PLAIN-ASK")
	w.runner.Env["FAKE_FAIL"] = memory.Key(w.plain)

	err := w.runner.Run(Options{})
	if err == nil || !strings.Contains(err.Error(), w.plain) {
		t.Fatalf("run = %v, want the plain project's failure", err)
	}
	if n := len(w.calls()); n != 2 {
		t.Errorf("calls = %d, want both projects tried", n)
	}
	if _, err := os.Stat(lastPath(w.state)); err == nil {
		t.Error("last advanced after a failure")
	}
	if _, err := os.Stat(stampPath(w.state, memory.Key(w.plain))); err == nil {
		t.Error("failed project stamped")
	}
	if !strings.Contains(w.logBuf.String(), "boom") {
		t.Errorf("claude stderr not in the log: %q", w.logBuf.String())
	}

	w.reset()
	delete(w.runner.Env, "FAKE_FAIL")
	later := now.Add(time.Hour)
	w.runner.Now = func() time.Time { return later }
	must(t, w.runner.Run(Options{}))
	calls := w.calls()
	if len(calls) != 1 || calls[0].cwd != memory.Dir(w.state, w.plain) {
		t.Fatalf("retry calls = %+v, want only the failed project", calls)
	}
	if got, _ := readStamp(lastPath(w.state)); !got.Equal(later) {
		t.Errorf("last = %v after a clean retry, want %v", got, later)
	}
}

func TestHungClaudeIsKilled(t *testing.T) {
	w := newWorld(t)
	w.claudeSession(w.plain, time.Hour, "PLAIN-ASK")
	w.runner.Env["FAKE_HANG"] = "1"
	w.runner.Timeout = 2 * time.Second
	start := time.Now()
	if err := w.runner.Run(Options{}); err == nil {
		t.Fatal("run succeeded with a hung claude")
	}
	if !strings.Contains(w.logBuf.String(), "claude timed out after 2s") {
		t.Errorf("log = %q, want the timeout", w.logBuf.String())
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("timeout took %s", d)
	}
	pid, err := os.ReadFile(filepath.Join(w.calls()[0].dir, "child"))
	must(t, err)
	if out, _ := exec.Command("ps", "-o", "stat=", "-p", strings.TrimSpace(string(pid))).Output(); len(bytes.TrimSpace(out)) > 0 && out[0] != 'Z' {
		t.Errorf("claude's child %s outlived the timeout", pid)
	}
	if _, err := os.Stat(lastPath(w.state)); err == nil {
		t.Error("last advanced after a timeout")
	}
}

func TestHeldLeaseExitsQuietly(t *testing.T) {
	w := newWorld(t)
	w.claudeSession(w.plain, time.Hour, "PLAIN-ASK")
	ok, err := w.runner.Lease.Acquire(resource, 1, "other")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	must(t, w.runner.Run(Options{}))
	if n := len(w.calls()); n != 0 {
		t.Errorf("calls = %d under a held lease", n)
	}
	if _, err := os.Stat(lastPath(w.state)); err == nil {
		t.Error("last advanced under a held lease")
	}
}

func TestNoTranscriptsIsANoOp(t *testing.T) {
	w := newWorld(t)
	must(t, w.runner.Run(Options{}))
	if n := len(w.calls()); n != 0 {
		t.Errorf("calls = %d", n)
	}
	if got, err := readStamp(lastPath(w.state)); err != nil || !got.Equal(now) {
		t.Errorf("last = %v, %v", got, err)
	}
}

func TestProjectAndDryRun(t *testing.T) {
	w := newWorld(t)
	w.claudeSession(w.main, 2*time.Hour, "MAIN-ASK")
	w.claudeSession(w.plain, time.Hour, "PLAIN-ASK")
	must(t, w.runner.Run(Options{Project: w.wt, DryRun: true}))
	if n := len(w.calls()); n != 0 {
		t.Errorf("dry run made %d calls", n)
	}
	if out := w.out.String(); !strings.Contains(out, w.main) || strings.Contains(out, w.plain) {
		t.Errorf("dry run listed %q, want only %s", out, w.main)
	}

	must(t, w.runner.Run(Options{Project: w.wt}))
	calls := w.calls()
	if len(calls) != 1 || calls[0].cwd != memory.Dir(w.state, w.main) {
		t.Fatalf("--project calls = %+v", calls)
	}
	if _, err := os.Stat(lastPath(w.state)); err == nil {
		t.Error("--project advanced last")
	}
	if _, err := os.Stat(stampPath(w.state, memory.Key(w.main))); err == nil {
		t.Error("--project stamped the project")
	}
}

func TestDigestsDedupeAndBudget(t *testing.T) {
	at := func(h int) time.Time { return now.Add(time.Duration(h) * time.Hour) }
	turn := func(h int, text string) transcript.Turn {
		return transcript.Turn{Role: "assistant", Text: text, At: at(h)}
	}
	ss := []transcript.Session{
		{ID: "old", Start: at(0), End: at(0), Turns: []transcript.Turn{turn(0, "Parked: peer queue empty"), turn(0, "OLD-ONLY")}},
		{ID: "new", Start: at(1), End: at(1), Turns: []transcript.Turn{turn(1, "Parked: peer queue empty"), turn(1, "NEW-ONLY")}},
		{ID: "dup", Start: at(2), End: at(2), Turns: []transcript.Turn{turn(2, "NEW-ONLY")}},
	}
	got := digests(ss, digestBudget)
	if n := strings.Count(got, "Parked: peer queue empty"); n != 1 {
		t.Errorf("repeated turn kept %d times", n)
	}
	if !strings.Contains(got, "OLD-ONLY") || !strings.Contains(got, "NEW-ONLY") {
		t.Errorf("distinct turns lost: %q", got)
	}
	// each repeated turn stays in its newest session; sessions keep oldest-first order
	inOrder := []string{" old ", "OLD-ONLY", " new ", "Parked", " dup ", "NEW-ONLY"}
	for i := 1; i < len(inOrder); i++ {
		if a, b := strings.Index(got, inOrder[i-1]), strings.Index(got, inOrder[i]); a < 0 || b < a {
			t.Errorf("%q not before %q in %q", inOrder[i-1], inOrder[i], got)
		}
	}

	var many []transcript.Session
	for i := range 200 {
		many = append(many, transcript.Session{ID: fmt.Sprintf("s%03d", i), Start: at(i), End: at(i),
			Turns: []transcript.Turn{turn(i, fmt.Sprintf("S%03d ", i)+strings.Repeat("x", 3000))}})
	}
	got = digests(many, 100_000)
	if len(got) > 100_000 || len(got) < 90_000 {
		t.Errorf("digest size %d, want close to the 100000 budget", len(got))
	}
	if !strings.Contains(got, "S199 ") || strings.Contains(got, "S000 ") {
		t.Error("over budget, want the newest sessions kept and the oldest dropped")
	}
}

func TestSpawnGate(t *testing.T) {
	state := tmp(t)
	marker := filepath.Join(state, "spawned")
	exe := filepath.Join(state, "fake-harness")
	write(t, exe, "#!/bin/sh\necho \"$@\" >> "+marker+"\n")
	must(t, os.Chmod(exe, 0o755))
	clock := now
	gate := &lease.Store{Dir: filepath.Join(state, "leases"), TTL: SpawnGap, Now: func() time.Time { return clock }}
	count := func(want int) int {
		n := 0
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			b, _ := os.ReadFile(marker)
			if n = strings.Count(string(b), "dream"); n > want {
				return n
			}
		}
		return n
	}

	must(t, writeStamp(lastPath(state), now.Add(-time.Hour)))
	must(t, Spawn(state, exe, nil, gate))
	if n := count(0); n != 0 {
		t.Errorf("fresh stamp spawned %d", n)
	}
	must(t, writeStamp(lastPath(state), now.Add(-Stale-time.Minute)))
	for range 5 {
		must(t, Spawn(state, exe, nil, gate))
	}
	if n := count(1); n != 1 {
		t.Errorf("stale stamp spawned %d dreams, want 1", n)
	}
	clock = clock.Add(SpawnGap + time.Minute)
	must(t, Spawn(state, exe, nil, gate))
	if n := count(2); n != 2 {
		t.Errorf("after the gap spawned %d total, want 2", n)
	}
}

func TestGuardKeepsToolsInsideTheMemoryDir(t *testing.T) {
	dir := "/st/claude/projects/k/memory"
	for _, c := range []struct {
		payload string
		allow   bool
	}{
		{`{"tool_name":"Write","tool_input":{"file_path":"/st/claude/projects/k/memory/a.md","content":"x"}}`, true},
		{`{"tool_name":"Edit","tool_input":{"file_path":"a.md"}}`, true},
		{`{"tool_name":"Read","tool_input":{"file_path":"/st/claude/projects/k/memory"}}`, true},
		{`{"tool_name":"Glob","tool_input":{"pattern":"**/*.md"}}`, true},
		{`{"tool_name":"Grep","tool_input":{"pattern":"/etc/passwd","path":"/st/claude/projects/k/memory"}}`, true},
		{`{"tool_name":"Grep","tool_input":{"pattern":"x"}}`, true},
		{`{"tool_name":"Write","tool_input":{"file_path":"/st/claude/projects/k/MEMORY.md"}}`, false},
		{`{"tool_name":"Write","tool_input":{"file_path":"/st/claude/projects/k/memory/../../k2/memory/a.md"}}`, false},
		{`{"tool_name":"Edit","tool_input":{"file_path":"../a.md"}}`, false},
		{`{"tool_name":"Write","tool_input":{"file_path":"/st/claude/projects/k/memory-evil/a.md"}}`, false},
		{`{"tool_name":"Read","tool_input":{"file_path":"/etc/hosts"}}`, false},
		{`{"tool_name":"Glob","tool_input":{"pattern":"/Users/**/.env"}}`, false},
		{`{"tool_name":"Glob","tool_input":{"pattern":"../../**"}}`, false},
		{`{"tool_name":"Grep","tool_input":{"pattern":"x","path":"/"}}`, false},
		{`{"tool_name":"Read","tool_input":{"file_path":7}}`, false},
		{`{"tool_name":"Bash","tool_input":{"command":"ls"}}`, false},
		{`not json`, false},
	} {
		err := Guard(dir, strings.NewReader(c.payload))
		if (err == nil) != c.allow {
			t.Errorf("Guard(%s) = %v, want allow %v", c.payload, err, c.allow)
		}
	}
}
