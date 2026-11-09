// Package judge decides whether a launch gets omp-classifier, and proves its local judge works first.
//
// omp-classifier blocks every bash call when its judge cannot answer, so the classifier is only
// wired in when the fine-tuned judge on loopback has just answered the gate battery's real
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
	// KeyEnv names the env var the plugin sends as the Bearer key. The adapter server skips auth on
	// loopback, so the value is a placeholder, but the plugin refuses an endpoint whose key var is unset.
	KeyEnv   = "HARNESS_JUDGE_KEY"
	keyValue = "loopback"
	// ConfigEnv points the plugin at the harness-owned config file, whatever XDG or profile says.
	ConfigEnv = "OMP_JEV_CONFIG"

	// MinMemory is the RAM a Mac needs to keep the 9B NVFP4 model resident next to an agent session.
	MinMemory = 32 << 30
	// ReadyBudget bounds everything a launch waits for: the lock, bun install and a cold model load.
	ReadyBudget = 30 * time.Second
	// ProbeTimeout is the plugin's own default classification budget: a judge slower than this
	// would fail closed on its first real command.
	ProbeTimeout = 8 * time.Second
	// ProbeFast is the longest a battery-sized request may take on a loaded model: the plugin
	// aborts a classification at 8 s, and its two requests run in parallel.
	ProbeFast = 5 * time.Second
	// FetchGap spaces download attempts across launches.
	FetchGap = time.Hour
	// IdleExit makes the server exit after a quiet working day, freeing the GPU memory; the next
	// launch starts it again.
	IdleExit = 6 * time.Hour

	pollEvery = 250 * time.Millisecond
	depsTime  = 60 * time.Second
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
	Root   string // harness checkout: the plugin lives in Root/vendors/omp-classifier
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

// New builds the real Local for a launch from the environment in s.
func New(s *sys.Sys, root, state string, dryRun bool, log io.Writer) *Local {
	port := DefaultPort
	if v := s.Env.Get("HARNESS_JUDGE_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			port = n
		}
	}
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
	// Extension is the plugin directory for omp's -e flag; Env is added to the launch env.
	Extension string
	Env       map[string]string
}

func (l *Local) PluginDir() string { return filepath.Join(l.Root, "vendors", "omp-classifier") }

// ConfigPath is the plugin's omp-classifier.json. Its directory also receives the plugin's audit log.
func ConfigPath(state string) string {
	return filepath.Join(state, "omp", "omp-classifier", "omp-classifier.json")
}

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

// GatePath is the battery the plugin decides by (its `battery` config key) and the probe asks.
func (l *Local) GatePath() string {
	return filepath.Join(l.Root, "bench", "judge", "gates", ModelID+".json")
}

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
	if !isFile(filepath.Join(l.PluginDir(), "index.ts")) {
		return "vendors/omp-classifier is not checked out", ""
	}
	if !l.depsPresent() && l.bin("bun") == "" {
		return "bun not installed, plugin dependencies missing", ""
	}
	if _, err := l.loadGate(); err != nil {
		return err.Error(), ""
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
	if err != nil || l.depsPresent() {
		if o, done := l.conclude(model, err); done {
			return o
		}
	}
	if l.DryRun {
		if err == nil {
			err = errors.New("plugin dependencies are not installed")
		}
		return l.soon("not ready (%v; a dry run starts and installs nothing)", err)
	}
	deadline := l.Now().Add(l.Budget)
	lock, lockErr := lockUntil(l.dir("start.lock"), deadline, l.Now)
	if lockErr != nil {
		return l.soon("another launch is still starting it")
	}
	defer lock.Close()
	if !l.depsPresent() {
		if err := l.Runner.Run(l.PluginDir(), min(depsTime, deadline.Sub(l.Now())), l.bin("bun"),
			"install", "--frozen-lockfile", "--production"); err != nil || !l.depsPresent() {
			return l.soon("plugin dependencies did not install (%v)", err)
		}
	}
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

// enable wires the plugin: it owns judgeBackend, battery and hardBlocks in the config and nothing else.
func (l *Local) enable(model string) Outcome {
	if err := l.syncConfig(model); err != nil {
		return l.soon("%v", err)
	}
	return Outcome{
		Ready: true, Reason: model + " on " + l.baseURL(), Extension: l.PluginDir(),
		Env: map[string]string{ConfigEnv: ConfigPath(l.State), KeyEnv: keyValue},
	}
}

func (l *Local) depsPresent() bool {
	for _, d := range []string{"mvdan-sh", "tldts"} {
		if !isFile(filepath.Join(l.PluginDir(), "node_modules", d, "package.json")) {
			return false
		}
	}
	return true
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
