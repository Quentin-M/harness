// Package sys wraps the launch environment, binary lookup and subprocesses.
package sys

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Env is a process environment as a map; harnesses mutate a clone for launch.
type Env map[string]string

// OSEnv snapshots the current process environment.
func OSEnv() Env {
	e := Env{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			e[k] = v
		}
	}
	return e
}

func (e Env) Get(k string) string            { return e[k] }
func (e Env) Lookup(k string) (string, bool) { v, ok := e[k]; return v, ok }
func (e Env) Set(k, v string)                { e[k] = v }
func (e Env) Unset(k string)                 { delete(e, k) }
func (e Env) SetDefault(k, v string) {
	if _, ok := e[k]; !ok {
		e[k] = v
	}
}

func (e Env) Clone() Env {
	out := make(Env, len(e))
	for k, v := range e {
		out[k] = v
	}
	return out
}

// Environ is the env as sorted KEY=VALUE pairs, for exec.
func (e Env) Environ() []string {
	out := make([]string, 0, len(e))
	for k, v := range e {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// Sys is the machine as the launcher sees it: env (for PATH and overrides) and the home dir.
type Sys struct {
	Env    Env
	Home   string
	Stderr io.Writer // nil means os.Stderr
	Self   string    // the harness binary; "" means os.Executable
}

// New builds a Sys whose home is env HOME, falling back to the OS user home.
func New(env Env) *Sys {
	home := env.Get("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &Sys{Env: env, Home: home}
}

func (s *Sys) stderr() io.Writer {
	if s.Stderr != nil {
		return s.Stderr
	}
	return os.Stderr
}

var harnessShebang = regexp.MustCompile(`(?m)(^|[\s/"'])harness(\s|["']|$)|go/cmd/claude/main\.sh`)

func (s *Sys) self() string {
	p := s.Self
	if p == "" {
		p, _ = os.Executable()
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// pointsAtHarness spots shims that would exec harness again (alias wrappers, the harness binary itself).
func (s *Sys) pointsAtHarness(path string) bool {
	if r, err := filepath.EvalSymlinks(path); err == nil && r == s.self() {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	return bytes.HasPrefix(head, []byte("#!")) && harnessShebang.Match(head)
}

// FindBin finds the real `name` binary: HARNESS_<NAME>_BIN, then PATH and the usual install dirs,
// skipping anything that points back at harness. "" when absent.
func (s *Sys) FindBin(name string) string {
	if o := s.Env.Get("HARNESS_" + strings.ToUpper(name) + "_BIN"); o != "" {
		return o
	}
	var dirs []string
	for _, p := range filepath.SplitList(s.Env.Get("PATH")) {
		if p != "" {
			dirs = append(dirs, p)
		}
	}
	dirs = append(dirs, filepath.Join(s.Home, ".local/bin"), filepath.Join(s.Home, ".local/share/mise/shims"),
		"/opt/homebrew/bin", "/usr/local/bin")
	for _, d := range dirs {
		c := filepath.Join(d, name)
		if executable(c) && !s.pointsAtHarness(c) {
			return c
		}
	}
	return ""
}

// Which is shutil.which over env PATH only, with no harness filtering.
func (s *Sys) Which(name string) string { return which(name, s.Env.Get("PATH")) }

func which(name, path string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		if executable(name) {
			return name
		}
		return ""
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		if c := filepath.Join(d, name); executable(c) {
			return c
		}
	}
	return ""
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

func (s *Sys) command(name string, args ...string) *exec.Cmd {
	bin := name
	if !strings.ContainsRune(name, os.PathSeparator) {
		if w := s.Which(name); w != "" {
			bin = w
		}
	}
	c := exec.Command(bin, args...)
	c.Args[0] = name
	c.Env = s.Env.Environ()
	return c
}

// Output runs name with s.Env and returns stdout; stderr is discarded.
func (s *Sys) Output(name string, args ...string) (string, error) {
	out, err := s.command(name, args...).Output()
	return string(out), err
}

// Run runs name with s.Env, output to the launcher's stdout and stderr.
func (s *Sys) Run(name string, args ...string) error {
	c := s.command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, s.stderr()
	return c.Run()
}

// Shell runs cmd under /bin/sh -c and returns trimmed stdout, ignoring failure (Python shell=True).
func (s *Sys) Shell(cmd string) string {
	c := exec.Command("/bin/sh", "-c", cmd)
	c.Env = s.Env.Environ()
	out, _ := c.Output()
	return strings.TrimSpace(string(out))
}
