// Package api is the engine's request/response layer, free of any
// transport. The HTTP server and the WebAssembly build both call it, so a
// position analysed in the browser and on a server gives the same answer in
// the same shape.
package api

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tusk80/azul-engine/eval"
	"github.com/tusk80/azul-engine/game"
	"github.com/tusk80/azul-engine/search"
)

// Error is a request that cannot be answered; Status is an HTTP status code.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

const (
	statusBadRequest = 400
	statusNoMoves    = 422
)

func badRequest(format string, a ...any) *Error {
	return &Error{Status: statusBadRequest, Msg: fmt.Sprintf(format, a...)}
}

// ParseState decodes and validates a position.
func ParseState(raw json.RawMessage) (game.State, error) {
	var st game.State
	if len(raw) == 0 {
		return st, badRequest(`request: missing "state"`)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, badRequest("state: %v", err)
	}
	return st, nil
}

type BestMoveRequest struct {
	State   json.RawMessage `json:"state"`
	TimeMs  int             `json:"timeMs"`  // 0 = the default
	Depth   int             `json:"depth"`   // 0 = no limit
	MultiPV int             `json:"multiPV"` // 0 = 3
}

// Options turns the request into search options, using def when the request
// names no time and never exceeding max.
func (r *BestMoveRequest) Options(def, max time.Duration) search.Options {
	opt := search.Options{MoveTime: def, MaxDepth: r.Depth, MultiPV: 3}
	if r.TimeMs > 0 {
		opt.MoveTime = time.Duration(r.TimeMs) * time.Millisecond
	}
	opt.MoveTime = min(opt.MoveTime, max)
	if r.MultiPV > 0 {
		opt.MultiPV = min(r.MultiPV, 8)
	}
	return opt
}

type Effect struct {
	Tiles        int  `json:"tiles"`
	Placed       int  `json:"placed"`
	ToFloor      int  `json:"toFloor"`
	Token        bool `json:"token"`
	CompleteLine bool `json:"completeLine"`
	WallPoints   int  `json:"wallPoints"`
	FloorDelta   int  `json:"floorDelta"`
	Bonus        int  `json:"bonus"`
}

type Move struct {
	Text    string `json:"text"`              // "F2 black->1"
	Source  string `json:"source"`            // "factory" or "center"
	Factory int    `json:"factory,omitempty"` // 1-5
	Color   string `json:"color"`
	Line    int    `json:"line,omitempty"` // 1-5; absent for floor
	Floor   bool   `json:"floor"`
	Effect  Effect `json:"effect"`
}

type Line struct {
	Move     Move     `json:"move"`
	Eval     float64  `json:"eval"`              // points for the side to move
	Outcome  string   `json:"outcome,omitempty"` // "win" or "loss" when the game is decided
	EvalText string   `json:"evalText"`
	PV       []string `json:"pv"`
}

type BestMoveResponse struct {
	Line
	Reason string `json:"reason"`
	Lines  []Line `json:"lines"`
	Depth  int    `json:"depth"`
	Exact  bool   `json:"exact"`
	Nodes  uint64 `json:"nodes"`
	TimeMs int64  `json:"timeMs"`
}

// BestMove searches st with eng and describes the result.
func BestMove(eng *search.Searcher, st *game.State, opt search.Options) (*BestMoveResponse, error) {
	res := eng.Search(st, opt)
	if len(res.Lines) == 0 {
		return nil, &Error{Status: statusNoMoves, Msg: "no legal moves: the round or game is over"}
	}
	resp := &BestMoveResponse{
		Depth: res.Depth, Exact: res.Exact, Nodes: res.Nodes, TimeMs: res.Elapsed.Milliseconds(),
	}
	for _, l := range res.Lines {
		resp.Lines = append(resp.Lines, toLine(st, l))
	}
	resp.Line = resp.Lines[0]
	resp.Reason = Reason(st, res.Lines[0])
	return resp, nil
}

