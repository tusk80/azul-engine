// Package server exposes the engine over HTTP.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/tusk80/azul-engine/eval"
	"github.com/tusk80/azul-engine/game"
	"github.com/tusk80/azul-engine/search"
)

const maxBody = 1 << 20

// Config sets how the server searches and how much it lets clients use.
// The zero value suits a single user on localhost.
type Config struct {
	Weights   *eval.Weights // nil = eval.Default
	Lookahead search.Lookahead

	Engines     int           // searches that may run at once; default 1
	HashMB      int           // transposition table per engine; default 64
	MoveTime    time.Duration // search time when a request does not ask; default 1s
	MaxMoveTime time.Duration // most a request may ask for; default 30s
	QueueWait   time.Duration // how long a request waits for a free engine; default 5s

	// SearchesPerMinute limits /bestmove per client address; other
	// requests get ten times as many. 0 = unlimited.
	SearchesPerMinute int
	// ClientIPHeader names the header your reverse proxy sets to the
	// visitor's address ("CF-Connecting-IP", "X-Forwarded-For"). Empty
	// means use the connection's address.
	ClientIPHeader string
	// CORSOrigins lists the sites whose pages may call the API; "*" allows
	// any. Empty means same-origin only.
	CORSOrigins []string
	// Log receives one line per API request (no client addresses). Nil
	// means no request log.
	Log *slog.Logger
}

// Server answers analysis requests.
type Server struct {
	cfg       Config
	engines   chan *search.Searcher
	searches  *limiter
	requests  *limiter
	anyOrigin bool
	mux       *http.ServeMux
}

func New(cfg Config) *Server {
	if cfg.Weights == nil {
		cfg.Weights = &eval.Default
	}
	if cfg.Engines <= 0 {
		cfg.Engines = 1
	}
	if cfg.HashMB <= 0 {
		cfg.HashMB = 64
	}
	if cfg.MoveTime <= 0 {
		cfg.MoveTime = time.Second
	}
	if cfg.MaxMoveTime <= 0 {
		cfg.MaxMoveTime = 30 * time.Second
	}
	if cfg.QueueWait <= 0 {
		cfg.QueueWait = 5 * time.Second
	}
	s := &Server{
		cfg:       cfg,
		engines:   make(chan *search.Searcher, cfg.Engines),
		searches:  newLimiter(cfg.SearchesPerMinute),
		requests:  newLimiter(cfg.SearchesPerMinute * 10),
		anyOrigin: slices.Contains(cfg.CORSOrigins, "*"),
		mux:       http.NewServeMux(),
	}
	for range cfg.Engines {
		e := search.New(cfg.HashMB)
		e.Weights, e.Lookahead = *cfg.Weights, cfg.Lookahead
		s.engines <- e
	}
	s.mux.HandleFunc("POST /bestmove", s.handleBestMove)
	s.mux.HandleFunc("POST /apply", s.handleApply)
	s.mux.HandleFunc("POST /new", s.handleNew)
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// The page reads its limits from here, so it only offers search times
	// this server will actually honour.
	s.mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"defaultTimeMs": cfg.MoveTime.Milliseconds(),
			"maxTimeMs":     cfg.MaxMoveTime.Milliseconds(),
			"version":       Version(),
		})
	})
	ui := uiHandler()
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Short cache: the files are tiny and a deploy should show up fast.
		w.Header().Set("Cache-Control", "public, max-age=300")
		ui.ServeHTTP(w, r)
	})
	return s
}

