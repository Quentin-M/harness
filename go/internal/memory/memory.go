// Package memory locates Claude Code's per-project auto memory, which codex and omp share.
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	indexFile  = "MEMORY.md"
	indexLines = 200
	indexBytes = 25_000
	keyLen     = 200
)

// Root is the directory Claude keys memory on: the main checkout for a linked worktree, the git
// toplevel inside a repo, else cwd itself. It mirrors Claude's file reads, so no git process runs.
func Root(cwd string) string {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	top := gitTop(dir)
	if top == "" {
		return dir
	}
	return mainCheckout(top)
}

func gitTop(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// mainCheckout follows a worktree's .git file to the repository it belongs to, accepting it only
// when the worktree's gitdir points back at top, as Claude does.
func mainCheckout(top string) string {
	b, err := os.ReadFile(filepath.Join(top, ".git"))
	if err != nil {
		return top
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return top
	}
	wt := resolve(top, strings.TrimSpace(ref))
	common, err := os.ReadFile(filepath.Join(wt, "commondir"))
	if err != nil {
		return top
	}
	git := resolve(wt, strings.TrimSpace(string(common)))
	if filepath.Dir(wt) != filepath.Join(git, "worktrees") {
		return top
	}
	back, err := os.ReadFile(filepath.Join(wt, "gitdir"))
	if err != nil {
		return top
	}
	backReal, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(back)))
	topReal, err2 := filepath.EvalSymlinks(top)
	if err1 != nil || err2 != nil || backReal != filepath.Join(topReal, ".git") {
		return top
	}
	if filepath.Base(git) != ".git" {
		return git
	}
	return filepath.Dir(git)
}

func resolve(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// Key is Claude's project directory name for root: every non-alphanumeric UTF-16 unit becomes '-',
// and a name past 200 units is cut and suffixed with root's base-36 Java string hash.
func Key(root string) string {
	units := utf16.Encode([]rune(root))
	b := make([]byte, len(units))
	var h int32
	for i, u := range units {
		h = h<<5 - h + int32(u)
		if u < utf8.RuneSelf && (u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9') {
			b[i] = byte(u)
		} else {
			b[i] = '-'
		}
	}
	if len(b) <= keyLen {
		return string(b)
	}
	abs := int64(h)
	if abs < 0 {
		abs = -abs
	}
	return string(b[:keyLen]) + "-" + strconv.FormatInt(abs, 36)
}

// Dir is the auto memory directory Claude uses for a session started in cwd.
func Dir(state, cwd string) string {
	return filepath.Join(state, "claude", "projects", Key(Root(cwd)), "memory")
}

// Index is dir's MEMORY.md as Claude loads it: trimmed, capped at 200 lines and 25KB, with Claude's
// warning appended when cut. A missing index is "".
func Index(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, indexFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("memory index: %w", err)
	}
	s := strings.TrimSpace(string(b))
	lines, size := strings.Split(s, "\n"), len(s)
	long, big := len(lines) > indexLines, size > indexBytes
	if !long && !big {
		return s, nil
	}
	if long {
		s = strings.Join(lines[:indexLines], "\n")
	}
	if len(s) > indexBytes {
		if i := strings.LastIndexByte(s[:indexBytes+1], '\n'); i > 0 {
			s = s[:i]
		} else {
			s = strings.ToValidUTF8(s[:indexBytes], "")
		}
	}
	why := fmt.Sprintf("%d lines and %d bytes", len(lines), size)
	if !big {
		why = fmt.Sprintf("%d lines (limit: %d)", len(lines), indexLines)
	} else if !long {
		why = fmt.Sprintf("%d bytes (limit: %d)", size, indexBytes)
	}
	return s + "\n\n> WARNING: " + indexFile + " is " + why + ". Only part of it was loaded. " +
		"Keep index entries to one line under ~200 chars; move detail into topic files.", nil
}

// PathCmd prints the memory directory for args[0], resolved against cwd, or for cwd itself.
func PathCmd(state string, args []string, cwd string, out io.Writer) error {
	switch len(args) {
	case 0:
	case 1:
		cwd = resolve(cwd, args[0])
	default:
		return fmt.Errorf("usage: harness memory path [dir]")
	}
	_, err := fmt.Fprintln(out, Dir(state, cwd))
	return err
}

type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// ContextHook answers a claude or codex SessionStart hook: it injects the session's MEMORY.md and
// its directory, and prints nothing when the index is empty.
func ContextHook(state string, stdin io.Reader, out io.Writer) error {
	var in struct {
		Cwd string `json:"cwd"`
	}
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in); err != nil {
		return fmt.Errorf("memory context: hook payload: %w", err)
	}
	if !filepath.IsAbs(in.Cwd) {
		return fmt.Errorf("memory context: hook payload cwd %q is not absolute", in.Cwd)
	}
	dir := Dir(state, in.Cwd)
	idx, err := Index(dir)
	if err != nil || idx == "" {
		return err
	}
	var o hookOutput
	o.HookSpecificOutput.HookEventName = "SessionStart"
	o.HookSpecificOutput.AdditionalContext = "<memories>\nMemory directory: " + dir +
		"\nRead a topic file when its index line is relevant.\n\n## " + indexFile + "\n\n" + idx + "\n</memories>"
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(o)
}
