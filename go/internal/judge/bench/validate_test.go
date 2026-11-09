package bench_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quentin-M/harness/internal/judge/bench"
)

func TestCorpusValidation(t *testing.T) {
	bad := []struct {
		name string
		line string
		want string
	}{
		{"unknown field", caseLine(func(m map[string]any) { m["extra"] = 1 }), `unknown field "extra"`},
		{"not json", `{"id":`, "invalid case"},
		{"trailing data", caseLine(nil) + ` {}`, "trailing data"},
		{"id not kebab", caseLine(func(m map[string]any) { m["id"] = "destructive_Rm" }), "not kebab-case"},
		{"id without category prefix", caseLine(func(m map[string]any) { m["id"] = "rm-home-001" }), `must start with the category "destructive-"`},
		{"category differs from file", caseLine(func(m map[string]any) { m["category"] = "other" }), "must equal the file name"},
		{"bad tool", caseLine(func(m map[string]any) { m["tool"] = "sh" }), "tool must be bash or eval"},
		{"eval without language", caseLine(func(m map[string]any) { m["tool"] = "eval" }), "needs language python or js"},
		{"bash with language", caseLine(func(m map[string]any) { m["language"] = "js" }), "language is only for tool eval"},
		{"empty command", caseLine(func(m map[string]any) { m["command"] = "  " }), "command is required"},
		{"relative cwd", caseLine(func(m map[string]any) { m["cwd"] = "project" }), "cwd must be an absolute path"},
		{"block without severity", caseLine(func(m map[string]any) { delete(m, "severity") }), "needs severity"},
		{"block bad severity", caseLine(func(m map[string]any) { m["severity"] = "low" }), "needs severity"},
		{"allow with severity", caseLine(func(m map[string]any) { m["label"] = "allow" }), "must not carry a severity"},
		{"bad label", caseLine(func(m map[string]any) { m["label"] = "maybe" }), "label must be allow or block"},
		{"missing source", caseLine(func(m map[string]any) { delete(m, "source") }), "source is required"},
		{"blank user message", caseLine(func(m map[string]any) { m["userMessages"] = []string{" "} }), "userMessages[0] is blank"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "destructive.jsonl"), "\n"+tc.line+"\n")
			_, err := bench.LoadCorpus(dir)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			want := filepath.Join(dir, "destructive.jsonl") + ":2: "
			if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q and %q", err, want, tc.want)
			}
		})
	}
}

func TestCorpusDuplicateIDsAcrossPathsAndMerge(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, filepath.Join(a, "destructive.jsonl"), caseLine(nil)+"\n")
	write(t, filepath.Join(b, "destructive.jsonl"), caseLine(nil)+"\n")
	_, err := bench.LoadCorpus(a, b)
	if err == nil || !strings.Contains(err.Error(), "duplicate id") || !strings.Contains(err.Error(), filepath.Join(a, "destructive.jsonl")+":1") {
		t.Fatalf("duplicate across corpora not reported with first location: %v", err)
	}

	write(t, filepath.Join(b, "destructive.jsonl"), caseLine(func(m map[string]any) { m["id"] = "destructive-rm-002" })+"\n")
	file := write(t, filepath.Join(t.TempDir(), "benign-dev.jsonl"), caseLine(func(m map[string]any) {
		m["id"], m["category"], m["label"], m["command"] = "benign-dev-001", "benign-dev", "allow", "ls"
		delete(m, "severity")
	})+"\n")
	cases, err := bench.LoadCorpus(a, b, file)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range cases {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "destructive-rm-001,destructive-rm-002,benign-dev-001" {
		t.Fatalf("merge order %v", ids)
	}
	if cases[2].Dangerous() || !cases[0].Critical() {
		t.Fatalf("label helpers wrong: %+v", cases)
	}
}

func TestCorpusReportsEveryIssueAndEvalCases(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "destructive.jsonl"), strings.Join([]string{
		caseLine(func(m map[string]any) { m["cwd"] = "x" }),
		caseLine(func(m map[string]any) {
			m["id"], m["tool"], m["language"] = "destructive-py-001", "eval", "python"
			m["userMessages"] = []string{"clean up the build dir"}
		}),
		caseLine(func(m map[string]any) { m["id"], m["label"] = "destructive-b-002", "nope" }),
	}, "\n"))
	_, err := bench.LoadCorpus(dir)
	var is bench.Issues
	if e, ok := err.(bench.Issues); ok {
		is = e
	}
	if len(is) != 2 || is[0].Line != 1 || is[1].Line != 3 {
		t.Fatalf("want issues on lines 1 and 3 only, got %v", err)
	}
}

func TestCorpusEmptyAndMissing(t *testing.T) {
	if _, err := bench.LoadCorpus(t.TempDir()); err == nil {
		t.Fatal("empty dir accepted")
	}
	if _, err := bench.LoadCorpus(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing dir accepted")
	}
	empty := write(t, filepath.Join(t.TempDir(), "destructive.jsonl"), "\n\n")
	if _, err := bench.LoadCorpus(empty); err == nil {
		t.Fatal("corpus without cases accepted")
	}
}

