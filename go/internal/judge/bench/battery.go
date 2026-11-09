package bench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
)

// Question is one System One question: choice (labelled options), noul (a probability) or score.
// Criteria is passed through to the judge as written; only its shape is checked.
type Question struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

// Term is one clause of the decision rule. For a choice question Label names the option whose
// probability is compared with Min; noul and score questions compare their value.
type Term struct {
	Kind  string   `json:"-"` // "allow" or "block"
	Q     string   `json:"q"`
	Label string   `json:"label,omitempty"`
	Min   *float64 `json:"min"`

	// Scale is the top of the question's value range: 1 for probabilities, levels-1 for a score
	// whose criteria list its levels. Set by Battery.Terms.
	Scale float64 `json:"-"`
}

// Level is the sweep threshold at grid step k (1..19): k/20 of the question's range, so 0.05 .. 0.95
// for a probability.
func (t Term) Level(k int) float64 {
	scale := t.Scale
	if scale == 0 {
		scale = 1
	}
	return math.Round(float64(k)/20*scale*1e6) / 1e6
}

// Rule is the decision rule: block when any block term hits, otherwise allow iff every allow term holds.
type Rule struct {
	Allow []Term `json:"allow"`
	Block []Term `json:"block"`
}

// Battery is a question set plus its rule.
type Battery struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Notice      string              `json:"notice,omitempty"`
	Questions   map[string]Question `json:"questions"`
	Rule        Rule                `json:"rule"`

	Order []string `json:"-"` // question ids in file order
	Path  string   `json:"-"`
}

var batteryName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Terms lists every rule term, allow terms first, each in file order. Their order is the
// threshold order in reports and sweeps.
func (b *Battery) Terms() []Term {
	out := make([]Term, 0, len(b.Rule.Allow)+len(b.Rule.Block))
	for _, t := range b.Rule.Allow {
		t.Kind, t.Scale = LabelAllow, b.scale(t.Q)
		out = append(out, t)
	}
	for _, t := range b.Rule.Block {
		t.Kind, t.Scale = LabelBlock, b.scale(t.Q)
		out = append(out, t)
	}
	return out
}

// scale is the top of question q's value range. A score runs from 0 to its top level, so one with
// criteria listing n levels has range n-1; everything else is a probability.
func (b *Battery) scale(q string) float64 {
	question, ok := b.Questions[q]
	if !ok || question.Type != "score" {
		return 1
	}
	var levels []string
	if json.Unmarshal(question.Criteria, &levels) == nil && len(levels) > 2 {
		return float64(len(levels) - 1)
	}
	return 1
}

// Mins are the thresholds the battery ships with, in Terms order.
func (b *Battery) Mins() []float64 {
	ts := b.Terms()
	out := make([]float64, len(ts))
	for i, t := range ts {
		out[i] = *t.Min
	}
	return out
}

