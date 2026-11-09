package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Quentin-M/harness/internal/judge/bench"
)

const (
	// GateEnv names the harness binary the omp extension spawns as `<binary> judge gate`.
	GateEnv = "HARNESS_JUDGE_GATE"

	// MaxCommand is the most bytes of one command or eval program the gate judges. The judge's
	// context is 4096 tokens; the gate battery's questions and prompt take about 1100 of them, the
	// user messages (MaxUserMessages x MaxUserMessageRunes) about 400, and 4000 bytes of code at
	// worst 2.5 bytes per token is 1600, which leaves headroom for the answers. A larger input is
	// blocked: judging a truncated program would approve what the judge never saw.
	MaxCommand = 4000
	// MaxCwd bounds the working directory sent along; a longer one is blocked as a bad request.
	MaxCwd = 512
	// MaxUserMessages is how many of the user's latest typed messages the judge sees.
	MaxUserMessages = 3
	// MaxUserMessageRunes truncates each of them; the user's intent is in its first words.
	MaxUserMessageRunes = 300
	// GateBudget is the whole time one decision may take, judge call included. The extension's
	// own timeout is 8 s, so the gate always answers (a block) before the extension gives up.
	GateBudget = 6 * time.Second

	maxRequestBytes = 1 << 20
	auditCommand    = 500 // bytes of the command kept in the audit line

	refusalTail = "Do not retry this command, or a reworded or obfuscated version of it, to get past the gate. " +
		"Pick a safer approach, or ask the user in chat; a retried command is judged again and sees the user's reply."
)

// commandCwd resolves bash's input.cwd the way omp's resolveToCwd does, so the judge sees the
// directory the command runs in: `~` expands, a relative path joins the session cwd, and a path of
// only slashes means the session cwd.
func commandCwd(dir, session string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if dir == "~" {
			dir = home
		} else if rest, ok := strings.CutPrefix(dir, "~/"); ok {
			dir = filepath.Join(home, rest)
		}
	}
	switch {
	case strings.Trim(dir, "/") == "":
		return session
	case filepath.IsAbs(dir):
		return filepath.Clean(dir)
	}
	return filepath.Join(session, dir)
}

// GateRequest is the one JSON object `judge gate` reads on stdin: omp's tool name and raw tool
// input, the session's working directory, and the user's latest typed messages. The gate picks the
// command (bash: input.command, eval: input.code and input.language) and the working directory
// (input.cwd wins over Cwd) itself.
type GateRequest struct {
	Tool         string         `json:"tool"`
	Input        map[string]any `json:"input"`
	Cwd          string         `json:"cwd"`
	UserMessages []string       `json:"userMessages,omitempty"`
}

// call is what a request resolves to: the state the judge is shown.
type call struct {
	tool, command, cwd, language string
	messages                     []string
}

// GateReply is the one JSON object it writes on stdout. The exit code is 0 for every decision.
type GateReply struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason"`
}

// Gate decides one tool call with the local judge and the committed gate battery. It never
// prompts: the answer is allow or a refusal for the agent.
type Gate struct {
	Root, State string
	Port        int
	HTTP        *http.Client
	Budget      time.Duration // GateBudget
	Now         func() time.Time
}

// AuditPath is the JSONL file that receives one line per decision.
func AuditPath(state string) string { return filepath.Join(state, "judge", "decisions.jsonl") }

// GatePathIn is the gate battery under a harness checkout.
func GatePathIn(root string) string {
	return filepath.Join(root, "bench", "judge", "gates", ModelID+".json")
}

// ExtensionPathIn is the omp extension that forwards tool calls to the gate.
func ExtensionPathIn(root string) string {
	return filepath.Join(root, "bench", "judge", "extension", "gate.ts")
}

// verdict is a decision plus what audit needs to know how it was reached.
type verdict struct {
	block     bool
	decidedBy string // request, judge, answer or rule
	why       string
	terms     map[string]float64
}

