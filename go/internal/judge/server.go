package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"syscall"
	"time"
)

// errSlow marks a judge that answers correctly but too slowly for the gate to use.
var errSlow = errors.New("judge too slow")

// errDown means nothing listens on the port; errForeign means something does that is no judge.
var (
	errDown    = errors.New("no server listening")
	errForeign = errors.New("not the judge")
)

// gate is the part of the gate battery the probe needs: every question verbatim (the request body
// carries them as the plugin sends them) and the declared type of each question the rule reads.
type gate struct {
	questions map[string]json.RawMessage
	asked     map[string]string // rule question -> declared type
}

// loadGate reads the committed gate battery. The probe must ask what the plugin asks.
func (l *Local) loadGate() (*gate, error) {
	data, err := os.ReadFile(l.GatePath())
	if err != nil {
		return nil, fmt.Errorf("gate battery missing (%v)", err)
	}
	var raw struct {
		Questions map[string]json.RawMessage `json:"questions"`
		Rule      struct {
			Allow []struct{ Q string } `json:"allow"`
			Block []struct{ Q string } `json:"block"`
		} `json:"rule"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || len(raw.Questions) == 0 {
		return nil, fmt.Errorf("gate battery %s is unreadable (%v)", l.GatePath(), err)
	}
	g := &gate{questions: raw.Questions, asked: map[string]string{}}
	for _, r := range append(raw.Rule.Allow, raw.Rule.Block...) {
		var q struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw.Questions[r.Q], &q) != nil || q.Type == "" {
			return nil, fmt.Errorf("gate battery %s: rule reads question %q, which it does not declare", l.GatePath(), r.Q)
		}
		g.asked[r.Q] = q.Type
	}
	if len(g.asked) == 0 {
		return nil, fmt.Errorf("gate battery %s has no rule", l.GatePath())
	}
	return g, nil
}

// probeBody is the gate battery's request in the System One wire shape the plugin speaks, for the
// state of a harmless command.
func (g *gate) probeBody(model string) []byte {
	b, _ := json.Marshal(map[string]any{"model": model,
		"state":     map[string]string{"tool": "bash", "workingDirectory": "/", "command": "true"},
		"questions": g.questions})
	return b
}

type answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          *float64           `json:"noul"`
	Score         *float64           `json:"score"`
}

// typed is an answer of the declared type that carries its value.
func (a answer) typed(want string) bool {
	if a.Type != want {
		return false
	}
	switch want {
	case "choice":
		return a.Choice != "" && len(a.Probabilities) > 0
	case "noul":
		return a.Noul != nil
	case "score":
		return a.Score != nil
	}
	return false
}

type modelEntry struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	Loaded       bool     `json:"loaded"`
	State        string   `json:"state"`
}

// ready proves the judge answers: the served model list names the pinned decision model, then the
// gate battery's own questions come back as typed answers of their declared types. /health alone is not proof: it answers
// before the model is loaded. A model that was already loaded and still needs longer than l.Fast
// is errSlow, since the plugin gives a whole classification 8 s and fails closed after it; a model
// still loading may take as long as it takes.
func (l *Local) ready(timeout time.Duration) (model string, err error) {
	g, err := l.loadGate()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var list struct {
		Data []modelEntry `json:"data"`
	}
	if err := l.call(ctx, http.MethodGet, "/v1/models", nil, &list); err != nil {
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			return "", fmt.Errorf("%w on port %d", errDown, l.Port)
		case errors.Is(err, context.DeadlineExceeded), isUnavailable(err):
			return "", err
		}
		return "", fmt.Errorf("%w: port %d does not serve a model list (%v)", errForeign, l.Port, err)
	}
	var warm bool
	for _, m := range list.Data {
		if m.ID == ModelID && slices.Contains(m.Capabilities, "decisions") {
			model, warm = m.ID, m.Loaded && m.State == "ready"
		}
	}
	if model == "" {
		return "", fmt.Errorf("%w: port %d serves no %s decision model", errForeign, l.Port, ModelID)
	}
	var reply struct {
		Answers map[string]answer `json:"answers"`
	}
	start := time.Now()
	if err := l.call(ctx, http.MethodPost, "/v1/systemone", g.probeBody(model), &reply); err != nil {
		if warm && errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: no answer to a battery-sized request in %s, the gate needs under %s", errSlow, timeout, l.Fast)
		}
		return "", err
	}
	for q, typ := range g.asked {
		if !reply.Answers[q].typed(typ) {
			return "", fmt.Errorf("decision request returned no typed %s answer for %q", typ, q)
		}
	}
	if took := time.Since(start); took > l.Fast {
		if warm {
			return "", fmt.Errorf("%w: %s for a battery-sized request, the gate needs under %s", errSlow, took.Round(100*time.Millisecond), l.Fast)
		}
		return "", fmt.Errorf("model was still loading (%s)", took.Round(100*time.Millisecond))
	}
	return model, nil
}

func (l *Local) call(ctx context.Context, method, route string, body []byte, into any) error {
	req, err := http.NewRequestWithContext(ctx, method, l.baseURL()+route, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+keyValue)
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &statusError{method + " " + route, resp.StatusCode}
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s %s: %w", method, route, err)
	}
	return nil
}

type statusError struct {
	what string
	code int
}

func (e *statusError) Error() string { return fmt.Sprintf("%s: HTTP %d", e.what, e.code) }

// isUnavailable is a 5xx: a server that is up but not ready, such as one still loading its model.
func isUnavailable(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.code >= 500
}