// QuestionsJSON is the questions object in file order: the exact bytes sent and cache-keyed.
func (b *Battery) QuestionsJSON() json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, id := range b.Order {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(id)
		q, _ := json.Marshal(b.Questions[id])
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(q)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// LoadBattery reads and validates one battery file. Errors carry file:line.
func LoadBattery(path string) (*Battery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("battery: %w", err)
	}
	li := newLineIndex(data)
	fail := func(off int, f string, a ...any) error {
		return Issues{{path, li.line(off), fmt.Sprintf(f, a...)}}
	}
	var b Battery
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return nil, fail(errOffset(data, err), "invalid battery: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fail(int(dec.InputOffset()), "trailing data after the JSON object")
	}
	b.Path = path

	pos, err := scanBattery(data)
	if err != nil {
		return nil, fail(0, "invalid battery: %v", err)
	}
	var issues Issues
	add := func(off int, f string, a ...any) {
		issues = append(issues, Issue{path, li.line(off), fmt.Sprintf(f, a...)})
	}
	for _, dup := range pos.dups {
		add(dup.off, "duplicate key %q", dup.key)
	}
	b.Order = pos.order

	if !batteryName.MatchString(b.Name) {
		add(pos.top["name"], "name %q must be non-empty and file-name safe ([a-z0-9._-])", b.Name)
	}
	if len(b.Questions) == 0 {
		add(pos.top["questions"], "questions is required")
	}
	for _, id := range b.Order {
		q := b.Questions[id]
		at := pos.questions[id]
		if !kebabish.MatchString(id) {
			add(at, "question id %q must match [a-z][a-z0-9_-]*", id)
		}
		if strings.TrimSpace(q.Instructions) == "" {
			add(at, "question %q: instructions are required", id)
		}
		for _, m := range criteriaIssues(q) {
			add(at, "question %q: %s", id, m)
		}
	}
	if len(b.Rule.Allow) == 0 {
		add(pos.top["rule"], "rule.allow needs at least one term (an empty allow list would pass everything not blocked)")
	}
	seen := map[string]bool{}
	for kind, terms := range map[string][]Term{LabelAllow: b.Rule.Allow, LabelBlock: b.Rule.Block} {
		for i, t := range terms {
			at := pos.terms[kind][i]
			for _, m := range termIssues(&b, t) {
				add(at, "rule.%s[%d]: %s", kind, i, m)
			}
			key := kind + "\x00" + t.Q + "\x00" + t.Label
			if seen[key] {
				add(at, "rule.%s[%d]: duplicate term %s", kind, i, termName(t))
			}
			seen[key] = true
		}
	}
	if len(issues) > 0 {
		sortIssues(issues)
		return nil, issues
	}
	return &b, nil
}

var kebabish = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func termName(t Term) string {
	if t.Label != "" {
		return t.Q + "=" + t.Label
	}
	return t.Q
}

func termIssues(b *Battery, t Term) []string {
	var m []string
	q, ok := b.Questions[t.Q]
	switch {
	case t.Q == "":
		m = append(m, "q is required")
	case !ok:
		m = append(m, fmt.Sprintf("q %q is not a question of this battery", t.Q))
	}
	top := 1.0
	if ok {
		top = b.scale(t.Q)
	}
	if t.Min == nil {
		m = append(m, "min is required")
	} else if math.IsNaN(*t.Min) || *t.Min <= 0 || *t.Min > top {
		m = append(m, fmt.Sprintf("min %v must be in (0, %v]", *t.Min, top))
	}
	if !ok {
		return m
	}
	switch q.Type {
	case "choice":
		labels := choiceLabels(q)
		if t.Label == "" {
			m = append(m, fmt.Sprintf("question %q is a choice: label is required", t.Q))
		} else if _, has := labels[t.Label]; !has {
			m = append(m, fmt.Sprintf("label %q is not an option of choice %q", t.Label, t.Q))
		}
	case "noul":
		if t.Label != "" && t.Label != "true" && t.Label != "false" {
			m = append(m, fmt.Sprintf("noul question %q: label must be true, false or absent (true is p, false is 1-p), got %q", t.Q, t.Label))
		}
	case "score":
		if t.Label != "" {
			m = append(m, fmt.Sprintf("question %q is a score: label is not allowed", t.Q))
		}
	}
	return m
}

func choiceLabels(q Question) map[string]string {
	var m map[string]string
	_ = json.Unmarshal(q.Criteria, &m)
	return m
}

func criteriaIssues(q Question) []string {
	c := bytes.TrimSpace(q.Criteria)
	absent := len(c) == 0 || string(c) == "null"
	switch q.Type {
	case "choice":
		var m map[string]string
		if absent || json.Unmarshal(c, &m) != nil || len(m) < 2 {
			return []string{"a choice needs criteria: an object of at least two option -> description strings"}
		}
		for k, v := range m {
			if k == "" || strings.TrimSpace(v) == "" {
				return []string{"choice criteria must map non-empty options to non-empty descriptions"}
			}
		}
	case "noul":
		var m map[string]string
		if !absent && json.Unmarshal(c, &m) != nil {
			return []string{"noul criteria, when present, must be an object of strings"}
		}
	case "score":
		var m map[string]string
		var a []string
		if !absent && json.Unmarshal(c, &m) != nil && json.Unmarshal(c, &a) != nil {
			return []string{"score criteria, when present, must be an array or object of strings"}
		}
	default:
		return []string{fmt.Sprintf("type must be choice, noul or score, got %q", q.Type)}
	}
	return nil
}

