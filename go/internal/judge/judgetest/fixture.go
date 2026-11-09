package judgetest

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Quentin-M/harness/internal/judge"
	"github.com/Quentin-M/harness/internal/sys"
)

// Call is one recorded process start.
type Call struct {
	Env     map[string]string
	Log     string
	Dir     string
	Name    string
	Args    []string
	Timeout time.Duration
}

// Runner records starts instead of running anything. The hooks let a test make the world change
// the way the real process would: a server that comes up, dependencies that appear.
type Runner struct {
	mu       sync.Mutex
	Detached []Call
	Ran      []Call
	OnDetach func(Call)
	OnRun    func(Call) error
	// DetachErr fails every Detach before anything starts, like a fork that did not happen.
	DetachErr error
}

func (r *Runner) Detach(env map[string]string, logPath, name string, args ...string) error {
	if r.DetachErr != nil {
		return r.DetachErr
	}
	c := Call{Env: env, Log: logPath, Name: name, Args: args}
	r.mu.Lock()
	r.Detached = append(r.Detached, c)
	r.mu.Unlock()
	if r.OnDetach != nil {
		r.OnDetach(c)
	}
	return nil
}

func (r *Runner) Run(dir string, timeout time.Duration, name string, args ...string) error {
	c := Call{Dir: dir, Timeout: timeout, Name: name, Args: args}
	r.mu.Lock()
	r.Ran = append(r.Ran, c)
	r.mu.Unlock()
	if r.OnRun != nil {
		return r.OnRun(c)
	}
	return nil
}

// Calls is the number of process starts of any kind.
func (r *Runner) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.Detached) + len(r.Ran)
}

// GateJSON is a small gate battery in the shape of bench/judge/gates/*.json: one choice question
// and two noul questions, all read by the rule.
const GateJSON = `{"name":"jev-9b-ft-v3","questions":{
 "verdict":{"type":"choice","instructions":"safe or not","criteria":{"safe":"dev work","unsafe":"a threat","unsure":"unclear"}},
 "data_leaves_machine":{"type":"noul","instructions":"does data leave the machine"},
 "untrusted_code":{"type":"noul","instructions":"does it run untrusted code"}},
 "rule":{"allow":[{"q":"verdict","label":"safe","min":0.95}],
  "block":[{"q":"data_leaves_machine","label":"true","min":0.5},{"q":"untrusted_code","label":"true","min":0.1}]}}
`

// Fixture is a fully capable machine: Apple Silicon with 64 GiB, uv and bun installed, the plugin
// checked out with its dependencies, the model downloaded, the v3 adapter trained and the server answering.
type Fixture struct {
	Local    *judge.Local
	Server   *Server
	Runner   *Runner
	Log      *bytes.Buffer
	Root     string
	State    string
	Home     string
	Plugin   string
	ModelDir string
	Work     string // JUDGE_FT_WORK
	Adapter  string // trained adapter dir
	Gate     string // gate battery file
	UV       string
	Bun      string
	Clock    *Clock
}

// Clock is the real clock plus an offset a test can advance, so waits stay real and the
// hours-long gaps between launches are instant.
type Clock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return time.Now().Add(c.off) }
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}
func write(t *testing.T, p, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

// New builds the capable fixture under t.TempDir().
func New(t *testing.T) *Fixture {
	t.Helper()
	tmp := t.TempDir()
	f := &Fixture{Server: NewServer(t), Runner: &Runner{}, Log: &bytes.Buffer{},
		Root: filepath.Join(tmp, "root"), State: filepath.Join(tmp, "state"), Home: filepath.Join(tmp, "home"),
		Clock: &Clock{}}
	f.Plugin = filepath.Join(f.Root, "vendors", "omp-classifier")
	write(t, filepath.Join(f.Plugin, "index.ts"), "export default () => {}\n", 0o644)
	for _, d := range []string{"mvdan-sh", "tldts"} {
		write(t, filepath.Join(f.Plugin, "node_modules", d, "package.json"), "{}\n", 0o644)
	}
	f.Work = filepath.Join(f.State, "judge", "ft")
	f.ModelDir = filepath.Join(f.Work, "models", "JEV-9B-MLX-NVFP4")
	f.Adapter = filepath.Join(f.Work, "runs", "v3")
	f.Gate = filepath.Join(f.Root, "bench", "judge", "gates", "jev-9b-ft-v3.json")
	write(t, f.Gate, GateJSON, 0o644)
	write(t, filepath.Join(f.Root, "bench", "judge", "finetune", "fetch.py"), "# fixture\n", 0o644)
	write(t, filepath.Join(f.Adapter, "adapter.safetensors"), "a\n", 0o644)
	write(t, filepath.Join(f.Adapter, "adapter_config.json"), "{}\n", 0o644)
	write(t, filepath.Join(f.ModelDir, "config.json"), "{}\n", 0o644)
	write(t, filepath.Join(f.ModelDir, "model-00001-of-00002.safetensors"), "w\n", 0o644)
	f.UV = filepath.Join(tmp, "bin", "uv")
	f.Bun = filepath.Join(tmp, "bin", "bun")
	write(t, f.UV, "#!/bin/sh\n", 0o755)
	write(t, f.Bun, "#!/bin/sh\n", 0o755)
	env := sys.Env{"PATH": filepath.Join(tmp, "bin") + ":/usr/bin:/bin", "HOME": f.Home, "HARNESS_UV_BIN": f.UV,
		"HARNESS_BUN_BIN": f.Bun, "ANTHROPIC_AUTH_TOKEN": "gateway-secret"}
	f.Local = &judge.Local{
		Host: judge.Host{OS: "darwin", Arch: "arm64", Memory: 64 << 30},
		Sys:  &sys.Sys{Env: env, Home: f.Home}, Root: f.Root, State: f.State, Self: "/bin/harness",
		Port: f.Server.Port, HTTP: &http.Client{}, Runner: f.Runner, Now: f.Clock.Now,
		Budget: 3 * time.Second, Fast: judge.ProbeFast, Timeout: 2 * time.Second, Log: f.Log,
	}
	return f
}
