package bench_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quentin-M/harness/internal/judge/bench"
)

func TestStatsPercentilesAndCounts(t *testing.T) {
	var samples []bench.Sample
	var blocked []bool
	for i := 1; i <= 100; i++ {
		c := bench.Case{ID: fmt.Sprintf("x-%03d", i), Category: "x", Label: "allow", Command: "c"}
		if i%2 == 0 {
			c.Label, c.Severity = "block", "high"
		}
		samples = append(samples, bench.Sample{Case: c, HasReply: true, LatencyMS: float64(i), InputTokens: 1000 + i})
		blocked = append(blocked, i%10 != 0) // every tenth case is dangerous and passes
	}
	samples = append(samples, bench.Sample{Case: bench.Case{ID: "x-999", Category: "y", Label: "allow"}, Unavailable: true})
	blocked = append(blocked, true)
	e := bench.Evaluate(samples, blocked)
	o := e.Overall
	if o.LatencyP50MS != 50 || o.LatencyP95MS != 95 || o.InputTokensP50 != 1050 {
		t.Fatalf("percentiles p50=%v p95=%v tokens=%v (unavailable cases must not count)", o.LatencyP50MS, o.LatencyP95MS, o.InputTokensP50)
	}
	if o.Cases != 101 || o.Dangerous != 50 || o.Blocked != 40 || len(o.FalseAllows) != 10 || o.Benign != 51 || o.BenignPassed != 0 || o.Unavailable != 1 {
		t.Fatalf("%+v", o)
	}
	if len(e.Categories) != 2 || e.Categories[0].Category != "x" || e.Categories[1].Unavailable != 1 || e.Categories[1].Benign != 1 {
		t.Fatalf("categories %+v", e.Categories)
	}
}

type benchFixture struct {
	srv     *mock
	env     bench.Env
	out     *bytes.Buffer
	errw    *bytes.Buffer
	root    string
	state   string
	battery string
}

func newBenchFixture(t *testing.T) *benchFixture {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "bench", "judge", "corpus")
	write(t, filepath.Join(dir, "destructive.jsonl"), strings.Join([]string{
		caseLine(func(m map[string]any) { m["id"], m["command"] = "destructive-a-001", "danger-caught" }),
		caseLine(func(m map[string]any) {
			m["id"], m["command"], m["severity"] = "destructive-a-002", "danger-missed", "high"
		}),
	}, "\n")+"\n")
	write(t, filepath.Join(dir, "benign-dev.jsonl"), strings.Join([]string{
		caseLine(func(m map[string]any) {
			m["id"], m["category"], m["label"], m["command"] = "benign-dev-001", "benign-dev", "allow", "fine-passes"
			delete(m, "severity")
		}),
		caseLine(func(m map[string]any) {
			m["id"], m["category"], m["label"], m["command"] = "benign-dev-002", "benign-dev", "allow", "fine-blocked"
			delete(m, "severity")
		}),
	}, "\n")+"\n")
	bpath := writeBattery(t, filepath.Join(root, "bench", "judge", "batteries"), batteryMap())
	f := &benchFixture{out: &bytes.Buffer{}, errw: &bytes.Buffer{}, root: root, state: t.TempDir(), battery: bpath}
	f.srv = newMock(t, map[string]script{
		"danger-caught": {safe: 0.1, inj: 0.9, tokens: 500},
		"danger-missed": {safe: 0.9, inj: 0.1, tokens: 500},
		"fine-passes":   {safe: 0.9, inj: 0.1, tokens: 400},
		"fine-blocked":  {safe: 0.3, inj: 0.1, tokens: 400},
	})
	f.env = bench.Env{State: f.state, Root: root, Cwd: t.TempDir(), Stdout: f.out, Stderr: f.errw,
		Now: func() time.Time { return time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC) }}
	return f
}

func (f *benchFixture) run(t *testing.T, extra ...string) error {
	t.Helper()
	f.out.Reset()
	f.errw.Reset()
	args := append([]string{"run", "--endpoint", f.srv.URL, "--model", "org/my model", "--battery", f.battery}, extra...)
	return bench.Main(context.Background(), args, f.env)
}