// Version is the short git revision the binary was built from, or "dev".
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	rev, dirty := "", false
	for _, kv := range info.Settings {
		switch kv.Key {
		case "vcs.revision":
			rev = kv.Value
		case "vcs.modified":
			dirty = kv.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	rev = rev[:min(len(rev), 7)]
	if dirty {
		rev += "+"
	}
	return rev
}

// statusWriter records the response status for the request log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// The page loads only its own scripts and styles and talks only to this
// server; the favicon is a data: URI.
const csp = "default-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", csp)

	if origin := r.Header.Get("Origin"); origin != "" && (s.anyOrigin || slices.Contains(s.cfg.CORSOrigins, origin)) {
		if s.anyOrigin {
			h.Set("Access-Control-Allow-Origin", "*")
		} else {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
		}
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		// A page on the public web calling a private address (the overlay
		// calling localhost) needs Chrome's Private Network Access header.
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			h.Set("Access-Control-Allow-Private-Network", "true")
		}
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		s.mux.ServeHTTP(w, r)
		return
	}
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	if s.requests.allow(clientIP(r, s.cfg.ClientIPHeader), start) {
		s.mux.ServeHTTP(sw, r)
	} else {
		tooMany(sw)
	}
	if s.cfg.Log != nil {
		s.cfg.Log.Info("request", "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	}
}

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "10")
	writeError(w, http.StatusTooManyRequests, errors.New("too many requests: slow down a little"))
}

// acquire waits for a free engine, giving up when the client goes away or
// the queue wait runs out.
func (s *Server) acquire(ctx context.Context) *search.Searcher {
	t := time.NewTimer(s.cfg.QueueWait)
	defer t.Stop()
	select {
	case e := <-s.engines:
		return e
	case <-ctx.Done():
	case <-t.C:
	}
	return nil
}

type bestMoveRequest struct {
	State   json.RawMessage `json:"state"`
	TimeMs  int             `json:"timeMs"`  // 0 = server default
	Depth   int             `json:"depth"`   // 0 = no limit
	MultiPV int             `json:"multiPV"` // 0 = 3
}

type effectJSON struct {
	Tiles        int  `json:"tiles"`
	Placed       int  `json:"placed"`
	ToFloor      int  `json:"toFloor"`
	Token        bool `json:"token"`
	CompleteLine bool `json:"completeLine"`
	WallPoints   int  `json:"wallPoints"`
	FloorDelta   int  `json:"floorDelta"`
	Bonus        int  `json:"bonus"`
}

type moveJSON struct {
	Text    string     `json:"text"`              // "F2 black->1"
	Source  string     `json:"source"`            // "factory" or "center"
	Factory int        `json:"factory,omitempty"` // 1-5
	Color   string     `json:"color"`
	Line    int        `json:"line,omitempty"` // 1-5; absent for floor
	Floor   bool       `json:"floor"`
	Effect  effectJSON `json:"effect"`
}

type lineJSON struct {
	Move     moveJSON `json:"move"`
	Eval     float64  `json:"eval"`              // points for the side to move
	Outcome  string   `json:"outcome,omitempty"` // "win" or "loss" when the game is decided
	EvalText string   `json:"evalText"`
	PV       []string `json:"pv"`
}

type bestMoveResponse struct {
	lineJSON
	Reason string     `json:"reason"`
	Lines  []lineJSON `json:"lines"`
	Depth  int        `json:"depth"`
	Exact  bool       `json:"exact"`
	Nodes  uint64     `json:"nodes"`
	TimeMs int64      `json:"timeMs"`
}

func (s *Server) handleBestMove(w http.ResponseWriter, r *http.Request) {
	var req bestMoveRequest
	if !decode(w, r, &req) {
		return
	}
	st, err := parseState(req.State)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	opt := search.Options{Ctx: r.Context(), MoveTime: s.cfg.MoveTime, MaxDepth: req.Depth, MultiPV: 3}
	if req.TimeMs > 0 {
		opt.MoveTime = time.Duration(req.TimeMs) * time.Millisecond
	}
	opt.MoveTime = min(opt.MoveTime, s.cfg.MaxMoveTime)
	if req.MultiPV > 0 {
		opt.MultiPV = min(req.MultiPV, 8)
	}

	if !s.searches.allow(clientIP(r, s.cfg.ClientIPHeader), time.Now()) {
		tooMany(w)
		return
	}
	eng := s.acquire(r.Context())
	if eng == nil {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, errors.New("the engine is busy: try again in a moment"))
		return
	}
	res := eng.Search(&st, opt)
	s.engines <- eng
	if r.Context().Err() != nil {
		return // the client left; nobody to answer
	}

	if len(res.Lines) == 0 {
		writeError(w, http.StatusUnprocessableEntity, errors.New("no legal moves: the round or game is over"))
		return
	}
	resp := bestMoveResponse{
		Depth: res.Depth, Exact: res.Exact, Nodes: res.Nodes, TimeMs: res.Elapsed.Milliseconds(),
	}
	for _, l := range res.Lines {
		resp.Lines = append(resp.Lines, toLineJSON(&st, l))
	}
	resp.lineJSON = resp.Lines[0]
	resp.Reason = Reason(&st, res.Lines[0])
	writeJSON(w, http.StatusOK, resp)
}

