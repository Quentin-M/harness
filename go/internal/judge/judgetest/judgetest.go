// Package judgetest is a fake adapter decision server for tests.
package judgetest

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Server answers /health always, and /v1/models plus /v1/systemone the way a loaded model does.
type Server struct {
	Port int
	// Hits counts every request, so a test can prove nothing contacted the server.
	Hits atomic.Int64

	mu       sync.Mutex
	ts       *httptest.Server
	loaded   bool
	model    string
	badShape bool
	notJudge bool
	delay    time.Duration
	last     []byte
}

// LastDecision is the body of the latest /v1/systemone request, "" before any.
func (s *Server) LastDecision() []byte { s.mu.Lock(); defer s.mu.Unlock(); return s.last }

// NewServer starts a loopback server serving jev-9b-ft-v3, loaded and answering.
func NewServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{loaded: true, model: "jev-9b-ft-v3"}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Down)
	_, port, _ := net.SplitHostPort(s.ts.Listener.Addr().String())
	s.Port, _ = strconv.Atoi(port)
	return s
}

// Down stops listening, so connections to the port are refused like a judge that is not running.
func (s *Server) Down() {
	s.mu.Lock()
	ts := s.ts
	s.ts = nil
	s.mu.Unlock()
	if ts != nil {
		ts.Close()
	}
}

// Up listens again on the same port.
func (s *Server) Up() {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(s.Port))
	if err != nil {
		panic(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	ts.Listener = l
	ts.Start()
	s.mu.Lock()
	s.ts = ts
	s.mu.Unlock()
}

// SetLoaded models the cold phase: while false /health is still fine but decisions fail.
func (s *Server) SetLoaded(v bool) { s.mu.Lock(); s.loaded = v; s.mu.Unlock() }

// SetModel changes the served model id.
func (s *Server) SetModel(id string) { s.mu.Lock(); s.model = id; s.mu.Unlock() }

// SetBadShape makes decision replies carry no typed answers.
func (s *Server) SetBadShape(v bool) { s.mu.Lock(); s.badShape = v; s.mu.Unlock() }

// SetNotJudge makes every route but /health answer 404, like an unrelated local service.
func (s *Server) SetNotJudge(v bool) { s.mu.Lock(); s.notJudge = v; s.mu.Unlock() }

// SetDelay makes decision requests slow, like a GPU shared with another job.
func (s *Server) SetDelay(d time.Duration) { s.mu.Lock(); s.delay = d; s.mu.Unlock() }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Hits.Add(1)
	s.mu.Lock()
	loaded, model, bad, notJudge, delay := s.loaded, s.model, s.badShape, s.notJudge, s.delay
	s.mu.Unlock()
	switch {
	case r.URL.Path == "/health":
		w.Write([]byte(`{"status":"ok"}`))
	case notJudge:
		http.NotFound(w, r)
	case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
		state := "loading"
		if loaded {
			state = "ready"
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": model, "capabilities": []string{"decisions"}, "loaded": loaded, "state": state}}})
	case r.URL.Path == "/v1/systemone" && r.Method == http.MethodPost:
		if !loaded {
			http.Error(w, `{"error":{"message":"model loading"}}`, http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.last = body
		s.mu.Unlock()
		var req struct {
			Questions map[string]struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		if json.Unmarshal(body, &req) != nil || len(req.Questions) == 0 {
			http.Error(w, `{"error":{"message":"no questions"}}`, http.StatusBadRequest)
			return
		}
		time.Sleep(delay)
		answers := map[string]any{}
		for id, q := range req.Questions {
			switch {
			case bad:
				answers[id] = map[string]any{"type": q.Type}
			case q.Type == "noul":
				answers[id] = map[string]any{"type": "noul", "noul": 0.02}
			default:
				answers[id] = map[string]any{"type": "choice", "choice": "safe", "confidence": 0.98,
					"probabilities": map[string]float64{"safe": 0.98, "unsafe": 0.02}}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": model, "answers": answers})
	default:
		http.NotFound(w, r)
	}
}
