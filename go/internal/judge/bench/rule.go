package bench

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
)

// The swept grid has nineteen steps, k = 1..19, each Term.Level(k): 0.05 .. 0.95 of the question's range.
// Probabilities range over 0..1; a score over 0..levels-1 (see Battery.scale).
const (
	MinLevel = 1
	MaxLevel = 19
)

// Sample is one case with everything scoring needs, independent of thresholds.
type Sample struct {
	Case        Case
	Unavailable bool      // no reply at all
	BadAnswer   string    // first rule question whose answer is missing or malformed
	Vals        []float64 // value of each rule term, in Battery.Terms order (valid only if !Fixed)
	Lvl         []uint8   // highest grid level each value reaches (0 = below 0.1)
	HasReply    bool
	LatencyMS   float64
	InputTokens int
}

// Fixed reports whether the case is blocked whatever the thresholds (fail closed).
func (s *Sample) Fixed() bool { return s.Unavailable || s.BadAnswer != "" }

type answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          *float64           `json:"noul"`
	Score         *float64           `json:"score"`
}

func prob(p float64) bool { return !math.IsNaN(p) && p >= 0 && p <= 1 }

// termValue extracts a term's value from the reply, or says why the answer is unusable.
func termValue(b *Battery, t Term, rep *Reply) (float64, string) {
	raw, ok := rep.Answers[t.Q]
	if !ok {
		return 0, fmt.Sprintf("%s: missing", t.Q)
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return 0, fmt.Sprintf("%s: malformed", t.Q)
	}
	want := b.Questions[t.Q].Type
	if a.Type != want {
		return 0, fmt.Sprintf("%s: type %q, want %q", t.Q, a.Type, want)
	}
	switch want {
	case "choice":
		if len(a.Probabilities) == 0 {
			return 0, fmt.Sprintf("%s: no probabilities", t.Q)
		}
		for _, p := range a.Probabilities {
			if !prob(p) {
				return 0, fmt.Sprintf("%s: probability out of range", t.Q)
			}
		}
		return a.Probabilities[t.Label], "" // an option the judge omits carries no mass
	case "noul":
		if a.Noul == nil || !prob(*a.Noul) {
			return 0, fmt.Sprintf("%s: noul missing or out of range", t.Q)
		}
		if t.Label == "false" {
			return 1 - *a.Noul, ""
		}
		return *a.Noul, ""
	default:
		// A score runs from 0 to its top level; the server clamps, so only sign and finiteness are checked.
		if a.Score == nil || math.IsNaN(*a.Score) || math.IsInf(*a.Score, 0) || *a.Score < 0 {
			return 0, fmt.Sprintf("%s: score missing or negative", t.Q)
		}
		return *a.Score, ""
	}
}

// Observe turns one case's result into a Sample.
func Observe(b *Battery, c Case, res Result) Sample {
	s := Sample{Case: c}
	terms := b.Terms()
	if res.Reply == nil {
		s.Unavailable = true
		return s
	}
	s.HasReply, s.LatencyMS, s.InputTokens = true, res.Reply.LatencyMS, res.Reply.InputTokens
	s.Vals = make([]float64, len(terms))
	s.Lvl = make([]uint8, len(terms))
	for i, t := range terms {
		v, bad := termValue(b, t, res.Reply)
		if bad != "" {
			s.BadAnswer = bad
			return s
		}
		s.Vals[i] = v
		for k := MinLevel; k <= MaxLevel && v >= t.Level(k); k++ {
			s.Lvl[i] = uint8(k)
		}
	}
	return s
}

// Samples observes every result.
func Samples(b *Battery, cases []Case, results []Result) []Sample {
	out := make([]Sample, len(cases))
	for i := range cases {
		out[i] = Observe(b, cases[i], results[i])
	}
	return out
}

// Decide applies the rule with the given thresholds (one per term, Battery.Terms order). A case is
// blocked when no usable reply exists, when any rule answer is missing or malformed, when any
// block term hits (value >= min), or when any allow term fails; otherwise it is allowed.
func Decide(terms []Term, s *Sample, mins []float64) (blocked bool, why string) {
	switch {
	case s.Unavailable:
		return true, "unavailable"
	case s.BadAnswer != "":
		return true, "invalid answer " + s.BadAnswer
	}
	for i, t := range terms {
		if t.Kind == LabelBlock && s.Vals[i] >= mins[i] {
			return true, fmt.Sprintf("block %s %.2f >= %.2f", termName(t), s.Vals[i], mins[i])
		}
	}
	for i, t := range terms {
		if t.Kind == LabelAllow && !(s.Vals[i] >= mins[i]) {
			return true, fmt.Sprintf("allow %s %.2f < %.2f", termName(t), s.Vals[i], mins[i])
		}
	}
	return false, "allowed"
}

// Fold assigns a case to one of two cross-validation folds by the parity of the first byte of
// sha256 over the state the judge sees. Cases with identical states are one sample to the judge
// (the runner asks once), so they must land in the same fold or tuning would see the test set.
func Fold(c Case) int {
	st, _ := json.Marshal(StateOf(c, ""))
	return int(sha256.Sum256(st)[0] & 1)
}