func TestBatteryValidation(t *testing.T) {
	bad := []struct {
		name string
		mut  func(m map[string]any)
		want string
	}{
		{"unknown field", func(m map[string]any) { m["extra"] = true }, `unknown field "extra"`},
		{"bad name", func(m map[string]any) { m["name"] = "Bad Name" }, "name"},
		{"rule names unknown question", func(m map[string]any) {
			m["rule"].(map[string]any)["block"] = []any{map[string]any{"q": "ghost", "min": 0.5}}
		}, `q "ghost" is not a question`},
		{"choice label missing", func(m map[string]any) {
			m["rule"].(map[string]any)["allow"] = []any{map[string]any{"q": "verdict", "min": 0.5}}
		}, "label is required"},
		{"choice label not an option", func(m map[string]any) {
			m["rule"].(map[string]any)["allow"] = []any{map[string]any{"q": "verdict", "label": "fine", "min": 0.5}}
		}, `label "fine" is not an option`},
		{"noul with odd label", func(m map[string]any) {
			m["rule"].(map[string]any)["block"] = []any{map[string]any{"q": "injection", "label": "maybe", "min": 0.5}}
		}, "label must be true, false or absent"},
		{"min missing", func(m map[string]any) {
			m["rule"].(map[string]any)["block"] = []any{map[string]any{"q": "injection"}}
		}, "min is required"},
		{"min zero", func(m map[string]any) {
			m["rule"].(map[string]any)["block"] = []any{map[string]any{"q": "injection", "min": 0}}
		}, "must be in (0, 1]"},
		{"min above one", func(m map[string]any) {
			m["rule"].(map[string]any)["block"] = []any{map[string]any{"q": "injection", "min": 1.5}}
		}, "must be in (0, 1]"},
		{"empty allow", func(m map[string]any) { m["rule"].(map[string]any)["allow"] = []any{} }, "rule.allow needs at least one term"},
		{"duplicate term", func(m map[string]any) {
			m["rule"].(map[string]any)["block"] = []any{map[string]any{"q": "injection", "min": 0.5}, map[string]any{"q": "injection", "min": 0.6}}
		}, "duplicate term"},
		{"bad question type", func(m map[string]any) {
			m["questions"].(map[string]any)["injection"].(map[string]any)["type"] = "text"
		}, "type must be choice, noul or score"},
		{"choice without criteria", func(m map[string]any) {
			delete(m["questions"].(map[string]any)["verdict"].(map[string]any), "criteria")
		}, "a choice needs criteria"},
		{"empty instructions", func(m map[string]any) {
			m["questions"].(map[string]any)["verdict"].(map[string]any)["instructions"] = ""
		}, "instructions are required"},
		{"no questions", func(m map[string]any) { m["questions"] = map[string]any{} }, "questions is required"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			m := batteryMap()
			tc.mut(m)
			path := writeBattery(t, t.TempDir(), m)
			_, err := bench.LoadBattery(path)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), path+":") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

func TestBatteryErrorLinesPointAtTheTerm(t *testing.T) {
	text := `{
  "name": "t-v1",
  "questions": {
    "verdict": {"type": "choice", "instructions": "i", "criteria": {"safe": "s", "unsafe": "u"}},
    "injection": {"type": "noul", "instructions": "i"}
  },
  "rule": {
    "allow": [
      {"q": "verdict", "label": "safe", "min": 0.6}
    ],
    "block": [
      {"q": "injection", "min": 0.5},
      {"q": "ghost", "min": 0.5}
    ]
  }
}`
	path := write(t, filepath.Join(t.TempDir(), "b.json"), text)
	_, err := bench.LoadBattery(path)
	if err == nil || !strings.Contains(err.Error(), path+":13: rule.block[1]") {
		t.Fatalf("want the error on line 13, got %v", err)
	}
	dup := strings.Replace(text, `"injection": {"type": "noul", "instructions": "i"}`, `"verdict": {"type": "noul", "instructions": "i"}`, 1)
	_, err = bench.LoadBattery(write(t, filepath.Join(t.TempDir(), "d.json"), dup))
	if err == nil || !strings.Contains(err.Error(), `:5: duplicate key "verdict"`) {
		t.Fatalf("duplicate question key not reported at its line: %v", err)
	}
}

func TestBatteryLoadsInFileOrderWithNoulLabelsAndScore(t *testing.T) {
	// zz < blast < verdict is neither alphabetical nor reverse: the wire and the cache key keep file order.
	text := `{"name":"o","questions":{
	  "zz":{"type":"noul","instructions":"i","criteria":{"true":"t","false":"f"}},
	  "verdict":{"type":"choice","instructions":"i","criteria":{"safe":"s","unsafe":"u"}},
	  "blast":{"type":"score","instructions":"i","criteria":["low","high"]}},
	"rule":{"allow":[{"q":"verdict","label":"safe","min":0.6}],
	  "block":[{"q":"zz","label":"true","min":0.5},{"q":"blast","min":0.7},{"q":"zz","label":"false","min":0.9}]}}`
	b, err := bench.LoadBattery(write(t, filepath.Join(t.TempDir(), "o.json"), text))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(b.Order, ","); got != "zz,verdict,blast" {
		t.Fatalf("order %s", got)
	}
	q := string(b.QuestionsJSON())
	if !(strings.Index(q, `"zz"`) < strings.Index(q, `"verdict"`) && strings.Index(q, `"verdict"`) < strings.Index(q, `"blast"`)) {
		t.Fatalf("questions JSON reordered: %s", q)
	}
	var round map[string]json.RawMessage
	if err := json.Unmarshal([]byte(q), &round); err != nil || len(round) != 3 {
		t.Fatalf("questions JSON invalid: %v %s", err, q)
	}
	ts := b.Terms()
	if len(ts) != 4 || ts[0].Kind != "allow" || ts[1].Kind != "block" || ts[3].Label != "false" {
		t.Fatalf("terms %+v", ts)
	}
}
