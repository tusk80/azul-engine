package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tusk80/azul-engine/game"
)

func newTestServer() *Server {
	return New(Config{HashMB: 16, MoveTime: 100 * time.Millisecond})
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bestmove", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBestMove(t *testing.T) {
	state, err := os.ReadFile("../testdata/midround.json")
	if err != nil {
		t.Fatal(err)
	}
	rec := post(t, newTestServer(), `{"state":`+string(state)+`,"timeMs":200}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp bestMoveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var st game.State
	if err := json.Unmarshal(state, &st); err != nil {
		t.Fatal(err)
	}
	m, err := game.ParseMove(resp.Move.Text)
	if err != nil || !st.Legal(m) {
		t.Fatalf("returned move %q is not legal: %v", resp.Move.Text, err)
	}
	if len(resp.Lines) != 3 || resp.PV[0] != resp.Move.Text || resp.Reason == "" {
		t.Fatalf("incomplete response: %s", rec.Body)
	}
	if resp.Move.Source == "factory" && (resp.Move.Factory < 1 || resp.Move.Factory > 5) {
		t.Fatalf("bad factory number %d", resp.Move.Factory)
	}
}

func TestBestMoveErrors(t *testing.T) {
	h := newTestServer()
	tests := map[string]struct {
		body string
		code int
	}{
		"not json":      {`nope`, http.StatusBadRequest},
		"missing state": {`{"timeMs":10}`, http.StatusBadRequest},
		"bad state":     {`{"state":{"factories":[["purple"]]}}`, http.StatusBadRequest},
		"round over": {`{"state":{"factories":[],"center":[],"players":[` +
			`{"score":0,"wall":[".....",".....",".....",".....","....."],"lines":[{},{},{},{},{}],"floor":0},` +
			`{"score":0,"wall":[".....",".....",".....",".....","....."],"lines":[{},{},{},{},{}],"floor":0}]}}`,
			http.StatusUnprocessableEntity},
	}
	for name, tt := range tests {
		if rec := post(t, h, tt.body); rec.Code != tt.code {
			t.Errorf("%s: status %d, want %d: %s", name, rec.Code, tt.code, rec.Body)
		}
	}
}

func postTo(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func TestApplyPlaysAWholeRound(t *testing.T) {
	h := newTestServer()
	rec := postTo(t, h, "/new", `{"seed":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("/new: %d %s", rec.Code, rec.Body)
	}
	var start positionJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}

	// Play the first legal move until the round ends, then score and deal.
	st := start.State
	var moves []string
	for !st.RoundOver() {
		var ml game.MoveList
		st.GenMoves(&ml)
		moves = append(moves, ml.Moves[0].String())
		st.Apply(ml.Moves[0])
	}
	moves = append(moves, "score", "deal")
	body, _ := json.Marshal(map[string]any{"state": start.State, "moves": moves, "seed": 9})
	rec = postTo(t, h, "/apply", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("/apply: %d %s", rec.Code, rec.Body)
	}
	var resp applyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Positions) != len(moves)+1 {
		t.Fatalf("%d positions for %d steps", len(resp.Positions), len(moves))
	}
	n := len(resp.Positions)
	if p := resp.Positions[n-3]; !p.RoundOver || p.Move == nil {
		t.Fatalf("last move should end the round: %+v", p)
	}
	if p := resp.Positions[n-2]; p.Action != "score" || p.State.Round != 2 {
		t.Fatalf("score step: %+v", p)
	}
	if p := resp.Positions[n-1]; p.Action != "deal" || p.RoundOver {
		t.Fatalf("deal step should give a new round: %+v", p)
	}
}

func TestApplyRejects(t *testing.T) {
	h := newTestServer()
	state, _ := os.ReadFile("../testdata/midround.json")
	for _, moves := range []string{`["F1 red->1"]`, `["score"]`, `["deal"]`, `["nonsense"]`} {
		rec := postTo(t, h, "/apply", `{"state":`+string(state)+`,"moves":`+moves+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", moves, rec.Code, rec.Body)
		}
	}
}

func TestUIServed(t *testing.T) {
	h := newTestServer()
	for _, path := range []string{"/", "/app.js", "/style.css", "/icon.svg", "/og.png", "/config"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("GET %s: %d", path, rec.Code)
		}
	}
}

func TestCORS(t *testing.T) {
	preflight := func(s *Server, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/bestmove", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Private-Network", "true")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	const site = "https://buddyboardgames.com"

	open := New(Config{HashMB: 1, CORSOrigins: []string{"*"}})
	rec := preflight(open, site)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
		rec.Header().Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatalf("wildcard preflight: %d %v", rec.Code, rec.Header())
	}

	listed := New(Config{HashMB: 1, CORSOrigins: []string{site}})
	if got := preflight(listed, site).Header().Get("Access-Control-Allow-Origin"); got != site {
		t.Fatalf("listed origin: got %q", got)
	}
	if got := preflight(listed, "https://evil.example").Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unlisted origin was allowed: %q", got)
	}

	closed := New(Config{HashMB: 1})
	if got := preflight(closed, site).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("same-origin server allowed %q", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

func TestMoveTimeIsCapped(t *testing.T) {
	s := New(Config{HashMB: 16, MaxMoveTime: 100 * time.Millisecond})
	state, _ := os.ReadFile("../testdata/opening.json")
	start := time.Now()
	rec := post(t, s, `{"state":`+string(state)+`,"timeMs":20000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("a 20 s request ran %v despite a 100 ms cap", el)
	}
}

func TestBusyReturns503(t *testing.T) {
	s := New(Config{HashMB: 1, QueueWait: 20 * time.Millisecond})
	eng := <-s.engines // someone else is searching
	defer func() { s.engines <- eng }()
	state, _ := os.ReadFile("../testdata/midround.json")
	rec := post(t, s, `{"state":`+string(state)+`}`)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d, want 503 with Retry-After: %s", rec.Code, rec.Body)
	}
}

func TestSearchRateLimit(t *testing.T) {
	s := New(Config{HashMB: 16, MoveTime: 5 * time.Millisecond, SearchesPerMinute: 6}) // burst of 5
	state, _ := os.ReadFile("../testdata/midround.json")
	body := `{"state":` + string(state) + `}`
	send := func(addr string) int {
		req := httptest.NewRequest(http.MethodPost, "/bestmove", strings.NewReader(body))
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range 5 {
		if code := send("203.0.113.5:1000"); code != http.StatusOK {
			t.Fatalf("request %d: status %d", i+1, code)
		}
	}
	if code := send("203.0.113.5:1001"); code != http.StatusTooManyRequests {
		t.Fatalf("6th request: status %d, want 429", code)
	}
	if code := send("203.0.113.9:1000"); code != http.StatusOK {
		t.Fatalf("another client was limited too: %d", code)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	// The client forged the first entry; our proxy appended the real one.
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7")
	r.Header.Set("CF-Connecting-IP", "192.0.2.44")
	tests := []struct{ header, want string }{
		{"", "10.0.0.1"},
		{"X-Forwarded-For", "198.51.100.7"},
		{"CF-Connecting-IP", "192.0.2.44"},
		{"X-Real-IP", "10.0.0.1"}, // configured but absent: fall back
	}
	for _, tt := range tests {
		if got := clientIP(r, tt.header); got != tt.want {
			t.Errorf("header %q: got %q, want %q", tt.header, got, tt.want)
		}
	}
}