// Serve is `judge gate`: one request in, one reply out, whatever happens.
func (g *Gate) Serve(stdin io.Reader, stdout io.Writer) error {
	start := g.now()
	var c call
	v := g.decide(stdin, &c, start)
	reply := GateReply{}
	if v.block {
		reply.Reason = "Blocked by the local command gate: " + v.why + ". " + refusalTail
		reply.Block = true
	}
	g.audit(c, v, g.now().Sub(start))
	return json.NewEncoder(stdout).Encode(reply)
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Gate) decide(stdin io.Reader, c *call, start time.Time) verdict {
	bad := func(format string, a ...any) verdict {
		return verdict{block: true, decidedBy: "request", why: fmt.Sprintf(format, a...)}
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maxRequestBytes+1))
	switch {
	case err != nil:
		return bad("the request could not be read (%v)", err)
	case len(data) > maxRequestBytes:
		return bad("the request is larger than %d bytes", maxRequestBytes)
	}
	var req GateRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return bad("the request is not valid JSON (%v)", err)
	}
	*c = call{tool: req.Tool, cwd: req.Cwd, messages: req.UserMessages}
	field := "command"
	if req.Tool == "eval" {
		field = "code"
	}
	switch v := req.Input[field].(type) {
	case nil:
	case string:
		c.command = v
	default:
		return bad("the %s input %q is not a string", req.Tool, field)
	}
	if d, ok := req.Input["cwd"].(string); ok && d != "" && req.Tool == "bash" {
		c.cwd = commandCwd(d, req.Cwd)
	}
	if l, ok := req.Input["language"].(string); ok && req.Tool == "eval" {
		c.language = l
	}
	switch {
	case req.Tool != "bash" && req.Tool != "eval":
		return bad("tool %q is not bash or eval", req.Tool)
	case strings.TrimSpace(c.command) == "":
		return verdict{decidedBy: "request", why: "empty input, nothing to run"}
	case len(c.command) > MaxCommand:
		return bad("the %s input is %d bytes and the gate judges at most %d; split it into smaller steps, or ask the user", req.Tool, len(c.command), MaxCommand)
	case len(c.cwd) > MaxCwd:
		return bad("the working directory is longer than %d bytes", MaxCwd)
	}
	b, err := bench.LoadBattery(GatePathIn(g.Root))
	if err != nil {
		return verdict{block: true, decidedBy: "judge", why: fmt.Sprintf("the gate battery cannot be loaded (%v)", err)}
	}
	bc := bench.Case{Tool: c.tool, Command: c.command, Cwd: c.cwd, Language: c.language,
		UserMessages: clipMessages(c.messages)}
	budget := g.Budget
	if budget <= 0 {
		budget = GateBudget
	}
	client, err := bench.NewClient("http://127.0.0.1:"+strconv.Itoa(g.Port), ModelID, "", 0)
	if err != nil {
		return verdict{block: true, decidedBy: "judge", why: err.Error()}
	}
	if g.HTTP != nil {
		client.HTTP = g.HTTP
	}
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(budget))
	defer cancel()
	rep, err := client.Ask(ctx, bench.StateOf(bc, b.Notice), b.QuestionsJSON())
	if err != nil {
		reason := "the local judge did not answer (" + err.Error() + ")"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = fmt.Sprintf("the local judge did not answer within %s", budget)
		}
		return verdict{block: true, decidedBy: "judge", why: reason}
	}
	s := bench.Observe(b, bc, bench.Result{Reply: rep})
	v := verdict{terms: map[string]float64{}}
	if !s.Fixed() {
		for i, t := range b.Terms() {
			name := t.Q
			if t.Label != "" {
				name += "=" + t.Label
			}
			v.terms[name] = s.Vals[i]
		}
	}
	blocked, why := bench.Decide(b.Terms(), &s, b.Mins())
	v.block, v.why, v.decidedBy = blocked, why, "rule"
	switch {
	case s.BadAnswer != "":
		v.decidedBy = "answer"
	case blocked:
		v.why = "deciding term: " + why
	}
	return v
}

// clipMessages keeps the last MaxUserMessages non-empty messages, each cut to MaxUserMessageRunes.
func clipMessages(in []string) []string {
	var out []string
	for _, m := range in {
		if strings.TrimSpace(m) != "" {
			out = append(out, m)
		}
	}
	if len(out) > MaxUserMessages {
		out = out[len(out)-MaxUserMessages:]
	}
	for i, m := range out {
		if utf8.RuneCountInString(m) > MaxUserMessageRunes {
			out[i] = string([]rune(m)[:MaxUserMessageRunes])
		}
	}
	return out
}

type auditLine struct {
	TS        string             `json:"ts"`
	Tool      string             `json:"tool"`
	Cwd       string             `json:"cwd"`
	Command   string             `json:"command"`
	Decision  string             `json:"decision"`
	DecidedBy string             `json:"decidedBy"`
	Why       string             `json:"why"`
	Terms     map[string]float64 `json:"terms,omitempty"`
	MS        int64              `json:"ms"`
}

// audit appends one line. A log that cannot be written never changes the decision.
func (g *Gate) audit(c call, v verdict, took time.Duration) {
	cmd := Redact(c.command) // before the cut, so a secret straddling it is not half kept
	if len(cmd) > auditCommand {
		cmd = strings.ToValidUTF8(cmd[:auditCommand], "") + "…"
	}
	decision := "allow"
	if v.block {
		decision = "block"
	}
	line, err := json.Marshal(auditLine{TS: g.now().UTC().Format(time.RFC3339Nano), Tool: c.tool, Cwd: c.cwd,
		Command: cmd, Decision: decision, DecidedBy: v.decidedBy, Why: v.why, Terms: v.terms, MS: took.Milliseconds()})
	if err != nil {
		return
	}
	p := AuditPath(g.State)
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Chmod(0o600) // a log created by an earlier version was world-readable
	_, _ = f.Write(append(line, '\n'))
}

// Status is the text `/classifier` shows. state is "on" or "off", the extension's per-session switch.
func (g *Gate) Status(state string) string {
	lines := []string{"Command gate: " + state}
	if state == "off" {
		lines[0] += " for this session: bash and eval calls run unjudged (`/classifier on` restores it)"
	}
	return strings.Join(append(lines,
		"gate file: "+GatePathIn(g.Root),
		"judge: http://127.0.0.1:"+strconv.Itoa(g.Port),
		"audit log: "+AuditPath(g.State)), "\n")
}
