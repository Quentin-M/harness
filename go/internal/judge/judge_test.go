package judge_test

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Quentin-M/harness/internal/judge"
	"github.com/Quentin-M/harness/internal/judge/judgetest"
	"github.com/Quentin-M/harness/internal/sys"
)

func writeFile(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestReadyJudgeWiresTheGate(t *testing.T) {
	f := judgetest.New(t)
	o := f.Local.Prepare()
	if !o.Ready {
		t.Fatalf("not ready: %+v", o)
	}
	if o.Extension != f.Extension || !strings.HasSuffix(o.Extension, "bench/judge/extension/gate.ts") {
		t.Fatalf("extension %q, want %q", o.Extension, f.Extension)
	}
	want := map[string]string{judge.GateEnv: "/bin/harness", "HARNESS_ROOT": f.Root, "HARNESS_STATE": f.State,
		"HARNESS_JUDGE_PORT": itoa(f.Server.Port)}
	if !maps.Equal(o.Env, want) {
		t.Fatalf("env %v, want %v", o.Env, want)
	}
	if f.Runner.Calls() != 0 {
		t.Fatalf("a ready judge must start nothing: %+v", f.Runner)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Every static reason leaves a vanilla launch that touches nothing: no process, no request, no file.
func TestIncapableLaunchesNothing(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(f *judgetest.Fixture)
		reason string
	}{
		{"linux", func(f *judgetest.Fixture) { f.Local.Host.OS = "linux" }, "not macOS"},
		{"intel mac", func(f *judgetest.Fixture) { f.Local.Host.Arch = "amd64" }, "not Apple Silicon"},
		{"small ram", func(f *judgetest.Fixture) { f.Local.Host.Memory = 16 << 30 }, "16 GiB"},
		{"opted out", func(f *judgetest.Fixture) { f.Local.Off = true }, "HARNESS_JUDGE=off"},
		{"no uv", func(f *judgetest.Fixture) { f.Local.Sys.Env["HARNESS_UV_BIN"] = "/nonexistent/uv" }, "uv not installed"},
		{"no gate battery", func(f *judgetest.Fixture) { os.Remove(f.Gate) }, "gate battery"},
		{"no extension", func(f *judgetest.Fixture) { os.Remove(f.Extension) }, "extension"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := judgetest.New(t)
			c.break_(f)
			o := f.Local.Prepare()
			if o.Ready || o.Transient || !strings.Contains(o.Reason, c.reason) {
				t.Fatalf("outcome %+v, want permanent off containing %q", o, c.reason)
			}
			if f.Runner.Calls() != 0 || f.Server.Hits.Load() != 0 {
				t.Fatalf("incapable launch touched the world: calls=%d hits=%d", f.Runner.Calls(), f.Server.Hits.Load())
			}
			if f.Log.Len() != 0 {
				t.Fatalf("incapable launch made noise: %q", f.Log.String())
			}
		})
	}
}

// The adapter server answers /health before any model is loaded, and a cold load outlasts the gate's
// budget: only a decision request that returns a typed answer proves the judge.
func TestHealthAloneIsNotReadiness(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*judgetest.Server)
	}{
		{"model still loading", func(s *judgetest.Server) { s.SetLoaded(false) }},
		{"another model served", func(s *judgetest.Server) { s.SetModel("d1-3B") }},
		{"untyped answer", func(s *judgetest.Server) { s.SetBadShape(true) }},
		{"not a judge", func(s *judgetest.Server) { s.SetNotJudge(true) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := judgetest.New(t)
			f.Local.Budget = 400 * time.Millisecond
			c.set(f.Server)
			o := f.Local.Prepare()
			if o.Ready || !o.Transient {
				t.Fatalf("outcome %+v", o)
			}
			if len(f.Runner.Detached) != 0 {
				t.Fatalf("a listening server was restarted or shadowed: %+v", f.Runner.Detached)
			}
			if !strings.Contains(f.Log.String(), "without the classifier") {
				t.Fatalf("no note: %q", f.Log.String())
			}
		})
	}
}

