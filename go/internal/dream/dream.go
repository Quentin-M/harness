// Package dream distils recent agent sessions into each project's shared memory directory, one
// headless claude run per project.
package dream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	assets "github.com/Quentin-M/harness"
	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/lease"
	"github.com/Quentin-M/harness/internal/memory"
	"github.com/Quentin-M/harness/internal/sys"
	"github.com/Quentin-M/harness/internal/transcript"
)

const (
	// Stale is how old the last successful dream gets before a launch starts the next one.
	Stale = 20 * time.Hour
	// SpawnGap spaces detached spawns: a rig bringing up 20 seats starts one dream, and a failing
	// project is retried at most hourly.
	SpawnGap = time.Hour

	firstWindow   = 24 * time.Hour
	maxWindow     = 7 * 24 * time.Hour
	digestBudget  = 200_000
	minShare      = 4_000 // smallest digest worth sending
	instructBytes = 40_000
	resource      = "dream"
)

// Tools is everything a dream may call: file access inside the memory directory.
const Tools = "Read,Write,Edit,Glob,Grep"

type Runner struct {
	State   string
	Claude  string  // claude binary
	Self    string  // harness binary, which serves the guard hook
	Model   string  // resolved model id
	Env     sys.Env // the interactive claude launch env: config dir and gateway
	Lease   *lease.Store
	Timeout time.Duration
	Now     func() time.Time
	Log     io.Writer
	Out     io.Writer
}

type Options struct {
	Project string        // dream only the project holding this dir, now, ignoring stamps
	Since   time.Duration // window length; 0 means since the last successful dream
	DryRun  bool
}

type project struct {
	root, dir string
	sessions  []transcript.Session
}

func (p project) key() string { return filepath.Base(filepath.Dir(p.dir)) }

func lastPath(state string) string       { return filepath.Join(state, "dream", "last") }
func stampPath(state, key string) string { return filepath.Join(state, "dream", "projects", key) }
func LogPath(state string) string        { return filepath.Join(state, "dream", "dream.log") }
func reportPath(state, key string, t time.Time) string {
	return filepath.Join(state, "dream", t.Format("2006-01-02"), key+".md")
}

// Run dreams every project with sessions in the window. The last-success stamp advances only when
// every project succeeded; each project keeps its own stamp, so a retry redoes only the failures.
func (r *Runner) Run(o Options) error {
	now := r.Now()
	holder := fmt.Sprintf("dream-%d", os.Getpid())
	if !o.DryRun {
		ok, err := r.Lease.Acquire(resource, 1, holder)
		if err != nil {
			return fmt.Errorf("dream: %w", err)
		}
		if !ok {
			r.logf("another dream holds the lease; exiting")
			return nil
		}
		defer r.Lease.Release(resource, holder)
	}
	stamps := o.Project == "" && o.Since == 0
	from, err := r.windowStart(now, o)
	if err != nil {
		return err
	}
	sessions, err := transcript.Since(r.State, from)
	if err != nil {
		return fmt.Errorf("dream: %w", err)
	}
	projects := r.group(sessions)
	if o.Project != "" {
		want := memory.Dir(r.State, o.Project)
		projects = slices.DeleteFunc(projects, func(p project) bool { return p.dir != want })
	}
	r.logf("dream start: window %s, %d sessions, %d projects", from.UTC().Format(time.RFC3339), len(sessions), len(projects))

	var failed []string
	for _, p := range projects {
		if stamps {
			t, err := readStamp(stampPath(r.State, p.key()))
			if err != nil {
				r.logf("project %s: %v", p.root, err)
				failed = append(failed, p.root)
				continue
			}
			p.sessions = after(p.sessions, t)
			if len(p.sessions) == 0 {
				continue
			}
		}
		prompt, err := r.prompt(p)
		if err != nil {
			r.logf("project %s: %v", p.root, err)
			failed = append(failed, p.root)
			continue
		}
		if o.DryRun {
			fmt.Fprintf(r.Out, "%-60s %4d sessions %8d bytes  %s\n", p.root, len(p.sessions), len(prompt), p.dir)
			continue
		}
		if _, err := r.Lease.Acquire(resource, 1, holder); err != nil {
			return fmt.Errorf("dream: renew lease: %w", err)
		}
		start := time.Now()
		report, err := r.dream(p, prompt)
		if err == nil {
			err = r.save(p, report, now, stamps)
		}
		if err != nil {
			r.logf("project %s: failed after %s: %v", p.root, time.Since(start).Round(time.Second), err)
			failed = append(failed, p.root)
			continue
		}
		r.logf("project %s: ok in %s, %d sessions, %d prompt bytes, report %s", p.root,
			time.Since(start).Round(time.Second), len(p.sessions), len(prompt), reportPath(r.State, p.key(), now))
	}
	if len(failed) > 0 {
		return fmt.Errorf("dream: %d of %d projects failed: %s", len(failed), len(projects), strings.Join(failed, ", "))
	}
	if stamps && !o.DryRun {
		if err := writeStamp(lastPath(r.State), now); err != nil {
			return err
		}
	}
	r.logf("dream done")
	return nil
}