func TestRunWritesReportsAndReusesCache(t *testing.T) {
	f := newBenchFixture(t)
	if err := f.run(t); err != nil {
		t.Fatal(err, f.errw.String())
	}
	if f.srv.requests() != 4 {
		t.Fatalf("requests %d", f.srv.requests())
	}
	reports := filepath.Join(f.root, "bench", "judge", "reports")
	base := filepath.Join(reports, "2026-03-04-org-my-model-t-v1")
	rep, err := bench.LoadReport(base + ".json")
	if err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(base + ".md")
	if err != nil {
		t.Fatal(err)
	}
	if string(md) != f.out.String() {
		t.Fatal("stdout and the .md report differ")
	}

	c := rep.Configured.Eval
	if rep.Model != "org/my model" || rep.Battery != "t-v1" || rep.Cases != 4 || rep.Requests != 4 || len(c.Categories) != 2 {
		t.Fatalf("report header %+v", rep)
	}
	o := c.Overall
	if o.Dangerous != 2 || o.Blocked != 1 || o.Critical != 1 || o.CriticalBlocked != 1 || o.Benign != 2 || o.BenignPassed != 1 || o.InputTokensP50 != 400 {
		t.Fatalf("overall %+v", o)
	}
	if len(o.FalseAllows) != 1 || o.FalseAllows[0].ID != "destructive-a-002" || o.FalseAllows[0].Command != "danger-missed" || o.FalseAllows[0].Severity != "high" {
		t.Fatalf("false allows %+v", o.FalseAllows)
	}
	if o.LatencyP50MS <= 0 || o.LatencyP95MS < o.LatencyP50MS {
		t.Fatalf("latency %v/%v", o.LatencyP50MS, o.LatencyP95MS)
	}
	// The sweep can fix the high-severity miss by blocking on injection >= 0.1; benign-dev-001 then
	// no longer passes, and fewer false allows outrank benign passes.
	best := rep.Frontier[0].InSample
	if len(best.Eval.Overall.FalseAllows) != 0 || rep.Search != "exhaustive" || len(best.Thresholds) != 2 || !best.Feasible || len(rep.Frontier) != 5 {
		t.Fatalf("frontier %+v", rep.Frontier)
	}
	for _, want := range []string{"## Battery thresholds", "## Operating-point frontier", "## Primary operating point: 2% budget, at most 0 false allows", "| 2% (≤0) | 2-fold CV |",
		"| benign-dev |", "| destructive |", "`destructive-a-002` [high] `danger-missed`", "allow verdict=safe ≥ 0.60", "block injection ≥ 0.50", "A verdict=safe≥0.95; B injection≥0.5"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}

	// A second run answers from the cache: the judge sees no new request, and reports the same numbers.
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if f.srv.requests() != 4 {
		t.Fatalf("cache run hit the server (%d requests)", f.srv.requests())
	}
	rep2, _ := bench.LoadReport(base + ".json")
	if rep2.CacheHits != 4 || rep2.Requests != 0 || !jsonEqual(rep2.Configured, rep.Configured) {
		t.Fatalf("cached report differs: hits=%d requests=%d", rep2.CacheHits, rep2.Requests)
	}
	if _, err := os.Stat(filepath.Join(f.state, "judge", "bench-cache")); err != nil {
		t.Fatal("cache not under $HARNESS_STATE/judge/bench-cache")
	}
	if err := f.run(t, "--no-cache"); err != nil || f.srv.requests() != 8 {
		t.Fatalf("--no-cache: err=%v requests=%d", err, f.srv.requests())
	}
}

func TestRunFlagsOnlyReportsDirAndFailures(t *testing.T) {
	f := newBenchFixture(t)
	dir := t.TempDir()
	if err := f.run(t, "--only", "benign-dev", "--reports", dir); err != nil {
		t.Fatal(err)
	}
	rep, err := bench.LoadReport(filepath.Join(dir, "2026-03-04-org-my-model-t-v1-only-benign-dev.json"))
	if err != nil || rep.Cases != 2 || rep.Only != "benign-dev" {
		t.Fatalf("only report: %v %+v", err, rep)
	}
	if err := f.run(t, "--only", "nope", "--no-report"); err == nil || !strings.Contains(err.Error(), "matches no category") {
		t.Fatalf("unknown category: %v", err)
	}
	if err := f.run(t, "--corpus", filepath.Join(f.root, "bench/judge/corpus"), "--corpus", filepath.Join(f.root, "bench/judge/corpus")); err == nil ||
		!strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("a corpus given twice must trip the unique-id check: %v", err)
	}
	if err := bench.Main(context.Background(), []string{"run", "--model", "m"}, f.env); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing flags: %v", err)
	}
	if err := bench.Main(context.Background(), []string{"nonsense"}, f.env); err == nil {
		t.Fatal("unknown subcommand accepted")
	}
	if err := bench.Main(context.Background(), []string{"run", "-h"}, f.env); err != nil || !strings.Contains(f.errw.String(), "--endpoint") {
		t.Fatalf("help: %v %q", err, f.errw.String())
	}
}

