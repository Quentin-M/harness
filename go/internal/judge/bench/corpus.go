// Package bench scores a System One decision model against a labeled corpus of agent tool calls.
//
// Commands in the corpus are data: this package posts them to a judge endpoint as text and never
// executes, parses or expands them. A battery fixes the questions asked and the rule that turns
// the answers into allow or block; every threshold in the rule is swept to show how far tuning
// moves the numbers, and 2-fold cross-validation shows how much of that is overfitting.
package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Case is one labeled tool call. Tool "bash" carries a shell command; "eval" carries code in Language.
type Case struct {
	ID           string   `json:"id"`
	Tool         string   `json:"tool"`
	Command      string   `json:"command"`
	Language     string   `json:"language,omitempty"`
	Cwd          string   `json:"cwd"`
	UserMessages []string `json:"userMessages,omitempty"`
	Label        string   `json:"label"`
	Category     string   `json:"category"`
	Severity     string   `json:"severity,omitempty"`
	Note         string   `json:"note,omitempty"`
	Source       string   `json:"source"`
}

const (
	LabelAllow = "allow"
	LabelBlock = "block"

	SevCritical = "critical"
	SevHigh     = "high"
	SevMedium   = "medium"
)

// Dangerous reports whether the judge must block the case.
func (c Case) Dangerous() bool { return c.Label == LabelBlock }

// Medium reports whether the case is a medium-severity block: a manipulation attempt around a
// harmless action. Medium cases are reported apart and never count against a false-allow budget.
func (c Case) Medium() bool { return c.Label == LabelBlock && c.Severity == SevMedium }

// Budgeted reports whether letting the case through counts against the false-allow budget:
// critical and high blocks.
func (c Case) Budgeted() bool { return c.Dangerous() && !c.Medium() }

// Critical reports whether letting the case through is a hard failure.
func (c Case) Critical() bool { return c.Label == LabelBlock && c.Severity == SevCritical }

// Issue is one validation failure at File:Line.
type Issue struct {
	File string
	Line int
	Msg  string
}

func (i Issue) Error() string { return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Msg) }

// Issues is every validation failure found in one load; its Error lists one per line.
type Issues []Issue

func (is Issues) Error() string {
	lines := make([]string, len(is))
	for i, x := range is {
		lines[i] = x.Error()
	}
	return strings.Join(lines, "\n")
}

var kebab = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// LoadCorpus reads the cases under each path: a directory contributes every <category>.jsonl in it
// (in file-name order), a file contributes itself. Paths are merged in order and ids must be unique
// across all of them. Unknown fields, duplicate ids and inconsistent labels are rejected; all
// issues are returned together as Issues.
func LoadCorpus(paths ...string) ([]Case, error) {
	if len(paths) == 0 {
		return nil, errors.New("corpus: no path given")
	}
	var files []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("corpus: %w", err)
		}
		if !fi.IsDir() {
			if !strings.HasSuffix(p, ".jsonl") {
				return nil, fmt.Errorf("corpus: %s is not a .jsonl file or a directory", p)
			}
			files = append(files, p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, fmt.Errorf("corpus: %w", err)
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, fmt.Errorf("corpus: no *.jsonl files in %s", p)
		}
		for _, n := range names {
			files = append(files, filepath.Join(p, n))
		}
	}
	var (
		cases  []Case
		issues Issues
		seen   = map[string]string{} // id -> file:line
	)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("corpus: %w", err)
		}
		cs, is, err := readCases(f, path, strings.TrimSuffix(filepath.Base(path), ".jsonl"), seen)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("corpus: %s: %w", path, err)
		}
		cases, issues = append(cases, cs...), append(issues, is...)
	}
	if len(issues) > 0 {
		return nil, issues
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("corpus: %v hold no cases", paths)
	}
	return cases, nil
}

func readCases(r io.Reader, path, category string, seen map[string]string) ([]Case, Issues, error) {
	var (
		cases  []Case
		issues Issues
	)
	br := bufio.NewReader(r)
	for line := 1; ; line++ {
		raw, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, nil, err
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			c, msgs := parseCase(raw, category)
			if len(msgs) == 0 {
				where := fmt.Sprintf("%s:%d", path, line)
				if first, dup := seen[c.ID]; dup {
					msgs = append(msgs, fmt.Sprintf("duplicate id %q, first at %s", c.ID, first))
				} else {
					seen[c.ID] = where
				}
			}
			for _, m := range msgs {
				issues = append(issues, Issue{path, line, m})
			}
			if len(msgs) == 0 {
				cases = append(cases, c)
			}
		}
		if errors.Is(err, io.EOF) {
			return cases, issues, nil
		}
	}
}

func parseCase(raw []byte, category string) (Case, []string) {
	var c Case
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, []string{"invalid case: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return c, []string{"invalid case: trailing data after the JSON object"}
	}
	return c, validateCase(c, category)
}

func validateCase(c Case, category string) []string {
	var m []string
	add := func(f string, a ...any) { m = append(m, fmt.Sprintf(f, a...)) }
	switch {
	case c.ID == "":
		add("id is required")
	case !kebab.MatchString(c.ID):
		add("id %q is not kebab-case", c.ID)
	case !strings.HasPrefix(c.ID, category+"-"):
		add("id %q must start with the category %q", c.ID, category+"-")
	}
	if c.Category != category {
		add("category %q must equal the file name %q", c.Category, category)
	}
	switch c.Tool {
	case "bash":
		if c.Language != "" {
			add("language is only for tool eval")
		}
	case "eval":
		if c.Language != "python" && c.Language != "js" {
			add("tool eval needs language python or js, got %q", c.Language)
		}
	default:
		add("tool must be bash or eval, got %q", c.Tool)
	}
	if strings.TrimSpace(c.Command) == "" {
		add("command is required")
	}
	if !strings.HasPrefix(c.Cwd, "/") {
		add("cwd must be an absolute path, got %q", c.Cwd)
	}
	for i, u := range c.UserMessages {
		if strings.TrimSpace(u) == "" {
			add("userMessages[%d] is blank", i)
		}
	}
	switch c.Label {
	case LabelBlock:
		if c.Severity != SevCritical && c.Severity != SevHigh && c.Severity != SevMedium {
			add("label block needs severity critical, high or medium, got %q", c.Severity)
		}
	case LabelAllow:
		if c.Severity != "" {
			add("label allow must not carry a severity (got %q)", c.Severity)
		}
	default:
		add("label must be allow or block, got %q", c.Label)
	}
	if strings.TrimSpace(c.Source) == "" {
		add("source is required (authored, omp-classifier:<corpus> or a dataset id with its license)")
	} else if strings.HasPrefix(c.Source, "omp-classifier:") && strings.TrimPrefix(c.Source, "omp-classifier:") == "" {
		add("source omp-classifier: needs the corpus name")
	}
	return m
}
