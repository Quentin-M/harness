package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

func at(min int) string { return t0.Add(time.Duration(min) * time.Minute).Format(time.RFC3339Nano) }

func jsonl(t testing.TB, recs ...any) string {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		if s, ok := r.(string); ok {
			b.WriteString(s + "\n")
			continue
		}
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func put(t testing.TB, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

type m = map[string]any

func claudeMsg(typ string, min int, content any, extra m) m {
	r := m{"type": typ, "sessionId": "c1", "cwd": "/w/gg", "timestamp": at(min),
		"message": m{"role": typ, "content": content}}
	for k, v := range extra {
		r[k] = v
	}
	return r
}

func text(s string) m { return m{"type": "text", "text": s} }

func claudeFixture(t testing.TB) string {
	return jsonl(t,
		m{"type": "permission-mode", "permissionMode": "default"},
		claudeMsg("user", 0, "fix the flaky lease test", nil),
		claudeMsg("user", 1, "<system-reminder>ctx</system-reminder>", nil),
		claudeMsg("user", 1, "Caveat: the messages below", m{"isMeta": true}),
		claudeMsg("assistant", 2, []m{{"type": "thinking", "thinking": "hmm"}}, nil),
		claudeMsg("assistant", 3, []m{{"type": "tool_use", "name": "Bash", "input": m{"command": "ls"}}}, nil),
		claudeMsg("user", 4, []m{{"type": "tool_result", "content": "out"}}, nil),
		claudeMsg("assistant", 5, []m{text("subagent prose")}, m{"isSidechain": true}),
		claudeMsg("user", 5, "This session is being continued from a previous conversation", m{"isCompactSummary": true}),
		claudeMsg("user", 6, "From: dev-engineer-1@gg\nTo: lead-lead@gg\nSent: 10-13 07:27Z\n---\nQueue handoff", nil),
		claudeMsg("user", 6, "[OpenRig watchdog scheduler · policy: periodic-reminder]", nil),
		claudeMsg("user", 6, "dev-engineer-2@gg\nOpenRig session identity:\n- rig: gg", nil),
		claudeMsg("user", 6, "Another Claude session sent a message:\n<teammate-message>", nil),
		m{"type": "attachment", "timestamp": at(7), "attachment": m{"type": "x"}},
		claudeMsg("assistant", 8, []m{{"type": "thinking", "thinking": "t"}, text("The race is in Release.")}, nil),
		m{"type": "assistant", "timestamp": at(9), "message": m{"role": "assistant", "content": []m{text("Fixed it.")}}},
	)
}

func codexItem(min int, role, phase string, content ...m) m {
	p := m{"type": "message", "role": role, "content": content}
	if phase != "" {
		p["phase"] = phase
	}
	return m{"timestamp": at(min), "type": "response_item", "payload": p}
}

func codexFixture(t testing.TB) string {
	return jsonl(t,
		m{"timestamp": at(10), "type": "session_meta", "payload": m{"id": "x1", "cwd": "/w/q", "base_instructions": m{"text": "big"}}},
		codexItem(10, "developer", "", m{"type": "input_text", "text": "<permissions instructions>"}),
		codexItem(10, "user", "", m{"type": "input_text", "text": "# AGENTS.md instructions for /w/q\n..."}),
		codexItem(10, "user", "", m{"type": "input_text", "text": "<environment_context>\n  <cwd>/w/q</cwd>"}),
		codexItem(11, "user", "", m{"type": "input_text", "text": "review the diff"}),
		m{"timestamp": at(11), "type": "response_item", "payload": m{"type": "reasoning", "summary": []m{}}},
		codexItem(12, "assistant", "commentary", m{"type": "output_text", "text": "Reading the diff now."}),
		m{"timestamp": at(12), "type": "response_item", "payload": m{"type": "custom_tool_call", "name": "exec"}},
		m{"timestamp": at(12), "type": "event_msg", "payload": m{"type": "token_count"}},
		codexItem(13, "user", "", m{"type": "input_text", "text": "From: <unknown sender>\nTo: lead-peer@gg\nSent: x\n---\nQueue handoff"}),
		codexItem(14, "assistant", "final_answer", m{"type": "output_text", "text": "Approve."}),
	)
}

func ompFixture(t testing.TB) string {
	msg := func(min int, role, attribution string, content ...m) m {
		mm := m{"role": role, "content": content, "timestamp": 1791683083633}
		if attribution != "" {
			mm["attribution"] = attribution
		}
		return m{"type": "message", "id": "a", "timestamp": at(min), "message": mm}
	}
	return jsonl(t,
		m{"type": "title", "v": 1, "title": "", "updatedAt": at(20)},
		m{"type": "session", "version": 3, "id": "o1", "timestamp": at(20), "cwd": "/tmp/pod"},
		m{"type": "model_change", "id": "b", "timestamp": at(20), "model": "x"},
		msg(20, "user", "user", text("lead the pod")),
		msg(21, "assistant", "", m{"type": "thinking", "thinking": "t"}, m{"type": "toolCall", "name": "bash"}),
		m{"type": "custom", "customType": "tool_execution_start", "timestamp": at(21)},
		msg(21, "toolResult", "", text("tool out")),
		m{"type": "custom_message", "customType": "advisor", "content": "advice", "timestamp": at(22)},
		msg(22, "user", "agent", text("agent-injected nudge")),
		msg(23, "assistant", "", text("Pod is up."), m{"type": "toolCall", "name": "bash"}),
	)
}

func turns(s Session) []string {
	out := make([]string, len(s.Turns))
	for i, t := range s.Turns {
		out[i] = t.Role + ": " + t.Text
	}
	return out
}

func TestSince(t *testing.T) {
	state := t.TempDir()
	now := t0.Add(time.Hour)
	put(t, filepath.Join(state, "claude/projects/-w-gg/c1.jsonl"), claudeFixture(t), now)
	put(t, filepath.Join(state, "claude/projects/-w-gg/c1/subagents/agent-a.jsonl"),
		jsonl(t, claudeMsg("user", 1, "subagent ask", nil)), now)
	put(t, filepath.Join(state, "codex/sessions/2026/10/12/rollout-x1.jsonl"), codexFixture(t), now)
	put(t, filepath.Join(state, "omp/agent/sessions/-tmp-pod/2026_o1.jsonl"), ompFixture(t), now)
	put(t, filepath.Join(state, "omp/agent/sessions/-tmp-pod/2026_o1/Engineer.jsonl"), ompFixture(t), now)
	put(t, filepath.Join(state, "claude/projects/-w-gg/noise.jsonl"),
		jsonl(t, claudeMsg("user", 30, "<command-name>/clear</command-name>", nil)), now)

	got, err := Since(state, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		harness, id, cwd string
		start, end       int
		turns            []string
	}{
		{"claude", "c1", "/w/gg", 0, 9, []string{
			"user: fix the flaky lease test", "assistant: The race is in Release.", "assistant: Fixed it."}},
		{"codex", "x1", "/w/q", 11, 14, []string{"user: review the diff", "assistant: Approve."}},
		{"omp", "o1", "/tmp/pod", 20, 23, []string{"user: lead the pod", "assistant: Pod is up."}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sessions, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		s := got[i]
		if s.Harness != w.harness || s.ID != w.id || s.Cwd != w.cwd {
			t.Errorf("session %d = %s %s %s, want %s %s %s", i, s.Harness, s.ID, s.Cwd, w.harness, w.id, w.cwd)
		}
		if !s.Start.Equal(t0.Add(time.Duration(w.start)*time.Minute)) || !s.End.Equal(t0.Add(time.Duration(w.end)*time.Minute)) {
			t.Errorf("%s span = %v..%v, want +%dm..+%dm", s.Harness, s.Start, s.End, w.start, w.end)
		}
		if !slices.Equal(turns(s), w.turns) {
			t.Errorf("%s turns:\n got %q\nwant %q", s.Harness, turns(s), w.turns)
		}
		if !strings.HasPrefix(s.Path, state) {
			t.Errorf("%s path %q outside state", s.Harness, s.Path)
		}
	}
}

func TestSinceCutoff(t *testing.T) {
	state := t.TempDir()
	cut := t0.Add(5 * time.Minute)
	put(t, filepath.Join(state, "claude/projects/-w/old.jsonl"),
		jsonl(t, claudeMsg("user", 10, "would be kept if opened", nil)), cut.Add(-time.Second))
	put(t, filepath.Join(state, "claude/projects/-w/mixed.jsonl"), jsonl(t,
		claudeMsg("user", 0, "before the cutoff", nil),
		claudeMsg("assistant", 6, []m{text("after the cutoff")}, nil),
	), cut.Add(time.Minute))
	put(t, filepath.Join(state, "claude/projects/-w/stale.jsonl"),
		jsonl(t, claudeMsg("user", 1, "only before", nil)), cut.Add(time.Minute))

	got, err := Since(state, cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0].Path) != "mixed.jsonl" {
		t.Fatalf("got %+v, want only mixed.jsonl", got)
	}
	if want := []string{"assistant: after the cutoff"}; !slices.Equal(turns(got[0]), want) {
		t.Fatalf("turns %q, want %q", turns(got[0]), want)
	}
}

func TestSinceCwdFallback(t *testing.T) {
	state := t.TempDir()
	put(t, filepath.Join(state, "claude/projects/-w-gg/nocwd.jsonl"), jsonl(t,
		m{"type": "user", "timestamp": at(0), "message": m{"role": "user", "content": "hi"}}), t0)
	got, err := Since(state, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Cwd != "-w-gg" || got[0].ID != "nocwd" {
		t.Fatalf("got %+v, want cwd -w-gg id nocwd", got)
	}
}

func TestLongAndTruncatedLines(t *testing.T) {
	state := t.TempDir()
	huge := strings.Repeat("x", 2<<20)
	body := jsonl(t,
		claudeMsg("user", 0, []m{{"type": "tool_result", "content": huge}}, nil),
		claudeMsg("user", 1, "after the huge line "+huge, nil),
		claudeMsg("assistant", 2, []m{text("done")}, nil),
	) + `{"type":"assistant","timestamp":"` + at(3) + `","message":{"role":"assistant","content":[{"type":"text","te`
	put(t, filepath.Join(state, "claude/projects/-w/long.jsonl"), body, t0)

	got, err := Since(state, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Turns) != 2 {
		t.Fatalf("got %+v, want one session with 2 turns", got)
	}
	u := got[0].Turns[0].Text
	if !strings.HasPrefix(u, "after the huge line") || len(u) > maxTurn {
		t.Fatalf("user turn %d bytes %.30q, want clipped to %d", len(u), u, maxTurn)
	}
	if got[0].Turns[1].Text != "done" {
		t.Fatalf("assistant turn %q", got[0].Turns[1].Text)
	}
}

func TestNoise(t *testing.T) {
	for _, c := range []struct {
		text string
		drop bool
	}{
		{"<system-reminder>x</system-reminder>", true},
		{"<task-notification>\n<task-id>1", true},
		{"<local-command-stdout>Set model", true},
		{"<command-name>/clear</command-name>", true},
		{"<command-message>ketchup</command-message>", true},
		{"<teammate-message teammate_id=\"a\">", true},
		{"<user-prompt-submit-hook>x", true},
		{"<environment_context>\n  <cwd>/w</cwd>", true},
		{"<bash-stdout>Command did not complete", true},
		{"# AGENTS.md instructions for /w", true},
		{"# Profile: primary (lead-lead@gg)\n\nYou own:", true},
		{"Caveat: The messages below were generated", true},
		{"[Request interrupted by user]", true},
		{"[Request interrupted by user for tool use]", true},
		{"[OpenRig watchdog scheduler · policy: periodic-reminder · job: 01]", true},
		{"Another Claude session sent a message:\n<teammate-message>", true},
		{"This session is being continued from a previous conversation that ran out", true},
		{"From: dev-engineer-1@gg\nTo: lead-lead@gg\nSent: 10-13\n---\nQueue handoff: qitem-1", true},
		{"From: <unknown sender>\nTo: lead-peer@gg\nSent: 10-13\n---\nQueue handoff", true},
		{"verify-judge@gg\nOpenRig session identity:\n- rig: gg", true},
		{"fix the flaky lease test", false},
		{"[Image #1] error at the bottom..?", false},
		{"# heading I typed", false},
		{"From: a customer email\nwhy is this bouncing", false},
		{"see <system-reminder> in the log", false},
		{"yes", false},
	} {
		if got := noise(c.text); got != c.drop {
			t.Errorf("noise(%.40q) = %v, want %v", c.text, got, c.drop)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("héllo", 4); got != "h…" {
		t.Errorf("clip = %q", got)
	}
	if got := clip("abc", 3); got != "abc" {
		t.Errorf("clip = %q", got)
	}
}

func TestDigest(t *testing.T) {
	s := Session{Harness: "claude", ID: "c1", Cwd: "/w/gg", Start: t0, End: t0.Add(time.Hour)}
	for i := range 40 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		s.Turns = append(s.Turns, Turn{Role: role, Text: fmt.Sprintf("turn-%02d %s", i, strings.Repeat("w", 300)), At: t0.Add(time.Duration(i) * time.Minute)})
	}

	full := s.Digest(1 << 20)
	if !strings.Contains(full, "turn-00") || !strings.Contains(full, "turn-39") || strings.Contains(full, "elided") {
		t.Fatalf("unbounded digest lost turns or marked elision")
	}
	if !strings.HasPrefix(full, "## claude c1 /w/gg 2026-10-12 09:00Z") {
		t.Fatalf("header %.60q", full)
	}

	for _, max := range []int{3000, 1200, 500} {
		d := s.Digest(max)
		if len(d) > max {
			t.Errorf("Digest(%d) = %d bytes", max, len(d))
		}
		if !strings.Contains(d, "turn-00") || !strings.Contains(d, "turn-39") {
			t.Errorf("Digest(%d) dropped the first or last turn", max)
		}
		if !strings.Contains(d, "turns elided") {
			t.Errorf("Digest(%d) has no elision mark", max)
		}
		if strings.Index(d, "turn-00") > strings.Index(d, "elided") || strings.Index(d, "elided") > strings.Index(d, "turn-39") {
			t.Errorf("Digest(%d) out of order", max)
		}
	}

	if d := s.Digest(20); len(d) > 20 {
		t.Errorf("Digest(20) = %d bytes", len(d))
	}
	one := Session{Harness: "codex", ID: "x", Turns: []Turn{{Role: "user", Text: strings.Repeat("z", 5000), At: t0}}}
	if d := one.Digest(1000); len(d) > 1000 || !strings.Contains(d, "user: zzz") {
		t.Errorf("single-turn Digest(1000) = %d bytes", len(d))
	}
}

// BenchmarkSince reads one 50MB Claude transcript shaped like a busy real one: mostly tool
// traffic with large results, a minority of prose turns.
func BenchmarkSince(b *testing.B) {
	state := b.TempDir()
	path := filepath.Join(state, "claude/projects/-w/big.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	w := bufio.NewWriter(f)
	result := strings.Repeat("r", 20<<10)
	for i := 0; ; i++ {
		var rec m
		switch i % 4 {
		case 0:
			rec = claudeMsg("user", i, fmt.Sprintf("ask %d %s", i, strings.Repeat("q", 200)), nil)
		case 1:
			rec = claudeMsg("assistant", i, []m{{"type": "tool_use", "name": "Bash", "input": m{"command": "ls"}}}, nil)
		case 2:
			rec = claudeMsg("user", i, []m{{"type": "tool_result", "content": result}}, nil)
		case 3:
			rec = claudeMsg("assistant", i, []m{text(strings.Repeat("a", 800))}, nil)
		}
		line, _ := json.Marshal(rec)
		w.Write(line)
		w.WriteByte('\n')
		if i%64 == 0 {
			if fi, _ := f.Stat(); fi.Size() > 50<<20 {
				break
			}
		}
	}
	if err := w.Flush(); err != nil {
		b.Fatal(err)
	}
	fi, _ := f.Stat()
	f.Close()

	b.SetBytes(fi.Size())
	b.ReportAllocs()
	for b.Loop() {
		got, err := Since(state, time.Time{})
		if err != nil || len(got) != 1 || len(got[0].Turns) == 0 {
			b.Fatalf("Since = %d sessions, %v", len(got), err)
		}
	}
}