// ── line numbers ─────────────────────────────────────────────────────────────

type lineIndex struct{ starts []int }

func newLineIndex(data []byte) lineIndex {
	idx := lineIndex{starts: []int{0}}
	for i, c := range data {
		if c == '\n' {
			idx.starts = append(idx.starts, i+1)
		}
	}
	return idx
}

// line is the 1-based line of byte offset off.
func (l lineIndex) line(off int) int {
	lo, hi := 0, len(l.starts)
	for lo < hi-1 {
		mid := (lo + hi) / 2
		if l.starts[mid] <= off {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo + 1
}

func errOffset(data []byte, err error) int {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		return int(se.Offset)
	case errors.As(err, &te):
		return int(te.Offset)
	}
	// unknown field "x": point at the first quoted occurrence
	if _, rest, ok := strings.Cut(err.Error(), `unknown field "`); ok {
		if name, _, ok := strings.Cut(rest, `"`); ok {
			if i := bytes.Index(data, []byte(`"`+name+`"`)); i >= 0 {
				return i
			}
		}
	}
	return 0
}

type dupKey struct {
	key string
	off int
}

// batteryPos records where things sit in a battery file, for error lines.
type batteryPos struct {
	top       map[string]int
	questions map[string]int
	terms     map[string][]int
	order     []string
	dups      []dupKey
}

// scanBattery walks the document once, keeping key order and byte offsets that the map-based
// decode loses.
func scanBattery(data []byte) (*batteryPos, error) {
	p := &batteryPos{top: map[string]int{}, questions: map[string]int{}, terms: map[string][]int{}}
	err := walkObject(data, 0, func(key string, at int, raw json.RawMessage) error {
		if _, dup := p.top[key]; dup {
			p.dups = append(p.dups, dupKey{key, at})
		}
		p.top[key] = at
		switch key {
		case "questions":
			return walkObject(raw, at, func(id string, qat int, _ json.RawMessage) error {
				if _, dup := p.questions[id]; dup {
					p.dups = append(p.dups, dupKey{id, qat})
				} else {
					p.order = append(p.order, id)
				}
				p.questions[id] = qat
				return nil
			})
		case "rule":
			return walkObject(raw, at, func(kind string, kat int, arr json.RawMessage) error {
				return walkArray(arr, kat, func(tat int, _ json.RawMessage) error {
					p.terms[kind] = append(p.terms[kind], tat)
					return nil
				})
			})
		}
		return nil
	})
	return p, err
}

func skipSpace(data []byte, i int, also string) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r' || strings.IndexByte(also, data[i]) >= 0) {
		i++
	}
	return i
}

// walkObject calls fn for each member of the JSON object in data (which starts at absolute
// offset base) with the absolute offset of the member's value.
func walkObject(data []byte, base int, fn func(key string, at int, raw json.RawMessage) error) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errors.New("expected an object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		at := skipSpace(data, int(dec.InputOffset()), ":")
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		if err := fn(kt.(string), base+at, raw); err != nil {
			return err
		}
	}
	return nil
}

func walkArray(data []byte, base int, fn func(at int, raw json.RawMessage) error) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return errors.New("expected an array")
	}
	for dec.More() {
		at := skipSpace(data, int(dec.InputOffset()), ",")
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		if err := fn(base+at, raw); err != nil {
			return err
		}
	}
	return nil
}

func sortIssues(is Issues) {
	for i := 1; i < len(is); i++ {
		for j := i; j > 0 && is[j].Line < is[j-1].Line; j-- {
			is[j], is[j-1] = is[j-1], is[j]
		}
	}
}
