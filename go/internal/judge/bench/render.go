package bench

import (
	"fmt"
	"strings"
)

const maxListedFalseAllows = 50

func pct(n, d int) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(d))
}

func frac(n, d int) string { return fmt.Sprintf("%d/%d", n, d) }

// codeSpan renders arbitrary text as one markdown code span on one line, truncated.
func codeSpan(s string) string {
	s = strings.NewReplacer("\r", `\r`, "\n", `\n`, "|", `\|`).Replace(s)
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…"
	}
	run, longest := 0, 0
	for _, c := range s {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if longest > 0 {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

func thresholdLine(ts []Threshold) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		name := t.Q
		if t.Label != "" {
			name += "=" + t.Label
		}
		parts[i] = fmt.Sprintf("%s %s ≥ %.2f", t.Kind, name, t.Min)
	}
	return strings.Join(parts, ", ")
}

func evalTable(e Eval) string {
	var b strings.Builder
	b.WriteString("| category | dangerous blocked | recall | critical blocked | false allows | medium allowed | benign passed | pass rate | latency p50/p95 ms | input tokens p50 | unavailable | invalid |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	row := func(name string, s Stats) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %s | %s | %.0f / %.0f | %.0f | %d | %d |\n", name,
			frac(s.Blocked, s.Dangerous), pct(s.Blocked, s.Dangerous), frac(s.CriticalBlocked, s.Critical),
			len(s.FalseAllows), frac(len(s.MediumAllowed), s.Medium), frac(s.BenignPassed, s.Benign), pct(s.BenignPassed, s.Benign),
			s.LatencyP50MS, s.LatencyP95MS, s.InputTokensP50, s.Unavailable, s.Invalid)
	}
	for _, c := range e.Categories {
		row(c.Category, c.Stats)
	}
	row("**overall**", e.Overall)
	return b.String()
}

func falseAllowList(s Stats) string {
	return caseList("False allows (critical and high)", s.FalseAllows) + caseList("Medium allowed", s.MediumAllowed)
}

func caseList(title string, cases []FalseAllow) string {
	if len(cases) == 0 {
		return title + ": none.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d):\n\n", title, len(cases))
	for i, f := range cases {
		if i == maxListedFalseAllows {
			fmt.Fprintf(&b, "- … %d more in the JSON report\n", len(cases)-i)
			break
		}
		fmt.Fprintf(&b, "- `%s` [%s] %s\n", f.ID, f.Severity, codeSpan(f.Command))
	}
	return b.String() + "\n"
}

func section(b *strings.Builder, title, thresholds string, e Eval) {
	fmt.Fprintf(b, "## %s\n\n", title)
	if thresholds != "" {
		fmt.Fprintf(b, "Thresholds: %s\n\n", thresholds)
	}
	b.WriteString(evalTable(e))
	b.WriteString("\n")
	b.WriteString(falseAllowList(e.Overall))
	b.WriteString("\n")
}

func thresholdCell(ts []Threshold) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		name := t.Q
		if t.Label != "" {
			name += "=" + t.Label
		}
		parts[i] = fmt.Sprintf("%s %s≥%.2g", strings.ToUpper(t.Kind[:1]), name, t.Min)
	}
	return strings.Join(parts, "; ")
}

func budgetLabel(f FrontierPoint) string {
	return fmt.Sprintf("%g%% (≤%d)", 100*f.BudgetPct, f.BudgetCases)
}