func TestColdStartWaitsForARealAnswer(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	f.Runner.OnDetach = bootsAfter(f, 300*time.Millisecond)
	o := f.Local.Prepare()
	if !o.Ready {
		t.Fatalf("not ready: %+v (%s)", o, f.Log.String())
	}
	if len(f.Runner.Detached) != 1 {
		t.Fatalf("detached %+v", f.Runner.Detached)
	}
	c := f.Runner.Detached[0]
	want := []string{"run", "--script", filepath.Join(f.Root, "bench", "judge", "adapters", "jev_systemone.py"),
		f.ModelDir, itoa(f.Server.Port), f.Adapter, "--model-id", "jev-9b-ft-v3", "--idle-exit-secs", "21600"}
	if c.Name != f.UV || !slices.Equal(c.Args, want) {
		t.Fatalf("server start %s %v, want %v", c.Name, c.Args, want)
	}
	if c.Env["JUDGE_FT_WORK"] != f.Work || c.Env["HARNESS_STATE"] != f.State {
		t.Fatalf("python env %v", c.Env)
	}
	if c.Log != filepath.Join(f.State, "judge", "server.log") {
		t.Fatalf("log %q", c.Log)
	}
	if _, leaked := c.Env["ANTHROPIC_AUTH_TOKEN"]; leaked {
		t.Fatalf("gateway secret reached the detached server: %v", c.Env)
	}
}

// bootsAfter makes a started server listen at once, then finish loading its model after d.
func bootsAfter(f *judgetest.Fixture, d time.Duration) func(judgetest.Call) {
	return func(judgetest.Call) {
		f.Server.SetLoaded(false)
		f.Server.Up()
		go func() { time.Sleep(d); f.Server.SetLoaded(true) }()
	}
}

