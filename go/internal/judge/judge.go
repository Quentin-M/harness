// Package judge decides whether a launch gets the command gate, proves its local judge works first,
// and is the gate itself (`claude judge gate`).
//
// The gate blocks every bash and eval call when its judge cannot answer, so the extension is only
// loaded when the fine-tuned judge on loopback has just answered the gate battery's real
// questions. Anything less (wrong host, missing runtime, model still downloading, adapter not
// trained, server not ready) means no classifier, which is omp's default, and the launch goes on.
package judge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Quentin-M/harness/internal/sys"
)

const (
	// ModelID is the id the adapter server serves: JEV-9B-MLX-NVFP4 plus the v3 LoRA adapter.
	ModelID = "jev-9b-ft-v3"
	// ModelName is the base model's directory under the work dir's models/.
	ModelName = "JEV-9B-MLX-NVFP4"
	// AdapterRun is the training run whose adapter is served: <work>/runs/<AdapterRun>.
	AdapterRun = "v3"
	// DefaultPort is the loopback port of the shared judge server.
	DefaultPort = 11436
	// keyValue is the placeholder Bearer key of the probe; the adapter server skips auth on loopback.
	keyValue = "loopback"

	// MinMemory is the RAM a Mac needs to keep the 9B NVFP4 model resident next to an agent session.
	MinMemory = 32 << 30
	// ReadyBudget bounds everything a launch waits for: the lock and a cold model load.
	ReadyBudget = 30 * time.Second
	// ProbeTimeout is the extension's own timeout per decision: a judge slower than this would
	// fail closed on its first real command.
	ProbeTimeout = 8 * time.Second
	// ProbeFast is the longest a battery-sized request may take on a loaded model: a decision
	// is cut off at GateBudget, and the request carries user messages and a command on top.
	ProbeFast = 5 * time.Second
	// FetchGap spaces download attempts across launches.
	FetchGap = time.Hour
	// IdleExit makes the server exit after a quiet working day, freeing the GPU memory; the next
	// launch starts it again.
	IdleExit = 6 * time.Hour

	pollEvery = 250 * time.Millisecond
)

// Runner starts processes. The real one execs; tests record and fake the server coming up.
type Runner interface {
	// Detach starts name in its own session with output appended to logPath, and returns at once.
	Detach(env map[string]string, logPath, name string, args ...string) error
	// Run runs name in dir to completion or until timeout.
	Run(dir string, timeout time.Duration, name string, args ...string) error
}

// Local is the launch-time view of the local judge.
type Local struct {
	Host   Host
	Sys    *sys.Sys
	Root   string // harness checkout: the gate battery, extension and adapter script live under it
	State  string
	Self   string // harness binary, which serves `judge fetch`
	Off    bool   // HARNESS_JUDGE=off
	DryRun bool   // probe only: no process is started, nothing is downloaded or installed
	Port   int
	// ModelDir is where the model is expected; "" means HARNESS_JUDGE_MODEL_DIR, else
	// <State>/judge/ft/models/JEV-9B-MLX-NVFP4.
	ModelDir string
	HTTP     *http.Client
	Runner   Runner
	Now      func() time.Time
	Budget   time.Duration
	Fast     time.Duration // ProbeFast; tests shorten it
	Timeout  time.Duration // how long one readiness check may take; ProbeTimeout
	Log      io.Writer     // one-line notes about transient reasons (download running, server slow)
}

// PortFrom is the judge's loopback port: HARNESS_JUDGE_PORT when it is a valid port, else DefaultPort.
func PortFrom(env sys.Env) int {
	if n, err := strconv.Atoi(env.Get("HARNESS_JUDGE_PORT")); err == nil && n > 0 && n < 65536 {
		return n
	}
	return DefaultPort
}

// New builds the real Local for a launch from the environment in s.
func New(s *sys.Sys, root, state string, dryRun bool, log io.Writer) *Local {
	port := PortFrom(s.Env)
	self := s.Self
	if self == "" {
		self, _ = os.Executable()
	}
	return &Local{
		Host: DetectHost(s), Sys: s, Root: root, State: state, Self: self, DryRun: dryRun, Port: port,
		Off:  s.Env.Get("HARNESS_JUDGE") == "off",
		HTTP: &http.Client{}, Runner: ExecRunner{}, Now: time.Now, Budget: ReadyBudget, Fast: ProbeFast, Timeout: ProbeTimeout, Log: log,
	}
}