func toLine(st *game.State, l search.Line) Line {
	out := Line{Move: toMove(st, l.Move), EvalText: eval.Format(l.Score)}
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

func toMove(st *game.State, m game.Move) Move {
	e := st.MoveEffect(m)
	out := Move{
		Text:  m.String(),
		Color: m.Color().String(),
		Effect: Effect{
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

// Position is one position on a line of play, with the step that led to it.
type Position struct {
	Move      *Move      `json:"move,omitempty"`   // the move that led here
	Action    string     `json:"action,omitempty"` // or "score" / "deal"
	State     game.State `json:"state"`
	RoundOver bool       `json:"roundOver"`
	GameOver  bool       `json:"gameOver"`
	Winner    *int       `json:"winner,omitempty"` // 0 or 1; -1 = shared; only when the game is over
}

func position(st game.State, m *Move, action string) Position {
	p := Position{Move: m, Action: action, State: st, RoundOver: st.RoundOver(), GameOver: st.GameOver}
	if st.GameOver {
		w := st.Winner()
		p.Winner = &w
	}
	return p
}

type ApplyRequest struct {
	State json.RawMessage `json:"state"`
	// Moves are played in order. Besides moves ("F2 red->3", "C blue->floor")
	// two actions are accepted at the end of a round: "score" tiles the
	// walls and scores the round, "deal" refills the factories.
	Moves []string `json:"moves"`
	Seed  uint64   `json:"seed"` // for "deal"; 0 = random
}

type ApplyResponse struct {
	Positions []Position `json:"positions"` // the start position, then one per step
}

// Apply validates a position (with no moves) or plays a line from it.
func Apply(req ApplyRequest) (*ApplyResponse, error) {
	st, err := ParseState(req.State)
	if err != nil {
		return nil, err
	}
	resp := &ApplyResponse{Positions: []Position{position(st, nil, "")}}
	for i, step := range req.Moves {
		switch step {
		case "score":
			if !st.RoundOver() || st.GameOver || !needsScoring(&st) {
				return nil, badRequest("step %d: there is no finished round to score", i+1)
			}
			st.ResolveRound()
			resp.Positions = append(resp.Positions, position(st, nil, "score"))
		case "deal":
			if !st.RoundOver() || st.GameOver || needsScoring(&st) {
				return nil, badRequest("step %d: score the round before dealing", i+1)
			}
			st.Refill(rand.New(rand.NewPCG(seedOrNow(req.Seed), uint64(i))))
			resp.Positions = append(resp.Positions, position(st, nil, "deal"))
		default:
			m, err := game.ParseMove(step)
			if err != nil || !st.Legal(m) {
				return nil, badRequest("step %d: illegal move %q", i+1, step)
			}
			mj := toMove(&st, m)
			st.Apply(m)
			resp.Positions = append(resp.Positions, position(st, &mj, ""))
		}
	}
	return resp, nil
}

// needsScoring reports whether a finished round has not been scored yet:
// scoring clears the floors and full lines and puts the token back.
func needsScoring(st *game.State) bool {
	if !st.TokenInCenter {
		return true
	}
	for p := range st.Players {
		pb := &st.Players[p]
		if pb.Floor > 0 {
			return true
		}
		for r, l := range pb.Lines {
			if int(l.Count) == r+1 {
				return true
			}
		}
	}
	return false
}

type NewRequest struct {
	Seed  uint64 `json:"seed"`  // 0 = random
	First int    `json:"first"` // starting player
}

// NewGame deals a fresh game.
func NewGame(req NewRequest) (*Position, error) {
	if req.First != 0 && req.First != 1 {
		return nil, badRequest("first must be 0 or 1")
	}
	st := game.NewGame(req.First)
	st.Refill(rand.New(rand.NewPCG(seedOrNow(req.Seed), 0)))
	p := position(st, nil, "deal")
	return &p, nil
}

func seedOrNow(seed uint64) uint64 {
	if seed == 0 {
		return uint64(time.Now().UnixNano())
	}
	return seed
}

// Config is what the page needs to know about the engine it talks to.
type Config struct {
	DefaultTimeMs int64  `json:"defaultTimeMs"`
	MaxTimeMs     int64  `json:"maxTimeMs"`
	Version       string `json:"version"`
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
