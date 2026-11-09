package judge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Quentin-M/harness/internal/sys"
)

// ExecRunner is the real Runner.
type ExecRunner struct{}

func (ExecRunner) Detach(env map[string]string, logPath, name string, args ...string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(name, args...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	sort.Strings(cmd.Env)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (ExecRunner) Run(dir string, timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// fetch starts `harness judge fetch` detached, at most once per FetchGap. The caller holds
// start.lock, so launches do not race on the stamp. The stamp is only kept once the spawn worked.
func (l *Local) fetch() error {
	stamp := l.dir("fetch.stamp")
	if fi, err := os.Stat(stamp); err == nil && l.Now().Sub(fi.ModTime()) < FetchGap {
		return nil
	}
	env := childEnv(l.Sys.Env)
	env["HARNESS_STATE"], env["HARNESS_ROOT"] = l.State, l.Root
	if err := l.Runner.Detach(env, l.dir("fetch.log"), l.Self, "judge", "fetch"); err != nil {
		return err
	}
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		return err
	}
	return os.Chtimes(stamp, l.Now(), l.Now())
}

// pyCmd is `uv run <script> args...` in the finetune dir, with the work dir every script resolves
// its paths from. Output streams to out.
func (l *Local) pyCmd(out io.Writer, script string, args ...string) *exec.Cmd {
	uv := l.Sys.FindBin("uv")
	cmd := exec.Command(uv, append([]string{"run", filepath.Join(l.finetuneDir(), script)}, args...)...)
	cmd.Dir = l.finetuneDir()
	env := l.Sys.Env.Clone()
	env.Set("JUDGE_FT_WORK", l.workDir())
	env.Set("HARNESS_STATE", l.State)
	if m := env.Get("HARNESS_JUDGE_MODEL_DIR"); m != "" {
		env.Set("JUDGE_FT_MODEL", m)
	}
	cmd.Env = env.Environ()
	cmd.Stdout, cmd.Stderr = out, out
	return cmd
}

func (l *Local) needUV() error {
	if bin := l.Sys.FindBin("uv"); bin == "" || !isFile(bin) {
		return errors.New("uv not installed")
	}
	return nil
}

func localFor(s *sys.Sys, root, state string) *Local {
	return &Local{Sys: s, Root: root, State: state, Now: time.Now}
}

// Fetch downloads the pinned training inputs, the base model and the published adapter with
// `uv run fetch.py`, in the foreground. It is what the detached `judge fetch` runs; a second copy
// exits at once while one holds the lock.
func Fetch(s *sys.Sys, root, state string, out io.Writer) error {
	return localFor(s, root, state).fetch1(out, true)
}

// fetch1 runs fetch.py; without adapter it skips the published adapter, for a local training run.
func (l *Local) fetch1(out io.Writer, adapter bool) error {
	lock, err := lockUntil(l.dir("fetch.lock"), time.Time{}, time.Now)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		fmt.Fprintln(out, "judge fetch: another download is running")
		return nil
	}
	if err != nil {
		return fmt.Errorf("judge fetch: %w", err)
	}
	defer lock.Close()
	done := func() bool { return modelPresent(l.modelDir()) && (!adapter || l.adapterPresent()) }
	if done() {
		fmt.Fprintln(out, "judge fetch: already present at", l.workDir())
		return nil
	}
	if err := l.needUV(); err != nil {
		return fmt.Errorf("judge fetch: %w", err)
	}
	var args []string
	if !adapter {
		args = []string{"--no-adapter"}
	}
	if err := l.pyCmd(out, "fetch.py", args...).Run(); err != nil {
		return fmt.Errorf("judge fetch: fetch.py: %w", err)
	}
	if !done() {
		return fmt.Errorf("judge fetch: fetch.py finished but the model or adapter is missing in %s", l.workDir())
	}
	fmt.Fprintln(out, "judge fetch: ready at", l.workDir())
	return nil
}

// trainArgs is the v3 recipe of bench/judge/finetune.md; the run lands in <work>/runs/v3.
var trainArgs = []string{"--epochs", "1.0", "--lr", "1e-4", "--neg-per-row", "1", "--lora-layers", "12",
	"--eval-every", "90", "--eval-rows", "200", "--no-final", "--max-minutes", "75"}

// Train fetches the inputs, builds the data and trains the v3 adapter locally in the foreground
// (about 75 minutes on the GPU), in place of the published one. It refuses to replace an existing
// adapter unless force. A local run keeps the gate's thresholds, which were tuned on the published
// adapter; bench/judge/finetune.md#gate says how to retune them.
func Train(s *sys.Sys, root, state string, force bool, out io.Writer) error {
	l := localFor(s, root, state)
	if l.adapterPresent() && !force {
		return fmt.Errorf("judge train: adapter already exists at %s (--force retrains it)", l.adapterDir())
	}
	if err := l.needUV(); err != nil {
		return fmt.Errorf("judge train: %w", err)
	}
	lock, err := lockUntil(l.dir("train.lock"), time.Time{}, time.Now)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errors.New("judge train: another training run is in progress")
	}
	if err != nil {
		return fmt.Errorf("judge train: %w", err)
	}
	defer lock.Close()
	if err := l.fetch1(out, false); err != nil {
		return err
	}
	if !modelPresent(l.modelDir()) {
		return errors.New("judge train: the model is being downloaded by another process; retry when it is done")
	}
	for _, step := range []struct {
		script string
		args   []string
	}{{"build_data.py", nil}, {"train.py", trainArgs}} {
		fmt.Fprintf(out, "judge train: uv run %s %s\n", step.script, strings.Join(step.args, " "))
		if err := l.pyCmd(out, step.script, step.args...).Run(); err != nil {
			return fmt.Errorf("judge train: %s: %w", step.script, err)
		}
	}
	if !l.adapterPresent() {
		return fmt.Errorf("judge train: train.py finished but no adapter at %s", l.adapterDir())
	}
	fmt.Fprintln(out, "judge train: adapter ready at", l.adapterDir())
	return nil
}