func (r *Runner) windowStart(now time.Time, o Options) (time.Time, error) {
	if o.Since > 0 {
		return now.Add(-o.Since), nil
	}
	if o.Project != "" {
		return now.Add(-firstWindow), nil
	}
	last, err := readStamp(lastPath(r.State))
	if err != nil {
		return time.Time{}, err
	}
	if last.IsZero() {
		return now.Add(-firstWindow), nil
	}
	if oldest := now.Add(-maxWindow); last.Before(oldest) {
		return oldest, nil
	}
	return last, nil
}

// group buckets sessions by memory directory, dropping sessions in deleted checkouts and the
// harness's own state dir (dreams themselves run there).
func (r *Runner) group(ss []transcript.Session) []project {
	var out []project
	idx := map[string]int{}
	for _, s := range ss {
		if !filepath.IsAbs(s.Cwd) || within(s.Cwd, r.State) {
			continue
		}
		root := memory.Root(s.Cwd)
		if _, err := os.Stat(root); err != nil || within(root, r.State) {
			continue
		}
		dir := memory.Dir(r.State, s.Cwd)
		i, ok := idx[dir]
		if !ok {
			i = len(out)
			idx[dir] = i
			out = append(out, project{root: root, dir: dir})
		}
		out[i].sessions = append(out[i].sessions, s)
	}
	return out
}

func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// after keeps the turns at or after t.
func after(ss []transcript.Session, t time.Time) []transcript.Session {
	var out []transcript.Session
	for _, s := range ss {
		i := slices.IndexFunc(s.Turns, func(tu transcript.Turn) bool { return !tu.At.Before(t) })
		if i < 0 {
			continue
		}
		s.Turns = s.Turns[i:]
		s.Start = s.Turns[0].At
		out = append(out, s)
	}
	return out
}

func (r *Runner) prompt(p project) (string, error) {
	var b strings.Builder
	b.WriteString(assets.Dream)
	fmt.Fprintf(&b, "\n# Project\n\nRoot: %s\nMemory directory: %s\n", p.root, p.dir)

	instructions := func(title, path string) bool {
		text, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		fmt.Fprintf(&b, "\n## %s\n\n<instructions>\n%s\n</instructions>\n", title,
			clip(strings.TrimSpace(string(text)), instructBytes))
		return true
	}
	instructions("User instructions, loaded in every project", filepath.Join(r.State, "claude", "CLAUDE.md"))
	_ = instructions("Project instructions", filepath.Join(p.root, "CLAUDE.md")) ||
		instructions("Project instructions", filepath.Join(p.root, "AGENTS.md"))

	b.WriteString("\n## Current memory\n\n")
	ents, err := os.ReadDir(p.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("list memory: %w", err)
	}
	if len(ents) == 0 {
		b.WriteString("The directory is empty.\n")
	}
	for _, e := range ents {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			fmt.Fprintf(&b, "- %s (%d bytes)\n", e.Name(), info.Size())
		}
	}
	idx, err := memory.Index(p.dir)
	if err != nil {
		return "", err
	}
	if idx != "" {
		fmt.Fprintf(&b, "\nMEMORY.md:\n\n%s\n", idx)
	}

	b.WriteString("\n## Sessions\n\n<transcripts>\n")
	b.WriteString(digests(p.sessions, digestBudget))
	b.WriteString("</transcripts>\n\nUpdate the memory directory now, then print the report.\n")
	return b.String(), nil
}

