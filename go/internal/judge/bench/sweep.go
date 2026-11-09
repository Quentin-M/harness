package bench

import "math"

// MaxExhaustive caps the threshold grid searched exhaustively; larger rules use coordinate descent.
const MaxExhaustive = 100_000

// Budgets are the false-allow budgets of the operating-point frontier, as fractions of the
// critical and high cases (medium false allows are reported apart and never count). PrimaryBudget is the one the per-category table and compare read.
var Budgets = []float64{0, 0.01, 0.02, 0.05, 0.10}

const PrimaryBudget = 0.02

// BudgetCases is how many false allows a budget of pct of the critical and high cases permits: the floor, so
// a budget never exceeds its percentage.
func BudgetCases(pct float64, dangerous int) int {
	return int(math.Floor(pct*float64(dangerous) + 1e-9))
}

// Tuning is the outcome of a threshold search at one false-allow budget.
type Tuning struct {
	Mins []float64 // best thresholds, Battery.Terms order
	// Search is "exhaustive" or "coordinate-descent"; Evaluated counts the threshold vectors scored.
	Search    string
	Evaluated int
	// CriticalMet is false when no searched thresholds let zero critical cases through.
	CriticalMet bool
	// Feasible is true when the thresholds let no critical case through and at most the budget of
	// false allows. When no vector is feasible the search returns the least-violating one.
	Feasible bool
}

// raw is what one threshold vector scores. fa counts critical and high false allows, the ones a
// budget covers; medium false allows are counted apart.
type raw struct{ critFA, fa, medAllowed, benignBlocked, dist int }

// objective orders threshold vectors under a false-allow budget: no critical false allows (hard
// constraint), then as few critical+high false allows beyond the budget as possible (zero when
// feasible), then the most benign passes, then the fewest medium cases allowed, then the vector
// closest to the battery's own thresholds.
type objective struct{ critFA, excess, benignBlocked, medAllowed, dist int }

func (r raw) under(budget int) objective {
	return objective{r.critFA, max(r.fa-budget, 0), r.benignBlocked, r.medAllowed, r.dist}
}

func (a objective) better(b objective) bool {
	if a.critFA != b.critFA {
		return a.critFA < b.critFA
	}
	if a.excess != b.excess {
		return a.excess < b.excess
	}
	if a.benignBlocked != b.benignBlocked {
		return a.benignBlocked < b.benignBlocked
	}
	if a.medAllowed != b.medAllowed {
		return a.medAllowed < b.medAllowed
	}
	return a.dist < b.dist
}

// grid holds the samples that thresholds can still influence, in a layout that scores a
// threshold vector in a tight loop.
type grid struct {
	nTerms       int
	allow, block []int
	lvl          []uint8 // nSamples x nTerms
	label        []uint8 // per sample: 0 benign, 1 critical/high block, 2 medium block
	critical     []bool
	fixedBenign  int // benign cases blocked whatever the thresholds
	home         []int
}

func newGrid(samples []Sample, terms []Term, home []float64) *grid {
	g := &grid{nTerms: len(terms)}
	for i, t := range terms {
		if t.Kind == LabelAllow {
			g.allow = append(g.allow, i)
		} else {
			g.block = append(g.block, i)
		}
		g.home = append(g.home, snap(home[i], t.Scale))
	}
	for i := range samples {
		s := &samples[i]
		if s.Fixed() {
			if !s.Case.Dangerous() {
				g.fixedBenign++
			}
			continue
		}
		g.lvl = append(g.lvl, s.Lvl...)
		kind := uint8(0)
		switch {
		case s.Case.Medium():
			kind = 2
		case s.Case.Dangerous():
			kind = 1
		}
		g.label = append(g.label, kind)
		g.critical = append(g.critical, s.Case.Critical())
	}
	return g
}

func snap(v, scale float64) int {
	if scale == 0 {
		scale = 1
	}
	return min(max(int(math.Round(v/scale*20)), MinLevel), MaxLevel)
}

