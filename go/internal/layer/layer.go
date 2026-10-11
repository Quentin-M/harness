// Package layer discovers config layers and renders their skills and instructions.
package layer

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/model"
	"github.com/Quentin-M/harness/internal/sys"
)

// StyleName is the output style every harness gets.
const StyleName = "i-have-adhd"

// Set is the resolved layer dirs, lowest precedence first.
type Set struct {
	Root string
	Dirs []string
}

// Discover resolves the layers: the harness checkout itself, the superproject's .agents, then HARNESS_LAYERS.
// Missing dirs are dropped.
func Discover(root string, env sys.Env) *Set {
	cands := []string{root}
	if sp := superproject(root, env); sp != "" {
		cands = append(cands, filepath.Join(sp, ".agents"))
	}
	for _, p := range strings.Split(env.Get("HARNESS_LAYERS"), ":") {
		if p != "" {
			cands = append(cands, expandHome(p, env))
		}
	}
	s := &Set{Root: root}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			s.Dirs = append(s.Dirs, resolve(c))
		}
	}
	return s
}

func superproject(root string, env sys.Env) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--show-superproject-working-tree")
	cmd.Env = env.Environ()
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func expandHome(p string, env sys.Env) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home := env.Get("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return home + p[1:]
}

func resolve(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func glob(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	sort.Strings(m)
	return m
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func read(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// Skills maps skill name to its dir; a later layer wins.
func (s *Set) Skills() map[string]string {
	out := map[string]string{}
	for _, d := range s.Dirs {
		for _, p := range glob(filepath.Join(d, "skills", "*", "SKILL.md")) {
			out[filepath.Base(filepath.Dir(p))] = filepath.Dir(p)
		}
	}
	return out
}

// Style is the raw text (frontmatter included) of the last layer's style file, "" when none.
func (s *Set) Style() string {
	for i := len(s.Dirs) - 1; i >= 0; i-- {
		if p := filepath.Join(s.Dirs[i], "styles", StyleName+".md"); isFile(p) {
			return read(p)
		}
	}
	return ""
}

// StyleBody is Style without its frontmatter.
func (s *Set) StyleBody() string {
	_, body := fsx.SplitFrontmatter(s.Style())
	return body
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return strings.TrimSpace(string(fsx.DumpYAML(v)))
}

// Instructions is the harness's AGENTS.md: every layer's AGENTS.md, then every layer's rules,
// then the pstack model sheet; codex also gets the style body, having no output-style slot.
func (s *Set) Instructions(m *model.Config, harness string) string {
	var parts []string
	for _, d := range s.Dirs {
		if f := filepath.Join(d, "AGENTS.md"); isFile(f) {
			parts = append(parts, read(f))
		}
	}
	for _, d := range s.Dirs {
		for _, f := range glob(filepath.Join(d, "rules", "*.md")) {
			_, body := fsx.SplitFrontmatter(read(f))
			parts = append(parts, body)
		}
	}
	sheet, _ := fsx.ReadYAML(filepath.Join(s.Root, "skills", "pstack-sheet.yaml"))
	if len(sheet) > 0 {
		var rows []string
		for _, label := range fsx.Keys(sheet) {
			var roles []string
			if list, ok := sheet[label].([]any); ok {
				for _, r := range list {
					roles = append(roles, toString(r))
				}
			} else {
				roles = []string{toString(sheet[label])}
			}
			refs := make([]string, len(roles))
			for i, r := range roles {
				refs[i] = m.Resolve(harness, r)
			}
			rows = append(rows, label+": "+strings.Join(refs, ", "))
		}
		parts = append(parts, "## pstack models\n\npstack per-role model choices (override the skill defaults), "+
			"one line per role:\n\n```\n"+strings.Join(rows, "\n")+"\n```")
	}
	if harness == "codex" {
		parts = append(parts, s.StyleBody())
	}
	var kept []string
	for _, part := range parts {
		if t := strings.TrimSpace(part); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, "\n\n") + "\n"
}

// LinkSkills makes dest a fresh dir of symlinks, one per skill.
func (s *Set) LinkSkills(dest string) error {
	if err := clearLinks(dest); err != nil {
		return err
	}
	for name, dir := range s.Skills() {
		// a layer skill wins over a same-named leftover copy (e.g. a command once rendered as a skill)
		if err := os.RemoveAll(filepath.Join(dest, name)); err != nil {
			return err
		}
		if err := os.Symlink(dir, filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	return nil
}

// clearLinks removes only the symlinks LinkSkills made; real dirs (codex's .system) belong to the harness.
func clearLinks(dest string) error {
	ents, err := os.ReadDir(dest)
	if os.IsNotExist(err) {
		return os.MkdirAll(dest, 0o755)
	}
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 {
			if err := os.Remove(filepath.Join(dest, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
