// Package transcript reads recent Claude, Codex and omp session transcripts down to their human and
// assistant prose, for jobs that mine many sessions at once.
//
// Transcripts run to hundreds of MB a day with multi-MB lines, so files stream line by line through
// one reused buffer per worker: memory is bounded by the longest line, never by the file.
package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxTurn = 4000

type Turn struct {
	Role string // "user" | "assistant"
	Text string
	At   time.Time
}

type Session struct {
	Harness string // "claude" | "codex" | "omp"
	ID      string
	Path    string
	Cwd     string
	Start   time.Time
	End     time.Time
	Turns   []Turn
}

type source struct {
	harness string
	// One directory level below the session root, so subagent transcripts nested deeper never match.
	glob string
	// The parent dir names the session's cwd, a lossy fallback when no line carries one.
	cwdDir bool
	line   func(*parse, []byte)
}

var sources = []source{
	{"claude", "claude/projects/*/*.jsonl", true, claudeLine},
	{"codex", "codex/sessions/*/*/*/*.jsonl", false, codexLine},
	{"omp", "omp/agent/sessions/*/*.jsonl", true, ompLine},
}

type file struct {
	src  *source
	path string
}

// Since returns sessions under state with at least one kept turn at or after t, oldest first.
// Turns before t are left out, so consecutive windows never repeat a turn.
func Since(state string, t time.Time) ([]Session, error) {
	var files []file
	for i := range sources {
		src := &sources[i]
		paths, err := filepath.Glob(filepath.Join(state, src.glob))
		if err != nil {
			return nil, fmt.Errorf("list %s transcripts: %w", src.harness, err)
		}
		for _, p := range paths {
			fi, err := os.Stat(p)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("stat transcript: %w", err)
			}
			if fi.Mode().IsRegular() && !fi.ModTime().Before(t) {
				files = append(files, file{src, p})
			}
		}
	}

	out := make([]Session, len(files))
	errs := make([]error, len(files))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(files)) {
		wg.Go(func() {
			r := reader{since: t}
			for i := int(next.Add(1)) - 1; i < len(files); i = int(next.Add(1)) - 1 {
				out[i], errs[i] = r.read(files[i])
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	out = slices.DeleteFunc(out, func(s Session) bool { return len(s.Turns) == 0 })
	slices.SortStableFunc(out, func(a, b Session) int { return a.Start.Compare(b.Start) })
	return out, nil
}

type reader struct {
	since time.Time
	br    *bufio.Reader
	buf   []byte
}

func (r *reader) read(f file) (Session, error) {
	fh, err := os.Open(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Session{}, nil
	}
	if err != nil {
		return Session{}, fmt.Errorf("open transcript: %w", err)
	}
	defer fh.Close()
	if r.br == nil {
		r.br = bufio.NewReaderSize(fh, 64<<10)
	} else {
		r.br.Reset(fh)
	}

	p := parse{Session: Session{Harness: f.src.harness, Path: f.path}, since: r.since}
	for {
		line, err := r.line()
		if len(line) > 0 {
			f.src.line(&p, line)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Session{}, fmt.Errorf("read transcript %s: %w", f.path, err)
		}
	}

	s := p.Session
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(f.path), ".jsonl")
	}
	if s.Cwd == "" && f.src.cwdDir {
		s.Cwd = filepath.Base(filepath.Dir(f.path))
	}
	if n := len(s.Turns); n > 0 {
		s.Start, s.End = s.Turns[0].At, s.Turns[n-1].At
	}
	return s, nil
}

// line returns the next line, valid until the following call. A line longer than the bufio buffer
// is assembled in r.buf, which keeps its capacity across lines and files.
func (r *reader) line() ([]byte, error) {
	chunk, err := r.br.ReadSlice('\n')
	if err != bufio.ErrBufferFull {
		return chunk, err
	}
	r.buf = append(r.buf[:0], chunk...)
	for {
		chunk, err = r.br.ReadSlice('\n')
		r.buf = append(r.buf, chunk...)
		if err != bufio.ErrBufferFull {
			return r.buf, err
		}
	}
}

type parse struct {
	Session
	since time.Time
	at    time.Time // last timestamp seen, for lines that lack one
}

func (p *parse) stamp(t time.Time) {
	if !t.IsZero() {
		p.at = t
	}
}

func (p *parse) setCwd(cwd string) {
	if p.Cwd == "" {
		p.Cwd = cwd
	}
}

func (p *parse) add(role string, c content) {
	if p.at.Before(p.since) {
		return
	}
	for _, b := range c {
		if b.Type != "text" && b.Type != "input_text" && b.Type != "output_text" {
			continue
		}
		text := strings.TrimSpace(b.Text)
		if text == "" || role == "user" && noise(text) {
			continue
		}
		p.Turns = append(p.Turns, Turn{Role: role, Text: clip(text, maxTurn), At: p.at})
	}
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// content is message content given either as a bare string or as typed blocks. Decoding into
// blocks skips tool payloads in place instead of copying them out as raw JSON.
type content []block

func (c *content) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*c = content{{Type: "text", Text: s}}
		return nil
	}
	return json.Unmarshal(data, (*[]block)(c))
}

