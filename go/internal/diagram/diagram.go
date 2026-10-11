// Package diagram lints the box-drawing diagrams in markdown so every line meets what it claims
// to connect to, on the one-glyph-one-cell grid the Communication rule draws on.
//
// A diagram is a fenced block with no info string (or text/txt), or a 4-space indented block, that
// holds at least one box-drawing glyph or arrowhead. A line holding `diagram-lint: off` skips the
// next diagram, for drawings that show a mistake on purpose. Each cell is one rune; a rune that is not one
// cell wide is itself an issue, so columns stay exact for every check after it.
package diagram

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxWidth is the widest a diagram line may render, in cells.
const MaxWidth = 80

type Issue struct {
	Line, Col int // 1-based, Col in cells
	Msg       string
}

func (i Issue) String() string { return fmt.Sprintf("%d:%d: %s", i.Line, i.Col, i.Msg) }

type arm uint8

const (
	up arm = 1 << iota
	down
	left
	right
)

var arms = map[rune]arm{}

func init() {
	for _, g := range []struct {
		runes string
		a     arm
	}{
		{"─━┄┅┈┉╌╍═", left | right},
		{"│┃┆┇┊┋╎╏║", up | down},
		{"┌┏╭╔┍┎╒╓", right | down},
		{"┐┓╮╗┑┒╕╖", left | down},
		{"└┗╰╚┕┖╘╙", up | right},
		{"┘┛╯╝┙┚╛╜", up | left},
		{"├┣╠┝┞┟┠┡┢╞╟", up | down | right},
		{"┤┫╣┥┦┧┨┩┪╡╢", up | down | left},
		{"┬┳╦┭┮┯┰┱┲╤╥", left | right | down},
		{"┴┻╩┵┶┷┸┹┺╧╨", left | right | up},
		{"┼╋╬┽┾┿╀╁╂╃╄╅╆╇╈╉╊╪╫", up | down | left | right},
		{"╴╸", left}, {"╶╺", right}, {"╵╹", up}, {"╷╻", down},
	} {
		for _, r := range g.runes {
			arms[r] = g.a
		}
	}
}

// heads maps an arrowhead to the arm its shaft must offer: ▶ needs a line on its left that reaches right.
var heads = map[rune]struct {
	dx, dy int
	want   arm
}{
	'▶': {-1, 0, right}, '◀': {1, 0, left}, '▼': {0, -1, down}, '▲': {0, 1, up},
}

func straight(a arm) bool { return a == left|right || a == up|down }

// Lint reports every issue in the diagrams of a markdown document.
func Lint(md string) []Issue {
	var out []Issue
	for _, b := range blocks(strings.Split(md, "\n")) {
		out = append(out, b.lint()...)
	}
	return out
}

type block struct {
	first int      // 0-based line of rows[0]
	rows  [][]rune // indent stripped
}

func blocks(lines []string) []block {
	var out []block
	var cur *block
	fence, lintFence, prevBlank, skip := "", false, true, false
	flush := func() {
		for cur != nil && len(cur.rows) > 0 && len(cur.rows[len(cur.rows)-1]) == 0 {
			cur.rows = cur.rows[:len(cur.rows)-1]
		}
		if cur != nil && cur.drawn() && !skip {
			out = append(out, *cur)
		}
		if cur != nil {
			skip = false
		}
		cur = nil
	}
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		if fence != "" {
			if strings.HasPrefix(t, fence) && strings.TrimSpace(t[len(fence):]) == "" {
				fence = ""
				flush()
			} else if lintFence {
				cur.rows = append(cur.rows, []rune(l))
			}
			continue
		}
		if strings.Contains(l, "diagram-lint: off") {
			flush()
			skip = true
			continue
		}
		if f := fenceOf(t); f != "" && len(l)-len(t) < 4 {
			flush()
			fence = f
			info := strings.TrimSpace(t[len(f):])
			lintFence = info == "" || info == "text" || info == "txt"
			if lintFence {
				cur = &block{first: i + 1}
			}
			prevBlank = false
			continue
		}
		indented := strings.HasPrefix(l, "    ")
		switch {
		case indented && (cur != nil || prevBlank):
			if cur == nil {
				cur = &block{first: i}
			}
			cur.rows = append(cur.rows, []rune(l[4:]))
		case strings.TrimSpace(l) == "" && cur != nil:
			cur.rows = append(cur.rows, nil) // a blank row inside an indented block
		default:
			flush()
		}
		prevBlank = strings.TrimSpace(l) == ""
	}
	flush()
	return out
}

