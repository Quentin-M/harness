package bench

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Env is what the CLI needs from the process.
type Env struct {
	State  string // $HARNESS_STATE; the cache lives in State/judge/bench-cache
	Root   string // harness checkout, base of the default bench/judge paths and fallback for relative ones; may be ""
	Cwd    string
	Key    string // Bearer key for the endpoint, from KeyEnv; none is sent when empty
	Now    func() time.Time
	Stdout io.Writer
	Stderr io.Writer
}

const usage = `usage:
  judge bench run --endpoint URL --model ID --battery FILE [--corpus PATH]... [--concurrency N]
                  [--only CATEGORY] [--timeout 60s] [--no-cache] [--reports DIR | --no-report]
  judge bench compare REPORT.json...`

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// KeyEnv names the env var holding the Bearer key for a remote endpoint under `judge bench run`.
const KeyEnv = "HARNESS_JUDGE_KEY"

// Main runs `judge bench <run|compare>`.
func Main(ctx context.Context, argv []string, e Env) error {
	if len(argv) == 0 {
		return errors.New(usage)
	}
	switch argv[0] {
	case "run":
		return runCmd(ctx, argv[1:], e)
	case "compare":
		return compareCmd(argv[1:], e)
	}
	return errors.New(usage)
}

func (e Env) locate(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(filepath.Join(e.Cwd, p)); err == nil || e.Root == "" {
		return filepath.Join(e.Cwd, p)
	}
	if rp := filepath.Join(e.Root, p); exists(rp) {
		return rp
	}
	return filepath.Join(e.Cwd, p)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func (e Env) benchPath(rel string) string {
	if e.Root != "" {
		return filepath.Join(e.Root, "bench", "judge", rel)
	}
	return filepath.Join(e.Cwd, "bench", "judge", rel)
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeName(s string) string { return strings.Trim(unsafeName.ReplaceAllString(s, "-"), "-.") }

func runCmd(ctx context.Context, argv []string, e Env) error {
	fs := flag.NewFlagSet("judge bench run", flag.ContinueOnError)
	fs.SetOutput(e.Stderr)
	fs.Usage = func() { fmt.Fprintln(e.Stderr, usage); fs.PrintDefaults() }
	var corpora multi
	endpoint := fs.String("endpoint", "", "judge base URL; requests go to URL/v1/systemone")
	model := fs.String("model", "", "model id sent to the judge")
	battery := fs.String("battery", "", "battery JSON file, e.g. bench/judge/batteries/compact-v1.json")
	fs.Var(&corpora, "corpus", "corpus dir or .jsonl file; repeatable (default bench/judge/corpus)")
	conc := fs.Int("concurrency", 2, "parallel requests")
	only := fs.String("only", "", "run one corpus category only")
	timeout := fs.Duration("timeout", 60*time.Second, "per-request timeout")
	noCache := fs.Bool("no-cache", false, "ignore cached replies and ask again")
	reports := fs.String("reports", "", "report dir (default bench/judge/reports)")
	noReport := fs.Bool("no-report", false, "print the report without writing files")
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q\n%s", fs.Arg(0), usage)
	}
	if *endpoint == "" || *model == "" || *battery == "" {
		return fmt.Errorf("--endpoint, --model and --battery are required\n%s", usage)
	}
	if *conc < 1 {
		return errors.New("--concurrency must be at least 1")
	}

	b, err := LoadBattery(e.locate(*battery))
	if err != nil {
		return err
	}
	paths := []string(corpora)
	if len(paths) == 0 {
		paths = []string{e.benchPath("corpus")}
	} else {
		for i, p := range paths {
			paths[i] = e.locate(p)
		}
	}
	cases, err := LoadCorpus(paths...)
	if err != nil {
		return err
	}
	if *only != "" {
		cases = filterCategory(cases, *only)
		if len(cases) == 0 {
			return fmt.Errorf("--only %q matches no category", *only)
		}
	}
	client, err := NewClient(*endpoint, *model, e.Key, *timeout)
	if err != nil {
		return err
	}
	runner := &Runner{Client: client, Concurrency: *conc, NoCache: *noCache, Log: e.Stderr,
		Cache: &Cache{Dir: filepath.Join(e.State, "judge", "bench-cache")}}
	results, counts, err := runner.Run(ctx, b, cases)
	if err != nil {
		return err
	}
	now := e.Now
	if now == nil {
		now = time.Now
	}
	rep := BuildReport(b, Samples(b, cases, results), Meta{Date: now().Format("2006-01-02"), Model: *model, Only: *only, Counts: counts})
	md := RenderMarkdown(rep)
	fmt.Fprint(e.Stdout, md)
	if counts.Unavailable > 0 {
		fmt.Fprintf(e.Stderr, "bench: %d cases got no reply (counted as blocked)\n", counts.Unavailable)
	}
	if *noReport {
		return nil
	}
	dir := *reports
	if dir == "" {
		dir = e.benchPath("reports")
	} else {
		dir = e.locate(dir)
	}
	base := fmt.Sprintf("%s-%s-%s", rep.Date, safeName(*model), safeName(b.Name))
	if *only != "" {
		base += "-only-" + safeName(*only)
	}
	js, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for ext, data := range map[string][]byte{".json": append(js, '\n'), ".md": []byte(md)} {
		if err := os.WriteFile(filepath.Join(dir, base+ext), data, 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(e.Stderr, "bench: wrote %s.{json,md}\n", filepath.Join(dir, base))
	return nil
}

func filterCategory(cases []Case, cat string) []Case {
	var out []Case
	for _, c := range cases {
		if c.Category == cat {
			out = append(out, c)
		}
	}
	return out
}

func compareCmd(argv []string, e Env) error {
	if len(argv) == 0 {
		return errors.New(usage)
	}
	var rs []*Report
	for _, p := range argv {
		r, err := LoadReport(e.locate(p))
		if err != nil {
			return err
		}
		rs = append(rs, r)
	}
	fmt.Fprint(e.Stdout, RenderCompare(rs))
	return nil
}
