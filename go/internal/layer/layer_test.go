package layer

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Quentin-M/harness/internal/fsx"
	"github.com/Quentin-M/harness/internal/model"
	"github.com/Quentin-M/harness/internal/sys"
)

func md(t *testing.T, path, body, fm string) {
	t.Helper()
	text := body
	if fm != "" {
		text = "---\n" + fm + "---\n" + body
	}
	if err := fsx.Write(path, []byte(text)); err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	root, extra string
	set         *Set
	gw          *model.Config
}

func fixture(t *testing.T) fx {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "harness")
	h, extra := root, filepath.Join(tmp, "extra")
	md(t, h+"/AGENTS.md", "# BASE\n", "")
	md(t, h+"/rules/a.md", "RULE-A\n", "paths:\n- '**/*.go'\n")
	md(t, h+"/rules/b.md", "RULE-B\n", "")
	md(t, h+"/styles/i-have-adhd.md", "STYLE-BODY\n", "name: i-have-adhd\n")
	md(t, h+"/skills/trace/SKILL.md", "harness trace\n", "name: trace\n")
	md(t, h+"/skills/only-base/SKILL.md", "x\n", "name: only-base\n")
	md(t, h+"/skills/not-a-skill/README.md", "x\n", "")
	md(t, h+"/skills/tdd/SKILL.md", "pstack tdd\n", "name: tdd\n")
	fsx.Write(h+"/skills/pstack-sheet.yaml", []byte("# comment\n\"judgment and prose\": default\n\"interrogate reviewers\": [default, review]\n"))
	md(t, extra+"/AGENTS.md", "# EXTRA\n", "")
	md(t, extra+"/skills/trace/SKILL.md", "extra trace\n", "name: trace\n")
	md(t, extra+"/rules/z.md", "RULE-EXTRA\n", "")
	set := Discover(root, sys.Env{"HARNESS_LAYERS": extra + "::" + filepath.Join(tmp, "missing"), "PATH": os.Getenv("PATH")})
	gw := &model.Config{Raw: map[string]any{"provider": "gw", "roles": map[string]any{"default": "big", "review": "rev"},
		"overrides": map[string]any{"codex": map[string]any{"default": "rev"}}}}
	return fx{root: root, extra: extra, set: set, gw: gw}
}

func real(p string) string { r, _ := filepath.EvalSymlinks(p); return r }

func TestDiscoverOrder(t *testing.T) {
	f := fixture(t)
	want := []string{real(f.root), real(f.extra)}
	if !reflect.DeepEqual(f.set.Dirs, want) {
		t.Fatalf("%v\n%v", f.set.Dirs, want)
	}
}

func TestDiscoverTildeLayer(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "lay"), 0o755)
	root := t.TempDir()
	s := Discover(root, sys.Env{"HOME": home, "HARNESS_LAYERS": "~/lay"})
	if !reflect.DeepEqual(s.Dirs, []string{real(root), real(filepath.Join(home, "lay"))}) {
		t.Fatal(s.Dirs)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "-C", dir}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/opt/homebrew/bin", "GIT_CONFIG_NOSYSTEM=1", "HOME=" + dir}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestDiscoverSuperproject(t *testing.T) {
	sup := t.TempDir()
	root := filepath.Join(sup, "harness")
	os.MkdirAll(root, 0o755)
	os.MkdirAll(filepath.Join(sup, ".agents"), 0o755)
	git(t, root, "init", "-q")
	git(t, root, "commit", "-q", "--allow-empty", "-m", "x")
	git(t, sup, "init", "-q")
	git(t, sup, "add", "harness")
	s := Discover(root, sys.Env{"PATH": "/usr/bin:/bin:/opt/homebrew/bin"})
	want := []string{real(root), real(filepath.Join(sup, ".agents"))}
	if !reflect.DeepEqual(s.Dirs, want) {
		t.Fatalf("%v\n%v", s.Dirs, want)
	}
}

func TestSkillsAndStyle(t *testing.T) {
	f := fixture(t)
	sk := f.set.Skills()
	var names []string
	for n := range sk {
		names = append(names, n)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"only-base", "tdd", "trace"}) || sk["trace"] != real(f.extra+"/skills/trace") {
		t.Fatal(sk)
	}
	if f.set.Style() != "---\nname: i-have-adhd\n---\nSTYLE-BODY\n" || f.set.StyleBody() != "STYLE-BODY\n" {
		t.Fatal(f.set.Style())
	}
	if (&Set{}).Style() != "" {
		t.Fatal("empty style")
	}
}

func TestInstructionsExact(t *testing.T) {
	f := fixture(t)
	sheet := "## pstack models\n\npstack per-role model choices (override the skill defaults), one line per role:\n\n```\n"
	omp := "# BASE\n\n# EXTRA\n\nRULE-A\n\nRULE-B\n\nRULE-EXTRA\n\n" + sheet +
		"judgment and prose: gw/big\ninterrogate reviewers: gw/big, gw/rev\n```\n"
	if got := f.set.Instructions(f.gw, "omp"); got != omp {
		t.Fatalf("got\n%s\nwant\n%s", got, omp)
	}
	codex := "# BASE\n\n# EXTRA\n\nRULE-A\n\nRULE-B\n\nRULE-EXTRA\n\n" + sheet +
		"judgment and prose: rev\ninterrogate reviewers: rev, rev\n```\n\nSTYLE-BODY\n"
	if got := f.set.Instructions(f.gw, "codex"); got != codex {
		t.Fatalf("got\n%s\nwant\n%s", got, codex)
	}
	if got := (&Set{Root: t.TempDir()}).Instructions(f.gw, "omp"); got != "\n" {
		t.Fatalf("%q", got)
	}
}

func TestLinkSkills(t *testing.T) {
	f := fixture(t)
	dest := t.TempDir() + "/skills"
	if err := f.set.LinkSkills(dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest + "/trace/SKILL.md"); !strings.HasSuffix(string(b), "extra trace\n") {
		t.Fatal(string(b))
	}
	if err := f.set.LinkSkills(dest); err != nil {
		t.Fatal("not idempotent:", err)
	}
}

func TestLinkSkillsKeepsHarnessOwnedEntries(t *testing.T) {
	f := fixture(t)
	dest := t.TempDir() + "/skills"
	if err := f.set.LinkSkills(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest+"/.system/imagegen", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nowhere", dest+"/stale"); err != nil {
		t.Fatal(err)
	}
	if err := f.set.LinkSkills(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest + "/.system/imagegen"); err != nil {
		t.Fatal("codex .system removed:", err)
	}
	if _, err := os.Lstat(dest + "/stale"); err == nil {
		t.Fatal("stale skill link kept")
	}
}

func TestLinkSkillsReplacesAStaleGeneratedSkill(t *testing.T) {
	f := fixture(t)
	dest := t.TempDir() + "/skills"
	if err := os.MkdirAll(dest+"/trace", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+"/trace/SKILL.md", []byte("---\nname: trace\n---\nold generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.set.LinkSkills(dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest + "/trace/SKILL.md"); !strings.HasSuffix(string(b), "extra trace\n") {
		t.Fatalf("stale copy kept: %q", b)
	}
}