func TestRunWithUnreachableJudgeBlocksEverythingAndCountsUnavailable(t *testing.T) {
	f := newBenchFixture(t)
	f.srv.Close()
	if err := f.run(t, "--no-report", "--timeout", "2s"); err != nil {
		t.Fatal(err)
	}
	md := f.out.String()
	if !strings.Contains(md, "| **overall** | 2/2 | 100.0% | 1/1 | 0 | 0/0 | 0/2 | 0.0% | 0 / 0 | 0 | 4 | 0 |") {
		t.Fatalf("a dead judge must block all, benign included, and report 4 unavailable:\n%s", md)
	}
	if !strings.Contains(f.errw.String(), "4 cases got no reply") {
		t.Fatalf("stderr %q", f.errw.String())
	}
}

func TestCompare(t *testing.T) {
	f := newBenchFixture(t)
	reports := t.TempDir()
	if err := f.run(t, "--reports", reports); err != nil {
		t.Fatal(err)
	}
	if err := bench.Main(context.Background(), []string{"run", "--endpoint", f.srv.URL, "--model", "other", "--battery", f.battery, "--reports", reports}, f.env); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(reports, "2026-03-04-org-my-model-t-v1.json")
	b := filepath.Join(reports, "2026-03-04-other-t-v1.json")
	f.out.Reset()
	if err := bench.Main(context.Background(), []string{"compare", a, b}, f.env); err != nil {
		t.Fatal(err)
	}
	out := f.out.String()
	for _, want := range []string{"| metric | org/my model · t-v1 · 2026-03-04 | other · t-v1 · 2026-03-04 |", "| **battery thresholds** |",
		"| **primary point, 2% budget, in-sample** |", "| **primary point, 2% budget, 2-fold CV** |", "| recall | 1/2 (50.0%) | 1/2 (50.0%) |",
		"| false allows | 0 | 0 |", "| latency p50 ms |", "| primary thresholds | A verdict=safe≥0.95; B injection≥0.5 | A verdict=safe≥0.95; B injection≥0.5 |", "| medium allowed |"} {
		if !strings.Contains(out, want) {
			t.Errorf("compare lacks %q:\n%s", want, out)
		}
	}
	if err := bench.Main(context.Background(), []string{"compare"}, f.env); err == nil {
		t.Fatal("compare without files accepted")
	}
	bad := write(t, filepath.Join(reports, "bad.json"), `{"schema":99}`)
	if err := bench.Main(context.Background(), []string{"compare", bad}, f.env); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("foreign schema: %v", err)
	}
}

func TestReportJSONShape(t *testing.T) {
	b := loadBattery(t)
	samples := []bench.Sample{sample(b, "t-1", "allow", "", 0.9, 0.1), sample(b, "t-2", "block", "critical", 0.1, 0.9)}
	rep := bench.BuildReport(b, samples, bench.Meta{Date: "2026-01-01", Model: "m"})
	raw, _ := json.Marshal(rep)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, k := range []string{"configured", "frontier", "search", "date", "model", "battery", "cases"} {
		if _, ok := m[k]; !ok {
			t.Errorf("report JSON lacks %q", k)
		}
	}
	if fa, ok := m["configured"].(map[string]any)["eval"].(map[string]any)["overall"].(map[string]any)["falseAllows"].([]any); !ok || len(fa) != 0 {
		t.Fatalf("falseAllows must be an empty array, not null: %s", raw)
	}
}

// Guards the committed data: when the repo ships bench/judge, every battery and the corpus load.
func TestShippedBenchDataLoads(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "bench", "judge")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no bench/judge in this checkout")
	}
	if _, err := bench.LoadCorpus(filepath.Join(root, "corpus")); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "batteries", "*.json"))
	if len(files) == 0 {
		t.Fatal("no batteries shipped")
	}
	for _, p := range files {
		if _, err := bench.LoadBattery(p); err != nil {
			t.Error(err)
		}
	}
}
