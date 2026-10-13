package transcript

import (
	"fmt"
	"slices"
	"strings"
)

// Digest renders the session as compact text of at most max bytes, keeping the head and tail turns
// and marking the elided middle.
func (s Session) Digest(max int) string {
	header := fmt.Sprintf("## %s %s %s %s → %s\n\n", s.Harness, s.ID, s.Cwd,
		s.Start.UTC().Format("2006-01-02 15:04Z"), s.End.UTC().Format("15:04Z"))
	if len(header) >= max {
		return clip(header, max)
	}
	parts := make([]string, len(s.Turns))
	total := len(header)
	for i, t := range s.Turns {
		parts[i] = render(t, len(t.Text)+64)
		total += len(parts[i])
	}
	if total <= max {
		return header + strings.Join(parts, "")
	}

	n := len(parts)
	budget := max - len(header) - len(elision(n))
	if n == 1 || budget <= 0 {
		return header + render(s.Turns[0], max-len(header))
	}
	// The opening turns carry the ask and the closing ones the outcome; the middle goes first.
	head := []string{render(s.Turns[0], budget/2)}
	tail := []string{render(s.Turns[n-1], budget-len(head[0]))}
	used := len(head[0]) + len(tail[0])
	lo, hi := 1, n-1
	for lo < hi {
		grew := false
		if used+len(parts[lo]) <= budget {
			head = append(head, parts[lo])
			used += len(parts[lo])
			lo, grew = lo+1, true
		}
		if lo < hi && used+len(parts[hi-1]) <= budget {
			tail = append(tail, parts[hi-1])
			used += len(parts[hi-1])
			hi, grew = hi-1, true
		}
		if !grew {
			break
		}
	}
	slices.Reverse(tail)

	var b strings.Builder
	b.Grow(max)
	b.WriteString(header)
	for _, p := range head {
		b.WriteString(p)
	}
	if hi > lo {
		b.WriteString(elision(hi - lo))
	}
	for _, p := range tail {
		b.WriteString(p)
	}
	return b.String()
}

// render formats one turn in at most room bytes.
func render(t Turn, room int) string {
	prefix := t.At.UTC().Format("15:04") + " " + t.Role + ": "
	const sep = "\n\n"
	if room < len(prefix)+len(sep) {
		return ""
	}
	return prefix + clip(t.Text, room-len(prefix)-len(sep)) + sep
}

func elision(n int) string {
	return fmt.Sprintf("[… %d turns elided …]\n\n", n)
}
