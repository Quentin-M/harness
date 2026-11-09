package bench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is what the judge is shown for one case.
type State struct {
	Tool             string   `json:"tool"`
	WorkingDirectory string   `json:"workingDirectory"`
	Command          string   `json:"command"`
	Language         string   `json:"language,omitempty"`
	UserMessages     []string `json:"userMessages,omitempty"`
	Notice           string   `json:"notice,omitempty"`
}

// StateOf is the state sent for c under a battery's notice.
func StateOf(c Case, notice string) State {
	return State{Tool: c.Tool, WorkingDirectory: c.Cwd, Command: c.Command, Language: c.Language,
		UserMessages: c.UserMessages, Notice: notice}
}

// Reply is a judge's answer to one case, in the form that is cached.
type Reply struct {
	Answers     map[string]json.RawMessage `json:"answers"`
	InputTokens int                        `json:"inputTokens"`
	LatencyMS   float64                    `json:"latencyMs"`
}

// Client posts decision requests to {Endpoint}/v1/systemone.
type Client struct {
	Endpoint string
	Model    string
	Key      string // Bearer key; none is sent when empty
	HTTP     *http.Client
	Timeout  time.Duration // per request; 0 means none
}

// NewClient checks the endpoint and returns a client that never follows redirects, so a
// 3xx cannot replay case text to another host.
func NewClient(endpoint, model, key string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q must be an http(s) URL", endpoint)
	}
	if model == "" {
		return nil, errors.New("model is required")
	}
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"), Model: model, Key: key, Timeout: timeout,
		HTTP: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

type wireRequest struct {
	Model     string          `json:"model"`
	State     State           `json:"state"`
	Questions json.RawMessage `json:"questions"`
}

type wireReply struct {
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// Ask posts one case. Any transport failure, non-200 status or body that is not a JSON object is
// an error; a well-formed reply with missing or bad answers is not (scoring fails those closed).
func (c *Client) Ask(ctx context.Context, st State, questions json.RawMessage) (*Reply, error) {
	body, err := json.Marshal(wireRequest{c.Model, st, questions})
	if err != nil {
		return nil, err
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	took := time.Since(start)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var w wireReply
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("reply is not a JSON object: %w", err)
	}
	return &Reply{Answers: w.Answers, InputTokens: w.Usage.InputTokens,
		LatencyMS: float64(took.Microseconds()) / 1000}, nil
}

// Cache stores replies on disk, one file per request key.
type Cache struct{ Dir string }

// CacheKey is sha256 over the model, the battery's questions and notice, and the case state.
// Thresholds and the rule are not part of it: re-scoring a rule never needs a new request.
func CacheKey(model string, b *Battery, st State) string {
	h := sha256.New()
	stj, _ := json.Marshal(st)
	for _, part := range [][]byte{[]byte(model), compactJSON(b.QuestionsJSON()), []byte(b.Notice), stj} {
		fmt.Fprintf(h, "%d:", len(part))
		h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func compactJSON(raw []byte) []byte {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return raw
	}
	return buf.Bytes()
}

func (c *Cache) path(key string) string { return filepath.Join(c.Dir, key[:2], key+".json") }

// Get returns the cached reply; an unreadable or corrupt entry is a miss.
func (c *Cache) Get(key string) (*Reply, bool) {
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, false
	}
	var r Reply
	if json.Unmarshal(data, &r) != nil {
		return nil, false
	}
	return &r, true
}

// Put stores r atomically.
func (c *Cache) Put(key string, r *Reply) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	return os.Rename(tmp.Name(), p)
}
