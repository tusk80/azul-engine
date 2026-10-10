// Package server exposes the engine over HTTP.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/tusk80/azul-engine/internal/api"
	"github.com/tusk80/azul-engine/internal/eval"
	"github.com/tusk80/azul-engine/internal/search"
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
		writeJSON(w, http.StatusOK, api.Config{
			DefaultTimeMs: cfg.MoveTime.Milliseconds(),
			MaxTimeMs:     cfg.MaxMoveTime.Milliseconds(),
			Version:       api.Version(),
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
// server. Images may also be blob: URLs: the reference photo is shown from
// one and never leaves the browser.
const csp = "default-src 'self'; img-src 'self' data: blob:; connect-src 'self' https://api.anthropic.com https://generativelanguage.googleapis.com; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

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

func (s *Server) handleBestMove(w http.ResponseWriter, r *http.Request) {
	var req api.BestMoveRequest
	if !decode(w, r, &req) {
		return
	}
	st, err := api.ParseState(req.State)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	opt := req.Options(s.cfg.MoveTime, s.cfg.MaxMoveTime)
	opt.Ctx = r.Context()

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
	resp, err := api.BestMove(eng, &st, opt)
	s.engines <- eng
	if r.Context().Err() != nil {
		return // the client left; nobody to answer
	}
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleApply validates a position (with no moves) or plays a line from it.
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var req api.ApplyRequest
	if !decode(w, r, &req) {
		return
	}
	resp, err := api.Apply(req)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleNew deals a fresh game.
func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	var req api.NewRequest
	if !decode(w, r, &req) {
		return
	}
	pos, err := api.NewGame(req)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pos)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("request: %w", err))
		return false
	}
	return true
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

// writeAPIError answers with the status an api.Error carries.
func writeAPIError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var ae *api.Error
	if errors.As(err, &ae) {
		code = ae.Status
	}
	writeError(w, code, err)
}