// RenderMarkdown is the human report: the battery's own thresholds, the operating-point frontier
// (in-sample next to 2-fold cross-validation), and the primary operating point by category.
func RenderMarkdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Judge bench: %s on %s (%s)\n\n", r.Model, r.Battery, r.Date)
	fmt.Fprintf(&b, "%d cases", r.Cases)
	if r.Only != "" {
		fmt.Fprintf(&b, " (category %s only)", r.Only)
	}
	fmt.Fprintf(&b, "; %d requests, %d served from cache. Unavailable or malformed answers count as blocked.\n\n", r.Requests, r.CacheHits)
	section(&b, "Battery thresholds", thresholdLine(r.Configured.Thresholds), r.Configured.Eval)

	b.WriteString("## Operating-point frontier\n\n")
	fmt.Fprintf(&b, "Each row maximizes benign passes with no critical false allow and at most the budget of critical and high false allows (a share of the critical and high cases; the count is in brackets), then blocks as many medium cases as it can. Thresholds: A = allow term, B = block term. %s search over %d threshold vectors. The cross-validated row tunes on one fold and tests on the other, pooled; compare it with the in-sample row to see overfitting.\n\n", r.Search, r.Evaluated)
	b.WriteString("| budget | set | thresholds | recall | critical blocked | false allows | medium allowed | benign pass rate | within budget |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, f := range r.Frontier {
		o := f.InSample.Eval.Overall
		within := "yes"
		if !f.InSample.Feasible {
			within = "NO (least violating)"
		}
		fmt.Fprintf(&b, "| %s | in-sample | %s | %s | %s | %d | %s | %s | %s |\n", budgetLabel(f), thresholdCell(f.InSample.Thresholds),
			pct(o.Blocked, o.Dangerous), frac(o.CriticalBlocked, o.Critical), len(o.FalseAllows), frac(len(o.MediumAllowed), o.Medium), pct(o.BenignPassed, o.Benign), within)
		c := f.CV.Eval.Overall
		fmt.Fprintf(&b, "| %s | 2-fold CV | per fold, below | %s | %s | %d | %s | %s | - |\n", budgetLabel(f),
			pct(c.Blocked, c.Dangerous), frac(c.CriticalBlocked, c.Critical), len(c.FalseAllows), frac(len(c.MediumAllowed), c.Medium), pct(c.BenignPassed, c.Benign))
	}
	b.WriteString("\nCross-validation folds:\n\n")
	for _, f := range r.Frontier {
		for _, fold := range f.CV.Folds {
			fmt.Fprintf(&b, "- %s, fold %d (tuned on %d cases, budget %d, tested on %d): %s. Train: %s critical blocked, %d false allows, %d medium allowed, %s benign passed. Test: %s critical blocked, %d false allows, %d medium allowed, %s benign passed.\n",
				budgetLabel(f), fold.Fold, fold.TrainCases, fold.BudgetCases, fold.TestCases, thresholdCell(fold.Thresholds),
				frac(fold.Train.CriticalBlocked, fold.Train.Critical), len(fold.Train.FalseAllows), len(fold.Train.MediumAllowed), frac(fold.Train.BenignPassed, fold.Train.Benign),
				frac(fold.Test.CriticalBlocked, fold.Test.Critical), len(fold.Test.FalseAllows), len(fold.Test.MediumAllowed), frac(fold.Test.BenignPassed, fold.Test.Benign))
		}
	}
	b.WriteString("\n")

	if p := r.Primary(); p != nil {
		crit := ""
		if !p.InSample.Feasible {
			crit = " Not within budget: no searched thresholds satisfy it, these are the least violating."
		}
		section(&b, fmt.Sprintf("Primary operating point: %g%% budget, at most %d false allows", 100*p.BudgetPct, p.BudgetCases),
			thresholdLine(p.InSample.Thresholds)+"."+crit, p.InSample.Eval)
		c := p.CV.Eval.Overall
		fmt.Fprintf(&b, "Cross-validated at this budget: recall %s, critical blocked %s, %d false allows, %d medium allowed, benign pass rate %s.\n",
			pct(c.Blocked, c.Dangerous), frac(c.CriticalBlocked, c.Critical), len(c.FalseAllows), len(c.MediumAllowed), pct(c.BenignPassed, c.Benign))
	}
	return b.String()
}

// RenderCompare puts reports side by side in one markdown table: the battery's own thresholds and
// the primary operating point, in-sample and cross-validated.
func RenderCompare(rs []*Report) string {
	heads := make([]string, len(rs))
	for i, r := range rs {
		heads[i] = fmt.Sprintf("%s · %s · %s", r.Model, r.Battery, r.Date)
		if r.Only != "" {
			heads[i] += " · " + r.Only
		}
	}
	var b strings.Builder
	b.WriteString("| metric | " + strings.Join(heads, " | ") + " |\n")
	b.WriteString("|---|" + strings.Repeat("---|", len(rs)) + "\n")
	row := func(name string, f func(r *Report) string) {
		cells := make([]string, len(rs))
		for i, r := range rs {
			cells[i] = f(r)
		}
		b.WriteString("| " + name + " | " + strings.Join(cells, " | ") + " |\n")
	}
	head := func(name string) { fmt.Fprintf(&b, "| **%s** |%s\n", name, strings.Repeat(" |", len(rs))) }
	group := func(name string, eval func(r *Report) (Eval, bool), rows ...string) {
		head(name)
		for _, which := range rows {
			row(which, func(r *Report) string {
				e, ok := eval(r)
				if !ok {
					return "-"
				}
				s := e.Overall
				switch which {
				case "recall":
					return frac(s.Blocked, s.Dangerous) + " (" + pct(s.Blocked, s.Dangerous) + ")"
				case "critical blocked":
					return frac(s.CriticalBlocked, s.Critical)
				case "false allows":
					return fmt.Sprint(len(s.FalseAllows))
				case "medium allowed":
					return frac(len(s.MediumAllowed), s.Medium)
				default:
					return frac(s.BenignPassed, s.Benign) + " (" + pct(s.BenignPassed, s.Benign) + ")"
				}
			})
		}
	}
	row("cases", func(r *Report) string { return fmt.Sprint(r.Cases) })
	row("latency p50 ms", func(r *Report) string { return fmt.Sprintf("%.0f", r.Configured.Eval.Overall.LatencyP50MS) })
	group("battery thresholds", func(r *Report) (Eval, bool) { return r.Configured.Eval, true }, "recall", "benign passed")
	group(fmt.Sprintf("primary point, %g%% budget, in-sample", 100*PrimaryBudget), func(r *Report) (Eval, bool) {
		if p := r.Primary(); p != nil {
			return p.InSample.Eval, true
		}
		return Eval{}, false
	}, "recall", "critical blocked", "false allows", "medium allowed", "benign passed")
	group(fmt.Sprintf("primary point, %g%% budget, 2-fold CV", 100*PrimaryBudget), func(r *Report) (Eval, bool) {
		if p := r.Primary(); p != nil {
			return p.CV.Eval, true
		}
		return Eval{}, false
	}, "recall", "critical blocked", "false allows", "medium allowed", "benign passed")
	row("primary thresholds", func(r *Report) string {
		if p := r.Primary(); p != nil {
			return strings.ReplaceAll(thresholdCell(p.InSample.Thresholds), "|", `\|`)
		}
		return "-"
	})
	return b.String()
}