// digests renders sessions oldest first within budget bytes. Identical turns repeat across
// watchdog seats, so each is kept once, in its newest session. Newest sessions are kept while each
// can still get min(its size, minShare); the budget is then shared fairly, small sessions whole.
func digests(ss []transcript.Session, budget int) string {
	newest := slices.Clone(ss)
	slices.SortStableFunc(newest, func(a, b transcript.Session) int { return b.End.Compare(a.End) })
	seen := map[string]bool{}
	var kept []transcript.Session
	var size []int
	floor := 0
	for _, s := range newest {
		var turns []transcript.Turn
		for _, t := range s.Turns {
			k := t.Role + "\x00" + t.Text
			if !seen[k] {
				seen[k] = true
				turns = append(turns, t)
			}
		}
		if len(turns) == 0 {
			continue
		}
		s.Turns = turns
		n := len(s.Digest(math.MaxInt))
		if floor+min(n, minShare) > budget {
			break
		}
		floor += min(n, minShare)
		kept, size = append(kept, s), append(size, n)
	}
	order := make([]int, len(kept))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return size[a] - size[b] })
	share, left := make([]int, len(kept)), budget
	for n, i := range order {
		share[i] = min(size[i], left/(len(kept)-n))
		left -= share[i]
	}
	var b strings.Builder
	for i := len(kept) - 1; i >= 0; i-- {
		b.WriteString(kept[i].Digest(share[i]))
	}
	return b.String()
}

// args confines claude to dir. Managed settings allow Write(*) and Edit(*), which beats --add-dir
// and every permission mode, so the guard hook is what keeps each tool call inside dir.
func (r *Runner) args(dir string) ([]string, error) {
	guard := shellQuote(r.Self) + " dream guard " + shellQuote(dir)
	settings, err := json.Marshal(map[string]any{
		"outputStyle": "default",
		"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": ".*",
			"hooks": []any{map[string]any{"type": "command", "command": guard, "timeout": 10}}}}},
	})
	if err != nil {
		return nil, fmt.Errorf("dream settings: %w", err)
	}
	return []string{"-p", "--model", r.Model, "--tools", Tools, "--add-dir", dir,
		"--permission-mode", "acceptEdits", "--strict-mcp-config", "--no-session-persistence",
		"--disable-slash-commands", "--settings", string(settings), "--output-format", "text"}, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Guard answers the dream's PreToolUse hook: nil when the call stays inside dir, else the reason
// to deny it. Anything it cannot parse is denied.
func Guard(dir string, stdin io.Reader) error {
	var in struct {
		Tool  string         `json:"tool_name"`
		Input map[string]any `json:"tool_input"`
	}
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in); err != nil {
		return fmt.Errorf("dream guard: hook payload: %w", err)
	}
	if !slices.Contains(strings.Split(Tools, ","), in.Tool) {
		return fmt.Errorf("dream guard: tool %q is not allowed", in.Tool)
	}
	real := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		real = r
	}
	for _, k := range []string{"file_path", "path", "pattern"} {
		v, ok := in.Input[k]
		if !ok || in.Tool == "Grep" && k == "pattern" {
			continue
		}
		p, ok := v.(string)
		if !ok {
			return fmt.Errorf("dream guard: %s %s is not a string", in.Tool, k)
		}
		if k == "pattern" && !filepath.IsAbs(p) && !slices.Contains(strings.Split(filepath.ToSlash(p), "/"), "..") {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if p = filepath.Clean(p); !within(p, dir) && !within(p, real) {
			return fmt.Errorf("dream guard: %s %s %q is outside the memory directory %s", in.Tool, k, p, dir)
		}
	}
	return nil
}

