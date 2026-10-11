package diagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func want(t *testing.T, md string, wants ...string) {
	t.Helper()
	var got []string
	for _, i := range Lint(md) {
		got = append(got, i.String())
	}
	if len(got) != len(wants) {
		t.Fatalf("got %d issues, want %d:\n%s", len(got), len(wants), strings.Join(got, "\n"))
	}
	for i, w := range wants {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("issue %d: got %q, want prefix %q", i, got[i], w)
		}
	}
}

func TestCleanDiagramsPass(t *testing.T) {
	want(t, `
    ╭──────────────────╮
    │ parse + validate │ ─────────▶ deny + log
    ╰────────┬─────────╯
             ▼
    ╭──────────────────╮
    │ core             │
    ╰──────────────────╯

    src/
    ├── auth/
    │   └── session.ts    ~  moved
    └── api/login.ts      +  new

     client         api
       │             │
       ├─ POST /pay ▶│
       │◀─ 200 ──────┤
`)
}

func TestVerticalOffByOne(t *testing.T) {
	want(t, "\n    ╭───┬───╮\n    ╰───┴───╯\n             │\n",
		"4:10: │ hangs from nothing above")
	want(t, "# t\n\n```text\nok ─▶ b\n   │\n```\n",
		"5:4: │ reaches up into ─, which has no arm back")
}

func TestShiftedColumnBreaksEveryJoin(t *testing.T) {
	want(t, "\n    ┌─┬─┐\n    │  │ │\n    └─┴─┘\n",
		"2:3: ┬ has an open down arm",
		"2:5: ┐ has an open down arm",
		"3:4: │ reaches up into ─",
		"3:4: │ reaches down into ─",
		"3:6: │ hangs from nothing above",
		"4:3: ┴ has an open up arm",
		"4:5: ┘ has an open up arm")
}

func TestJoinsMustMeet(t *testing.T) {
	want(t, "\n    ──┐\n      ─\n", "2:3: ┐ reaches down into ─")
	want(t, "\n    ├─│\n", "2:2: ─ reaches right into │")
	want(t, "\n    ─┬\n", "2:2: ┬ has an open right arm")
}

func TestArrowheadsNeedAShaft(t *testing.T) {
	want(t, "\n    a ─▶ b\n    c  ▶ d\n", "3:4: arrowhead ▶ has no line reaching it")
	want(t, "\n    │\n    ▼\n      ▼\n", "4:3: arrowhead ▼ has no line reaching it")
}

func TestWidthTabsAndEmoji(t *testing.T) {
	want(t, "\n    ─"+strings.Repeat("x", 80)+"\n", "2:81: line is 81 cells")
	want(t, "\n    ─▶ 🚀 deploy\n", "2:4: U+1F680 emoji")
	want(t, "```\n─▶\tok\n```\n", "2:3: U+0009 tab")
}

func TestOnlyDiagramsAreLinted(t *testing.T) {
	want(t, "```go\n\tx := a │ b\n```\n")
	want(t, "\n    plain indented prose\twith a tab\n")
	want(t, "- item\n    │ continuation, not a block\n")
}

func TestLintOffSkipsTheNextDiagramOnly(t *testing.T) {
	want(t, "<!-- diagram-lint: off -->\n\n    ─┬\n\nprose\n\n    ─┬\n", "7:2: ┬ has an open right arm")
}

// The harness's own rules and README follow the rule they teach.
func TestHarnessDocsAreClean(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files, _ := filepath.Glob(filepath.Join(root, "rules", "*.md"))
	files = append(files, filepath.Join(root, "README.md"))
	if len(files) < 5 {
		t.Fatalf("found %d docs under %s", len(files), root)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range Lint(string(b)) {
			t.Errorf("%s:%s", filepath.Base(f), i)
		}
	}
}