func toLineJSON(st *game.State, l search.Line) lineJSON {
	out := lineJSON{Move: toMoveJSON(st, l.Move), EvalText: eval.Format(l.Score)}
	v := l.Score
	switch {
	case v > eval.Win/2:
		out.Outcome, v = "win", v-eval.Win
	case v < -eval.Win/2:
		out.Outcome, v = "loss", v+eval.Win
	}
	out.Eval = float64(v) / eval.Scale
	for _, m := range l.PV {
		out.PV = append(out.PV, m.String())
	}
	return out
}

func toMoveJSON(st *game.State, m game.Move) moveJSON {
	e := st.MoveEffect(m)
	out := moveJSON{
		Text:  m.String(),
		Color: m.Color().String(),
		Effect: effectJSON{
			Tiles: e.Tiles, Placed: e.Placed, ToFloor: e.ToFloor, Token: e.Token,
			CompleteLine: e.CompleteLine, WallPoints: e.WallPoints, FloorDelta: e.FloorDelta, Bonus: e.Bonus,
		},
	}
	if m.Src() == game.SrcCenter {
		out.Source = "center"
	} else {
		out.Source, out.Factory = "factory", m.Src()+1
	}
	if m.Dst() == game.DstFloor {
		out.Floor = true
	} else {
		out.Line = m.Dst() + 1
	}
	return out
}

// Reason is a one-line explanation of the best move: what it does now, and
// what the principal variation costs the opponent in floor tiles.
func Reason(st *game.State, l search.Line) string {
	m := l.Move
	e := st.MoveEffect(m)
	var parts []string
	switch {
	case e.CompleteLine:
		p := fmt.Sprintf("completes line %d (+%d", m.Dst()+1, e.WallPoints)
		if e.Bonus > 0 {
			p += fmt.Sprintf(", bonus +%d", e.Bonus)
		}
		parts = append(parts, p+")")
	case e.Placed > 0:
		parts = append(parts, fmt.Sprintf("builds line %d", m.Dst()+1))
	}
	var floor []string
	if e.ToFloor > 0 {
		floor = append(floor, fmt.Sprintf("%d to floor", e.ToFloor))
	}
	if e.Token {
		floor = append(floor, "takes first player")
	}
	if len(floor) > 0 {
		parts = append(parts, fmt.Sprintf("%s (%d)", strings.Join(floor, " and "), e.FloorDelta))
	}

	// Walk the PV and count the opponent's forced floor tiles.
	c := *st
	me := c.ToMove
	oppFloor := 0
	for _, pm := range l.PV {
		if c.ToMove != me {
			oppFloor += c.MoveEffect(pm).ToFloor
		}
		c.Apply(pm)
	}
	if oppFloor > 0 {
		parts = append(parts, fmt.Sprintf("main line: opponent drops %d on the floor", oppFloor))
	}
	if len(parts) == 0 {
		parts = append(parts, "best by search")
	}
	return strings.Join(parts, ", ") + "; eval " + eval.Format(l.Score)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("request: %w", err))
		return false
	}
	return true
}

func parseState(raw json.RawMessage) (game.State, error) {
	var st game.State
	if len(raw) == 0 {
		return st, errors.New(`request: missing "state"`)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, fmt.Errorf("state: %w", err)
	}
	return st, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