// Outcome is what a launch does with the classifier.
type Outcome struct {
	Ready bool
	// Reason says why not (or, when ready, which model answered). Transient reasons are the ones a
	// later launch can fix on its own, and they get a note on stderr.
	Reason    string
	Transient bool
	// Extension is the file for omp's -e flag; Env is added to the launch env.
	Extension string
	Env       map[string]string
}

// ExtensionPath is the omp extension that forwards bash and eval calls to `judge gate`.
func (l *Local) ExtensionPath() string { return ExtensionPathIn(l.Root) }

func (l *Local) dir(name string) string { return filepath.Join(l.State, "judge", name) }

// workDir is the fine-tune work dir ($JUDGE_FT_WORK for every python child).
func (l *Local) workDir() string { return l.dir("ft") }

func (l *Local) modelDir() string {
	switch {
	case l.ModelDir != "":
		return l.ModelDir
	case l.Sys.Env.Get("HARNESS_JUDGE_MODEL_DIR") != "":
		return l.Sys.Env.Get("HARNESS_JUDGE_MODEL_DIR")
	}
	return filepath.Join(l.workDir(), "models", ModelName)
}

func (l *Local) adapterDir() string { return filepath.Join(l.workDir(), "runs", AdapterRun) }

// adapterPresent is a finished training run: the weights and their config.
func (l *Local) adapterPresent() bool {
	return isFile(filepath.Join(l.adapterDir(), "adapter.safetensors")) &&
		isFile(filepath.Join(l.adapterDir(), "adapter_config.json"))
}

// scripts are the python entry points under Root/bench/judge.
func (l *Local) finetuneDir() string { return filepath.Join(l.Root, "bench", "judge", "finetune") }
func (l *Local) adapterScript() string {
	return filepath.Join(l.Root, "bench", "judge", "adapters", "jev_systemone.py")
}

// GatePath is the battery the gate decides by and the probe asks.
func (l *Local) GatePath() string { return GatePathIn(l.Root) }

func (l *Local) baseURL() string { return "http://127.0.0.1:" + strconv.Itoa(l.Port) }

func (l *Local) note(format string, a ...any) {
	if l.Log != nil {
		fmt.Fprintf(l.Log, "harness: local judge: "+format+"; launching without the classifier\n", a...)
	}
}

func no(reason string) Outcome { return Outcome{Reason: reason} }

func (l *Local) soon(format string, a ...any) Outcome {
	o := Outcome{Reason: fmt.Sprintf(format, a...), Transient: true}
	l.note("%s", o.Reason)
	return o
}

// bin is an installed binary, or "": HARNESS_<NAME>_BIN may name one that is not there.
func (l *Local) bin(name string) string {
	if p := l.Sys.FindBin(name); isFile(p) {
		return p
	}
	return ""
}

// static returns why this host or checkout can never run the judge, or "". bin is uv.
func (l *Local) static() (reason, bin string) {
	if l.Off {
		return "disabled by HARNESS_JUDGE=off", ""
	}
	if r := l.Host.Incapable(); r != "" {
		return r, ""
	}
	bin = l.bin("uv")
	if bin == "" {
		return "uv not installed", ""
	}
	if _, err := l.loadGate(); err != nil {
		return err.Error(), ""
	}
	if !isFile(l.ExtensionPath()) {
		return "extension " + l.ExtensionPath() + " is missing", ""
	}
	return "", bin
}

// conclude ends the wait when err is final: the judge answered, answered too slowly, or is not a
// judge at all. Anything else (nothing listening yet, model still loading) is worth waiting on.
func (l *Local) conclude(model string, err error) (Outcome, bool) {
	switch {
	case err == nil:
		return l.enable(model), true
	case errors.Is(err, errSlow):
		return l.soon("%v; the GPU may be busy, the next launch tries again", err), true
	case errors.Is(err, errForeign):
		return l.soon("%v", err), true
	}
	return Outcome{}, false
}