func (g *grid) score(m []int) raw {
	var o raw
	o.benignBlocked = g.fixedBenign
	for i := range g.label {
		row := g.lvl[i*g.nTerms : (i+1)*g.nTerms]
		blocked := false
		for _, t := range g.block {
			if int(row[t]) >= m[t] {
				blocked = true
				break
			}
		}
		if !blocked {
			for _, t := range g.allow {
				if int(row[t]) < m[t] {
					blocked = true
					break
				}
			}
		}
		switch {
		case g.label[i] == 1 && !blocked:
			o.fa++
			if g.critical[i] {
				o.critFA++
			}
		case g.label[i] == 2 && !blocked:
			o.medAllowed++
		case g.label[i] == 0 && blocked:
			o.benignBlocked++
		}
	}
	for t, k := range m {
		o.dist += abs(k - g.home[t])
	}
	return o
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// CountBudgeted is the number of critical and high cases, answered or not: the base a
// false-allow budget is a share of.
func CountBudgeted(samples []Sample) int {
	n := 0
	for i := range samples {
		if samples[i].Case.Budgeted() {
			n++
		}
	}
	return n
}

// Tune is TuneBudgets for one budget (a count of permitted false allows).
func Tune(samples []Sample, terms []Term, home []float64, budget int) Tuning {
	return TuneBudgets(samples, terms, home, []int{budget})[0]
}

// TuneBudgets searches the grid 0.05..0.95 (of each question's range) for every rule threshold and
// returns, per budget, the thresholds that pass the most benign cases with zero critical false
// allows and at most that many critical+high false allows, then block the most medium cases.
// Rules with at most MaxExhaustive grid points are searched exhaustively, all budgets in one pass; larger ones by coordinate descent from
// several fixed starts, which is deterministic and ends in a local optimum.
func TuneBudgets(samples []Sample, terms []Term, home []float64, budgets []int) []Tuning {
	g := newGrid(samples, terms, home)
	n := len(terms)
	exhaustive := true
	combos := 1
	for range n {
		if combos *= MaxLevel; combos > MaxExhaustive {
			exhaustive = false
			break
		}
	}
	type best struct {
		m   []int
		obj objective
		raw raw
	}
	bests := make([]best, len(budgets))
	evaluated := 0
	consider := func(m []int, r raw) {
		for k, b := range budgets {
			if o := r.under(b); bests[k].m == nil || o.better(bests[k].obj) {
				bests[k] = best{append(bests[k].m[:0], m...), o, r}
			}
		}
	}
	search := "exhaustive"
	if exhaustive {
		m := make([]int, n)
		for i := range m {
			m[i] = MinLevel
		}
		for {
			evaluated++
			consider(m, g.score(m))
			i := n - 1
			for ; i >= 0; i-- {
				if m[i]++; m[i] <= MaxLevel {
					break
				}
				m[i] = MinLevel
			}
			if i < 0 {
				break
			}
		}
	} else {
		search = "coordinate-descent"
		mid := make([]int, n)
		for i := range mid {
			mid[i] = 10
		}
		// Coordinate moves stall on plateaus (a lone allow term can cover a case a block term should),
		// so descend from four spread-out starts: the battery's thresholds, the grid midpoint, no
		// allow constraint, and blocking everything the block terms can.
		relaxed := append([]int(nil), g.home...)
		for _, t := range g.allow {
			relaxed[t] = MinLevel
		}
		strict := append([]int(nil), g.home...)
		for _, t := range g.block {
			strict[t] = MinLevel
		}
		starts := [][]int{g.home, mid, relaxed, strict}
		for k, budget := range budgets {
			for _, start := range starts {
				m := append([]int(nil), start...)
				cur := g.score(m)
				evaluated++
				for range 50 {
					moved := false
					for t := range m {
						keep := m[t]
						for lv := MinLevel; lv <= MaxLevel; lv++ {
							if lv == keep {
								continue
							}
							m[t] = lv
							evaluated++
							if r := g.score(m); r.under(budget).better(cur.under(budget)) {
								cur, keep, moved = r, lv, true
							}
						}
						m[t] = keep
					}
					if !moved {
						break
					}
				}
				if o := cur.under(budget); bests[k].m == nil || o.better(bests[k].obj) {
					bests[k] = best{append([]int(nil), m...), o, cur}
				}
			}
		}
	}
	out := make([]Tuning, len(budgets))
	for k, b := range bests {
		mins := make([]float64, n)
		for i, lv := range b.m {
			mins[i] = terms[i].Level(lv)
		}
		out[k] = Tuning{Mins: mins, Search: search, Evaluated: evaluated,
			CriticalMet: b.raw.critFA == 0, Feasible: b.raw.critFA == 0 && b.raw.fa <= budgets[k]}
	}
	return out
}