// env is the interactive launch env minus the markers of a parent claude session. The prompt
// carries the project instructions, so the child skips CLAUDE.md files and its own auto memory.
func (r *Runner) env() []string {
	e := r.Env.Clone()
	e.Unset("CLAUDECODE")
	e.Unset("CLAUDE_CODE_ENTRYPOINT")
	e.Set("HARNESS_DREAM", "1")
	e.Set("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1")
	e.Set("CLAUDE_CODE_DISABLE_CLAUDE_MDS", "1")
	return e.Environ()
}

func (r *Runner) dream(p project, prompt string) (string, error) {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return "", fmt.Errorf("memory dir: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()
	args, err := r.args(p.dir)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, r.Claude, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = p.dir, r.env(), strings.NewReader(prompt)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, r.Log
	// claude runs tools in child processes; kill the whole group so none outlives the timeout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("claude timed out after %s", r.Timeout)
	}
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	return out.String(), prune(p.dir)
}

// prune deletes the topic files a dream emptied: its tools can write but not remove, which keeps
// every change inside the memory directory.
func prune(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("prune memory: %w", err)
	}
	for _, e := range ents {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("prune memory: %w", err)
		}
		if len(bytes.TrimSpace(b)) == 0 {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("prune memory: %w", err)
			}
		}
	}
	return nil
}

// save writes the report, and the project stamp when the run covered the window since its last dream.
func (r *Runner) save(p project, report string, now time.Time, stamp bool) error {
	body := fmt.Sprintf("# %s\n\n%s\n\n%s\n", p.root, p.dir, strings.TrimSpace(report))
	if err := fsx.Write(reportPath(r.State, p.key(), now), []byte(body)); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	if !stamp {
		return nil
	}
	return writeStamp(stampPath(r.State, p.key()), now)
}

func (r *Runner) logf(format string, a ...any) {
	fmt.Fprintf(r.Log, "%s %s\n", r.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

func readStamp(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("dream stamp: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}, fmt.Errorf("dream stamp %s: %w", path, err)
	}
	return t, nil
}

func writeStamp(path string, t time.Time) error {
	if err := fsx.Write(path, []byte(t.UTC().Format(time.RFC3339Nano)+"\n")); err != nil {
		return fmt.Errorf("dream stamp: %w", err)
	}
	return nil
}

// Due reports whether the last successful dream is missing, unreadable or older than Stale.
func Due(state string, now time.Time) bool {
	t, err := readStamp(lastPath(state))
	return err != nil || now.Sub(t) > Stale
}

// Spawn starts `exe dream` detached when one is due, at most once per SpawnGap across processes.
// Its output appends to the dream log; the caller never waits on it.
func Spawn(state, exe string, environ []string, gate *lease.Store) error {
	if !Due(state, gate.Now()) {
		return nil
	}
	holder := fmt.Sprintf("launch-%d-%d", os.Getpid(), time.Now().UnixNano())
	if ok, err := gate.Acquire("dream-spawn", 1, holder); err != nil || !ok {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(LogPath(state)), 0o755); err != nil {
		return err
	}
	log, err := os.OpenFile(LogPath(state), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(exe, "dream")
	cmd.Env, cmd.Stdout, cmd.Stderr = environ, log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn dream: %w", err)
	}
	return cmd.Process.Release()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "\n[… cut]"
}