func fenceOf(t string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(t, f) {
			return f[:1] + strings.Repeat(f[:1], len(t)-len(strings.TrimLeft(t, f[:1]))-1)
		}
	}
	return ""
}

func (b *block) drawn() bool {
	for _, r := range b.rows {
		for _, c := range r {
			if _, ok := arms[c]; ok {
				return true
			}
			if _, ok := heads[c]; ok {
				return true
			}
		}
	}
	return false
}

// edge stands for the rows beyond the block: a line may run off its top or bottom.
const edge rune = 0

func (b *block) at(x, y int) rune {
	if y < 0 || y >= len(b.rows) {
		return edge
	}
	if x < 0 || x >= len(b.rows[y]) {
		return ' '
	}
	return b.rows[y][x]
}

func (b *block) lint() []Issue {
	var out []Issue
	add := func(x, y int, f string, a ...any) {
		out = append(out, Issue{Line: b.first + y + 1, Col: x + 1, Msg: fmt.Sprintf(f, a...)})
	}
	for y, row := range b.rows {
		if n := len(row); n > MaxWidth {
			add(MaxWidth, y, "line is %d cells, max %d: go vertical or split the diagram", n, MaxWidth)
		}
		for x, c := range row {
			if why := unsafe(c); why != "" {
				add(x, y, "%U %s: shears every column after it", c, why)
				continue
			}
			if h, ok := heads[c]; ok {
				_, target := arms[b.at(x-h.dx, y-h.dy)] // `─ POST /pay ▶│`: the head butts into its target
				if arms[b.at(x+h.dx, y+h.dy)]&h.want == 0 && !target {
					add(x, y, "arrowhead %c has no line reaching it", c)
				}
				continue
			}
			a, ok := arms[c]
			if !ok {
				continue
			}
			for _, d := range []struct {
				a        arm
				dx, dy   int
				opposite arm
				name     string
			}{{up, 0, -1, down, "up"}, {down, 0, 1, up, "down"}, {left, -1, 0, right, "left"}, {right, 1, 0, left, "right"}} {
				if a&d.a == 0 {
					continue
				}
				n := b.at(x+d.dx, y+d.dy)
				na, isLine := arms[n]
				_, isHead := heads[n]
				switch {
				case isHead:
				case isLine && na&d.opposite == 0:
					add(x, y, "%c reaches %s into %c, which has no arm back: a column is off by one", c, d.name, n)
				case isLine:
				case n != ' ':
					// a label the line touches: a tree root, a header, an arrow label
				case !straight(a):
					add(x, y, "%c has an open %s arm: close the corner or junction", c, d.name)
				case d.a == up:
					add(x, y, "%c hangs from nothing above: start it at a ┬, ├, box edge or label", c)
				}
			}
		}
	}
	return out
}

// unsafe says why a rune is not one cell wide in a terminal, "" when it is.
func unsafe(r rune) string {
	switch {
	case r == '\t':
		return "tab"
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200D || (r >= 0xFE00 && r <= 0xFE0F):
		return "zero-width or combining mark"
	case r >= 0x1F000 && r <= 0x1FAFF:
		return "emoji, 2 cells wide"
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE4F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6, r >= 0x20000 && r <= 0x3FFFD:
		return "East Asian wide, 2 cells"
	}
	return ""
}