// The adapter script binds 127.0.0.1 only and takes no host flag, so the line carries no way to
// widen it; the model dir, port and adapter are positional.
func TestServeArgsAreTheAdapterServerLine(t *testing.T) {
	got := judge.ServeArgs("/r/jev_systemone.py", "/m/jev", 11436, "/w/runs/v3", 6*time.Hour)
	want := []string{"run", "--script", "/r/jev_systemone.py", "/m/jev", "11436", "/w/runs/v3",
		"--model-id", "jev-9b-ft-v3", "--idle-exit-secs", "21600"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestConcurrentLaunchesStartOneServer(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	f.Runner.OnDetach = bootsAfter(f, 300*time.Millisecond)
	var wg sync.WaitGroup
	outs := make([]judge.Outcome, 6)
	for i := range outs {
		l := *f.Local // each launch is its own process; they share the state dir only
		wg.Add(1)
		go func() { defer wg.Done(); outs[i] = l.Prepare() }()
	}
	wg.Wait()
	for i, o := range outs {
		if !o.Ready {
			t.Fatalf("launch %d: %+v", i, o)
		}
	}
	if n := len(f.Runner.Detached); n != 1 {
		t.Fatalf("%d servers started, want 1", n)
	}
}

func TestMissingModelDownloadsInTheBackgroundOnce(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	os.RemoveAll(f.ModelDir)
	o := f.Local.Prepare()
	if o.Ready || !o.Transient || !strings.Contains(o.Reason, "downloading") {
		t.Fatalf("outcome %+v", o)
	}
	if len(f.Runner.Detached) != 1 {
		t.Fatalf("detached %+v", f.Runner.Detached)
	}
	d := f.Runner.Detached[0]
	if d.Name != "/bin/harness" || !slices.Equal(d.Args, []string{"judge", "fetch"}) || d.Env["HARNESS_STATE"] != f.State ||
		d.Env["HARNESS_ROOT"] != f.Root {
		t.Fatalf("fetch spawn %+v", d)
	}
	f.Local.Prepare() // an hour has not passed: no second download attempt
	if len(f.Runner.Detached) != 1 {
		t.Fatalf("download respawned inside the gap: %+v", f.Runner.Detached)
	}
	f.Clock.Advance(judge.FetchGap + time.Minute)
	f.Local.Prepare()
	if len(f.Runner.Detached) != 2 {
		t.Fatalf("download not retried after the gap: %+v", f.Runner.Detached)
	}
}

// A downloaded model without the adapter fetches the published one in the background, starts no
// server, and never trains by itself.
func TestMissingAdapterIsDownloadedNotTrained(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	os.RemoveAll(f.Adapter)
	o := f.Local.Prepare()
	if o.Ready || !o.Transient || !strings.Contains(o.Reason, "downloading") {
		t.Fatalf("outcome %+v", o)
	}
	if len(f.Runner.Detached) != 1 || !slices.Equal(f.Runner.Detached[0].Args, []string{"judge", "fetch"}) || len(f.Runner.Ran) != 0 {
		t.Fatalf("want only the fetch spawn: %+v %+v", f.Runner.Detached, f.Runner.Ran)
	}
	if !strings.Contains(f.Log.String(), "without the classifier") {
		t.Fatalf("no note: %q", f.Log.String())
	}
	os.MkdirAll(f.Adapter, 0o755) // weights without their config are no adapter either
	writeFile(t, filepath.Join(f.Adapter, "adapter.safetensors"), "a")
	if o := f.Local.Prepare(); o.Ready || !strings.Contains(o.Reason, "downloading") || len(f.Runner.Detached) != 1 {
		t.Fatalf("outcome %+v detached %+v", o, f.Runner.Detached)
	}
}

// The probe is the gate battery itself: the gate's questions, a harmless bash state.
func TestProbeSendsTheGateQuestions(t *testing.T) {
	f := judgetest.New(t)
	if o := f.Local.Prepare(); !o.Ready {
		t.Fatalf("not ready: %+v", o)
	}
	var req struct {
		Model string            `json:"model"`
		State map[string]string `json:"state"`
		Q     map[string]struct {
			Type string `json:"type"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(f.Server.LastDecision(), &req); err != nil {
		t.Fatal(err)
	}
	wantState := map[string]string{"tool": "bash", "workingDirectory": "/", "command": "true"}
	if req.Model != "jev-9b-ft-v3" || !jsonEqual(req.State, wantState) {
		t.Fatalf("model %q state %v", req.Model, req.State)
	}
	if len(req.Q) != 3 || req.Q["verdict"].Type != "choice" || req.Q["data_leaves_machine"].Type != "noul" ||
		req.Q["untrusted_code"].Type != "noul" {
		t.Fatalf("questions %v", req.Q)
	}
}

// A choice answer to a noul question is not the declared type.
func TestWrongTypedAnswerIsNotReady(t *testing.T) {
	f := judgetest.New(t)
	f.Local.Budget = 300 * time.Millisecond
	f.Server.SetBadShape(true)
	if o := f.Local.Prepare(); o.Ready || !strings.Contains(o.Reason, "no typed") {
		t.Fatalf("outcome %+v", o)
	}
}

func TestUnfinishedDownloadIsNotAModel(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	writeFile(t, filepath.Join(f.ModelDir, "model-00002-of-00002.safetensors.partial"), "x")
	o := f.Local.Prepare()
	if o.Ready || !strings.Contains(o.Reason, "downloading") {
		t.Fatalf("outcome %+v", o)
	}
	for _, c := range f.Runner.Detached {
		if c.Name == f.UV {
			t.Fatal("served a half-downloaded model")
		}
	}
}

func TestIndexedShardMissingIsNotAModel(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	writeFile(t, filepath.Join(f.ModelDir, "model.safetensors.index.json"),
		`{"weight_map":{"a":"model-00001-of-00002.safetensors","b":"model-00002-of-00002.safetensors"}}`)
	if o := f.Local.Prepare(); o.Ready || !strings.Contains(o.Reason, "downloading") {
		t.Fatalf("outcome %+v", o)
	}
	writeFile(t, filepath.Join(f.ModelDir, "model-00002-of-00002.safetensors"), "w")
	f.Local.Prepare()
	if last := f.Runner.Detached[len(f.Runner.Detached)-1]; last.Name != f.UV {
		t.Fatalf("complete checkpoint not served: %+v", last)
	}
}

func TestFailedDownloadSpawnIsRetriedNextLaunch(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	os.RemoveAll(f.ModelDir)
	f.Runner.DetachErr = errors.New("fork failed")
	if o := f.Local.Prepare(); o.Ready || !strings.Contains(o.Reason, "did not start") {
		t.Fatalf("outcome %+v", o)
	}
	f.Runner.DetachErr = nil
	f.Local.Prepare()
	if len(f.Runner.Detached) != 1 {
		t.Fatalf("a failed spawn suppressed the retry: %+v", f.Runner.Detached)
	}
}

func TestDryRunProbesButStartsNothing(t *testing.T) {
	f := judgetest.New(t)
	f.Local.DryRun = true
	os.RemoveAll(f.ModelDir)
	f.Server.Down()
	if o := f.Local.Prepare(); o.Ready || !o.Transient {
		t.Fatalf("outcome %+v", o)
	}
	if f.Runner.Calls() != 0 {
		t.Fatalf("dry run started processes: %+v", f.Runner)
	}
	f.Server.Up()
	if o := f.Local.Prepare(); !o.Ready {
		t.Fatalf("dry run with a live judge must show the wiring: %+v", o)
	}
}

func TestLockWaitIsBounded(t *testing.T) {
	f := judgetest.New(t)
	f.Server.Down()
	f.Local.Budget = 400 * time.Millisecond
	lockPath := filepath.Join(f.State, "judge", "start.lock")
	os.MkdirAll(filepath.Dir(lockPath), 0o755)
	held, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	o := f.Local.Prepare()
	if o.Ready || !o.Transient || time.Since(start) > 3*time.Second {
		t.Fatalf("outcome %+v after %s", o, time.Since(start))
	}
	if f.Runner.Calls() != 0 {
		t.Fatal("started a server without the lock")
	}
}

func TestDetectHostFromOverride(t *testing.T) {
	for v, want := range map[string]string{
		"linux/amd64/64": "not macOS", "darwin/amd64/64": "not Apple Silicon",
		"darwin/arm64/8": "8 GiB", "garbage": "not macOS", "darwin/arm64/64": "",
	} {
		h := judge.DetectHost(&sys.Sys{Env: sys.Env{"HARNESS_JUDGE_HOST": v}})
		got := h.Incapable()
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", v, got, want)
		}
	}
}

func TestDescribeExplainsOffAndOn(t *testing.T) {
	f := judgetest.New(t)
	rows := f.Local.Describe()
	if rows[0][0] != "classifier" || !strings.HasPrefix(rows[0][1], "on: jev-9b-ft-v3") {
		t.Fatalf("rows %v", rows)
	}
	f.Local.Host.OS = "linux"
	rows = f.Local.Describe()
	if len(rows) != 1 || !strings.HasPrefix(rows[0][1], "off: linux is not macOS") {
		t.Fatalf("rows %v", rows)
	}
	f = judgetest.New(t)
	os.RemoveAll(f.ModelDir)
	f.Server.SetLoaded(false)
	var model string
	for _, r := range f.Local.Describe() {
		if r[0] == "judge model" {
			model = r[1]
		}
	}
	rows = f.Local.Describe()
	var names []string
	for _, r := range rows {
		names = append(names, r[0])
	}
	if want := []string{"classifier", "judge model", "judge adapter", "judge gate", "judge extension", "judge audit log", "judge logs"}; !slices.Equal(names, want) {
		t.Fatalf("doctor rows %v, want %v", names, want)
	}
	if !strings.Contains(model, "MISSING") || f.Runner.Calls() != 0 {
		t.Fatalf("model row %q, calls %d", model, f.Runner.Calls())
	}
}

func fakeUV(t *testing.T, f *judgetest.Fixture, script string) {
	t.Helper()
	writeFile(t, f.UV, "#!/bin/sh\n"+script)
	os.Chmod(f.UV, 0o755)
}

func TestFetchRunsFetchPy(t *testing.T) {
	f := judgetest.New(t)
	os.RemoveAll(f.ModelDir)
	os.RemoveAll(f.Adapter)
	fakeUV(t, f, `[ "$1" = run ] && [ "$2" = "`+f.Root+`/bench/judge/finetune/fetch.py" ] && [ $# = 2 ] || exit 2
[ "$JUDGE_FT_WORK" = "`+f.Work+`" ] && [ "$HARNESS_STATE" = "`+f.State+`" ] || exit 3
d="$JUDGE_FT_WORK/models/JEV-9B-MLX-NVFP4"; mkdir -p "$d"; echo '{}' > "$d/config.json"; echo w > "$d/model.safetensors"
a="$JUDGE_FT_WORK/runs/v3"; mkdir -p "$a"; echo a > "$a/adapter.safetensors"; echo '{}' > "$a/adapter_config.json"
`)
	var out strings.Builder
	if err := judge.Fetch(f.Local.Sys, f.Root, f.State, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !exists(filepath.Join(f.ModelDir, "config.json")) || !exists(filepath.Join(f.Adapter, "adapter.safetensors")) {
		t.Fatal("model or adapter not on disk after fetch")
	}
	out.Reset()
	fakeUV(t, f, "exit 9\n") // both present: nothing runs
	if err := judge.Fetch(f.Local.Sys, f.Root, f.State, &out); err != nil || !strings.Contains(out.String(), "already present") {
		t.Fatalf("%v %q", err, out.String())
	}
	os.RemoveAll(f.Adapter) // a model alone is not enough: the adapter is fetched too
	if err := judge.Fetch(f.Local.Sys, f.Root, f.State, &out); err == nil {
		t.Fatal("fetch skipped a missing adapter")
	}
}

func TestFetchFailuresAreErrors(t *testing.T) {
	f := judgetest.New(t)
	os.RemoveAll(f.ModelDir)
	fakeUV(t, f, "exit 1\n")
	if err := judge.Fetch(f.Local.Sys, f.Root, f.State, &strings.Builder{}); err == nil {
		t.Fatal("failed fetch reported success")
	}
	fakeUV(t, f, "exit 0\n") // "succeeds" but leaves no model
	if err := judge.Fetch(f.Local.Sys, f.Root, f.State, &strings.Builder{}); err == nil {
		t.Fatal("empty fetch reported success")
	}
	f.Local.Sys.Env["HARNESS_UV_BIN"] = "/nonexistent/uv"
	if err := judge.Fetch(f.Local.Sys, f.Root, f.State, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "uv not installed") {
		t.Fatalf("err %v", err)
	}
}

func TestSecondFetchYieldsToARunningOne(t *testing.T) {
	f := judgetest.New(t)
	os.RemoveAll(f.ModelDir)
	marker := filepath.Join(t.TempDir(), "ran")
	fakeUV(t, f, "touch "+marker+"\n")
	lockPath := filepath.Join(f.State, "judge", "fetch.lock")
	os.MkdirAll(filepath.Dir(lockPath), 0o755)
	held, _ := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	defer held.Close()
	syscall.Flock(int(held.Fd()), syscall.LOCK_EX)
	var out strings.Builder
	if err := judge.Fetch(f.Local.Sys, f.Root, f.State, &out); err != nil {
		t.Fatal(err)
	}
	if exists(marker) || !strings.Contains(out.String(), "another download") {
		t.Fatalf("second download ran: %q", out.String())
	}
}

// train = fetch, build_data, train.py with the v3 recipe, in that order, streaming the output.
func TestTrainRunsTheV3RecipeAndRefusesToReplace(t *testing.T) {
	f := judgetest.New(t)
	os.RemoveAll(f.Adapter)
	log := filepath.Join(t.TempDir(), "calls")
	fakeUV(t, f, `echo "$@" >> `+log+`
case "$2" in *train.py) mkdir -p "$JUDGE_FT_WORK/runs/v3"; echo a > "$JUDGE_FT_WORK/runs/v3/adapter.safetensors"; echo '{}' > "$JUDGE_FT_WORK/runs/v3/adapter_config.json";; esac
echo "step $2"
`)
	var out strings.Builder
	if err := judge.Train(f.Local.Sys, f.Root, f.State, false, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	b, _ := os.ReadFile(log)
	ft := f.Root + "/bench/judge/finetune/"
	want := "run " + ft + "build_data.py\nrun " + ft + "train.py --epochs 1.0 --lr 1e-4 --neg-per-row 1 --lora-layers 12 " +
		"--eval-every 90 --eval-rows 200 --no-final --max-minutes 75\n"
	if string(b) != want { // the model is present, so fetch is a no-op
		t.Fatalf("calls:\n%s\nwant:\n%s", b, want)
	}
	if !strings.Contains(out.String(), "step "+ft+"train.py") {
		t.Fatalf("output not streamed: %q", out.String())
	}
	os.Remove(log)
	if err := judge.Train(f.Local.Sys, f.Root, f.State, false, &out); err == nil || !strings.Contains(err.Error(), "--force") || exists(log) {
		t.Fatalf("existing adapter replaced without --force: %v", err)
	}
	if err := judge.Train(f.Local.Sys, f.Root, f.State, true, &out); err != nil || !exists(log) {
		t.Fatalf("--force did not retrain: %v", err)
	}
}

func TestTrainFetchesFirstAndStopsOnFailure(t *testing.T) {
	f := judgetest.New(t)
	os.RemoveAll(f.Adapter)
	os.RemoveAll(f.ModelDir)
	log := filepath.Join(t.TempDir(), "calls")
	fakeUV(t, f, `echo "$@" >> `+log+`
case "$2" in *fetch.py) d="$JUDGE_FT_WORK/models/JEV-9B-MLX-NVFP4"; mkdir -p "$d"; echo '{}' > "$d/config.json"; echo w > "$d/model.safetensors";;
*build_data.py) exit 1;; esac
`)
	err := judge.Train(f.Local.Sys, f.Root, f.State, false, &strings.Builder{})
	b, _ := os.ReadFile(log)
	if err == nil || !strings.Contains(err.Error(), "build_data.py") || strings.Count(string(b), "\n") != 2 || strings.Contains(string(b), "train.py") ||
		!strings.HasPrefix(string(b), "run "+f.Root+"/bench/judge/finetune/fetch.py --no-adapter\n") {
		t.Fatalf("err %v calls %q", err, b)
	}
}

// The gate gives a decision GateBudget and then blocks the call, so a judge that is up
// but cannot answer a battery-sized request quickly must keep the classifier off.
func TestSlowLoadedJudgeKeepsTheClassifierOff(t *testing.T) {
	f := judgetest.New(t)
	f.Local.Fast = 150 * time.Millisecond
	f.Server.SetDelay(400 * time.Millisecond)
	o := f.Local.Prepare()
	if o.Ready || !o.Transient || !strings.Contains(o.Reason, "too slow") {
		t.Fatalf("outcome %+v", o)
	}
	if f.Runner.Calls() != 0 {
		t.Fatalf("a slow warm judge must not be restarted: calls=%d", f.Runner.Calls())
	}
}

func TestUnansweredBatteryOnALoadedModelIsTooSlow(t *testing.T) {
	f := judgetest.New(t)
	f.Local.Timeout = 200 * time.Millisecond
	f.Server.SetDelay(time.Second)
	if o := f.Local.Prepare(); o.Ready || !strings.Contains(o.Reason, "too slow") || len(f.Runner.Detached) != 0 {
		t.Fatalf("outcome %+v detached %v", o, f.Runner.Detached)
	}
}

func TestForeignPortFailsFast(t *testing.T) {
	f := judgetest.New(t)
	f.Local.Budget = 20 * time.Second
	f.Server.SetNotJudge(true)
	start := time.Now()
	o := f.Local.Prepare()
	if o.Ready || !strings.Contains(o.Reason, "does not serve a model list") || time.Since(start) > 3*time.Second {
		t.Fatalf("outcome %+v after %s", o, time.Since(start))
	}
}

// The first battery after a fresh start also pays kernel warm-up, so one slow answer is tolerated
// there; the same slowness twice is the real speed.
func TestFreshServerGetsOneSlowAnswer(t *testing.T) {
	boot := func(f *judgetest.Fixture, settle time.Duration) {
		f.Local.Fast = 200 * time.Millisecond
		f.Server.Down()
		f.Runner.OnDetach = func(judgetest.Call) {
			f.Server.SetDelay(400 * time.Millisecond)
			f.Server.Up()
			go func() { time.Sleep(settle); f.Server.SetDelay(0) }()
		}
	}
	f := judgetest.New(t)
	boot(f, 500*time.Millisecond)
	if o := f.Local.Prepare(); !o.Ready {
		t.Fatalf("warm-up answer rejected: %+v (%s)", o, f.Log.String())
	}
	f = judgetest.New(t)
	boot(f, time.Hour)
	if o := f.Local.Prepare(); o.Ready || !strings.Contains(o.Reason, "too slow") {
		t.Fatalf("a persistently slow judge was enabled: %+v", o)
	}
}