// Prepare decides the classifier for this launch. It never fails the launch.
func (l *Local) Prepare() Outcome {
	reason, bin := l.static()
	if reason != "" {
		return no(reason)
	}
	model, err := l.ready(l.Timeout)
	if o, done := l.conclude(model, err); done {
		return o
	}
	if l.DryRun {
		return l.soon("not ready (%v; a dry run starts nothing)", err)
	}
	deadline := l.Now().Add(l.Budget)
	lock, lockErr := lockUntil(l.dir("start.lock"), deadline, l.Now)
	if lockErr != nil {
		return l.soon("another launch is still starting it")
	}
	defer lock.Close()
	// a concurrent launch may have started it while we waited
	model, err = l.ready(l.Timeout)
	if o, done := l.conclude(model, err); done {
		return o
	}
	started := false
	if errors.Is(err, errDown) {
		if !modelPresent(l.modelDir()) || !l.adapterPresent() {
			if err := l.fetch(); err != nil {
				return l.soon("model %s or its adapter is missing and the download did not start (%v)", ModelName, err)
			}
			return l.soon("model %s and its adapter are downloading in the background (log %s)", ModelName, l.dir("fetch.log"))
		}
		env := childEnv(l.Sys.Env)
		env["JUDGE_FT_WORK"], env["HARNESS_STATE"] = l.workDir(), l.State
		if err := l.Runner.Detach(env, l.dir("server.log"), bin,
			ServeArgs(l.adapterScript(), l.modelDir(), l.Port, l.adapterDir(), IdleExit)...); err != nil {
			return l.soon("could not start the judge server (%v)", err)
		}
		started = true
	}
	for slowOnce := started; l.Now().Before(deadline); {
		model, err = l.ready(max(time.Second, min(l.Timeout, deadline.Sub(l.Now()))))
		if errors.Is(err, errSlow) && slowOnce {
			// the first battery after a fresh start also pays kernel warm-up; only a repeat is the speed
			slowOnce = false
			err = errors.New("warming up")
		}
		if o, done := l.conclude(model, err); done {
			return o
		}
		time.Sleep(pollEvery)
	}
	return l.soon("the judge server did not become ready in %s (%v; the first start installs mlx-vlm and may take minutes, the server keeps going; log %s)",
		l.Budget, err, l.dir("server.log"))
}

// enable wires the gate: the extension for -e, and the env it and `judge gate` read. HARNESS_ROOT,
// HARNESS_STATE and HARNESS_JUDGE_PORT pin the gate to the checkout, state dir and server this
// launch just probed, whatever the session's own shell says.
func (l *Local) enable(model string) Outcome {
	return Outcome{
		Ready: true, Reason: model + " on " + l.baseURL(), Extension: l.ExtensionPath(),
		Env: map[string]string{GateEnv: l.Self, "HARNESS_ROOT": l.Root, "HARNESS_STATE": l.State,
			"HARNESS_JUDGE_PORT": strconv.Itoa(l.Port)},
	}
}

// ServeArgs is the uv command line of the adapter server. The script binds 127.0.0.1 only, after
// the model is loaded, so a listening port is a loaded judge.
func ServeArgs(script, modelDir string, port int, adapterDir string, idleExit time.Duration) []string {
	return []string{"run", "--script", script, modelDir, strconv.Itoa(port), adapterDir,
		"--model-id", ModelID, "--idle-exit-secs", strconv.Itoa(int(idleExit.Seconds()))}
}

// childEnv is the minimal environment of a detached judge process: it must not inherit gateway keys.
func childEnv(env sys.Env) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		switch {
		case k == "HOME", k == "PATH", k == "TMPDIR", k == "USER", k == "LANG", k == "NO_PROXY", k == "SSL_CERT_FILE",
			strings.HasPrefix(k, "UV_"), strings.HasPrefix(k, "HF_"), strings.HasSuffix(strings.ToUpper(k), "_PROXY"),
			strings.HasPrefix(k, "HARNESS_JUDGE"):
			out[k] = v
		}
	}
	return out
}

// modelPresent is a finished download: a config, no .partial file, and every shard the weight index
// names (or a lone safetensors file when there is no index).
func modelPresent(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	var config, weights bool
	for _, e := range ents {
		switch n := e.Name(); {
		case strings.HasSuffix(n, ".partial"):
			return false
		case n == "config.json":
			config = true
		case strings.HasSuffix(n, ".safetensors"):
			weights = true
		}
	}
	if !config || !weights {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "model.safetensors.index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var index struct {
		WeightMap map[string]string `json:"weight_map"`
	}
	if err != nil || json.Unmarshal(data, &index) != nil || len(index.WeightMap) == 0 {
		return false
	}
	for _, shard := range index.WeightMap {
		if !isFile(filepath.Join(dir, shard)) {
			return false
		}
	}
	return true
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// lockUntil takes an exclusive flock on p, polling until deadline.
func lockUntil(p string, deadline time.Time, now func() time.Time) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || !now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path.Base(p), err)
		}
		time.Sleep(pollEvery)
	}
}