type claudeRecord struct {
	Type                      string    `json:"type"`
	SessionID                 string    `json:"sessionId"`
	Cwd                       string    `json:"cwd"`
	Timestamp                 time.Time `json:"timestamp"`
	IsMeta                    bool      `json:"isMeta"`
	IsSidechain               bool      `json:"isSidechain"`
	IsCompactSummary          bool      `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool      `json:"isVisibleInTranscriptOnly"`
	Message                   struct {
		Role    string  `json:"role"`
		Content content `json:"content"`
	} `json:"message"`
}

func claudeLine(p *parse, line []byte) {
	var rec claudeRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	p.stamp(rec.Timestamp)
	p.setCwd(rec.Cwd)
	if p.ID == "" {
		p.ID = rec.SessionID
	}
	if rec.Type != "user" && rec.Type != "assistant" || rec.Message.Role != rec.Type ||
		rec.IsMeta || rec.IsSidechain || rec.IsCompactSummary || rec.IsVisibleInTranscriptOnly {
		return
	}
	p.add(rec.Message.Role, rec.Message.Content)
}

type codexRecord struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   struct {
		Type    string  `json:"type"`
		ID      string  `json:"id"`
		Cwd     string  `json:"cwd"`
		Role    string  `json:"role"`
		Phase   string  `json:"phase"`
		Content content `json:"content"`
	} `json:"payload"`
}

func codexLine(p *parse, line []byte) {
	var rec codexRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	p.stamp(rec.Timestamp)
	pl := &rec.Payload
	switch rec.Type {
	case "session_meta":
		p.Cwd = pl.Cwd
		if pl.ID != "" {
			p.ID = pl.ID
		}
	case "turn_context":
		p.setCwd(pl.Cwd)
	case "response_item":
		// Commentary is the interim narration between tool calls; final_answer carries the result.
		if pl.Type == "message" && (pl.Role == "user" || pl.Role == "assistant") && pl.Phase != "commentary" {
			p.add(pl.Role, pl.Content)
		}
	}
}

type ompRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Cwd       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Role        string  `json:"role"`
		Attribution string  `json:"attribution"`
		Content     content `json:"content"`
	} `json:"message"`
}

func ompLine(p *parse, line []byte) {
	var rec ompRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	p.stamp(rec.Timestamp)
	switch rec.Type {
	case "session":
		p.ID, p.Cwd = rec.ID, rec.Cwd
	case "message":
		m := &rec.Message
		if m.Role == "assistant" || m.Role == "user" && m.Attribution != "agent" {
			p.add(m.Role, m.Content)
		}
	}
}
