// Package search finds the best move within a round.
//
// A round is deterministic with perfect information, so it is searched with
// negamax/alpha-beta (PVS), iterative deepening and a transposition table.
// The end of the round is a leaf: it is resolved exactly and evaluated.
package search

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/tusk80/azul-engine/internal/eval"
	"github.com/tusk80/azul-engine/internal/game"
)

const (
	inf    = 2 * eval.Win
	maxPly = game.MaxRoundPly + 2
	noMove = game.Move(0xFFFF)
)

type Options struct {
	// Ctx stops the search when it is cancelled (after the first iteration,
	// so there is always a move). Nil means never.
	Ctx      context.Context
	MoveTime time.Duration // 0 = no time limit
	MaxDepth int           // plies; 0 = until the end of the round
	MaxNodes uint64        // 0 = no node limit; checked every 1024 nodes
	MultiPV  int           // number of root moves with exact scores; default 1
	OnDepth  func(Result)  // called after each completed iteration
}

// Line is one scored root move and its principal variation.
type Line struct {
	Move  game.Move
	Score int // eval units, from the root player's point of view
	PV    []game.Move
}

type Result struct {
	Lines []Line // best first, at most MultiPV
	Depth int
	Exact bool // the tree was searched to the end of the round
	// Lookahead reports that round-end positions were valued by sampling
	// next-round deals (see Searcher.Lookahead).
	Lookahead bool
	Nodes     uint64
	Elapsed   time.Duration
}

// Best returns the best move, or false if there are no legal moves.
func (r Result) Best() (game.Move, bool) {
	if len(r.Lines) == 0 {
		return 0, false
	}
	return r.Lines[0].Move, true
}

type rootMove struct {
	move  game.Move
	score int
	pv    []game.Move
}

// Searcher holds all search memory, allocated once. It is not safe for
// concurrent use; run one Searcher per goroutine.
type Searcher struct {
	Weights eval.Weights
	// Lookahead values round-end positions by sampling next-round deals.
	// It runs as a second pass once the round is solved without it, and only
	// replaces the plain result if it completes within the budget. Zero
	// means off. Call Clear after changing it.
	Lookahead Lookahead

	look   *lookState // allocated on first use
	lookOn bool       // the lookahead pass is running
	salt   uint64     // XORed into TT keys during that pass: its values differ

	tt      *TT
	states  [maxPly + 1]game.State
	moves   [maxPly]game.MoveList
	scores  [maxPly][game.MaxMoves]int32
	killers [maxPly][2]game.Move
	history [game.NumPlayers][512]int32
	pv      [maxPly + 1][maxPly + 1]game.Move
	pvLen   [maxPly + 1]int

	rootMoves []rootMove
	nodes     uint64 // includes nodes searched inside lookahead deals
	ticks     uint64 // own nodes, for the budget check cadence
	leafCuts  uint64 // depth-limited leaves reached in the current iteration
	deadline  time.Time
	done      <-chan struct{}
	maxNodes  uint64
	canStop   bool
	stop      bool
}

// New returns a Searcher with the default weights and a ttMB-megabyte
// transposition table.
func New(ttMB int) *Searcher {
	return &Searcher{Weights: eval.Default, tt: NewTT(ttMB)}
}

// Clear forgets everything learned from earlier searches. Call it between
// games, or after changing Weights.
func (e *Searcher) Clear() {
	e.tt.Clear()
	e.history = [game.NumPlayers][512]int32{}
	if e.look != nil {
		e.look.clear()
	}
}

// Search returns the best moves for the side to move in s.
func (e *Searcher) Search(s *game.State, opt Options) Result {
	start := time.Now()
	e.nodes, e.stop, e.canStop = 0, false, false
	e.deadline = time.Time{}
	if opt.MoveTime > 0 {
		e.deadline = start.Add(opt.MoveTime)
	}
	e.maxNodes = opt.MaxNodes
	e.done = nil
	if opt.Ctx != nil {
		e.done = opt.Ctx.Done()
	}
	e.tt.newSearch()
	for p := range e.history {
		for i := range e.history[p] {
			e.history[p][i] /= 4
		}
	}
	e.killers = [maxPly][2]game.Move{}
	for i := range e.killers {
		e.killers[i] = [2]game.Move{noMove, noMove}
	}

	maxDepth := opt.MaxDepth
	if maxDepth <= 0 || maxDepth > game.MaxRoundPly {
		maxDepth = game.MaxRoundPly
	}
	multiPV := max(opt.MultiPV, 1)

	e.states[0] = *s
	var ml game.MoveList
	s.GenMoves(&ml)
	e.rootMoves = e.rootMoves[:0]
	for _, m := range ml.Slice() {
		e.rootMoves = append(e.rootMoves, rootMove{move: m, score: -inf})
	}
	slices.SortStableFunc(e.rootMoves, func(a, b rootMove) int {
		return cmp.Compare(quickScore(s, b.move), quickScore(s, a.move))
	})

	var res Result
	if len(e.rootMoves) == 0 {
		return res
	}
	for depth := 1; depth <= maxDepth; depth++ {
		e.leafCuts = 0
		e.searchRoot(depth, multiPV)
		if e.stop {
			break
		}
		e.canStop = true
		slices.SortStableFunc(e.rootMoves, func(a, b rootMove) int { return cmp.Compare(b.score, a.score) })
		res = e.result(depth, multiPV)
		res.Exact = e.leafCuts == 0
		res.Nodes, res.Elapsed = e.nodes, time.Since(start)
		if opt.OnDepth != nil {
			opt.OnDepth(res)
		}
		if res.Exact {
			break
		}
	}

	// The round is solved and budget is left: re-search at the same depth
	// with sampled next-round deals at the round-end leaves. The result is
	// used only if that pass completes; otherwise the plain one stands.
	if res.Exact && !e.stop && e.Lookahead.Samples > 0 {
		e.lookOn, e.salt = true, lookSalt
		e.leafCuts = 0
		e.searchRoot(res.Depth, multiPV)
		if !e.stop {
			slices.SortStableFunc(e.rootMoves, func(a, b rootMove) int { return cmp.Compare(b.score, a.score) })
			depth := res.Depth
			res = e.result(depth, multiPV)
			res.Exact, res.Lookahead = true, true
			if opt.OnDepth != nil {
				res.Nodes, res.Elapsed = e.nodes, time.Since(start)
				opt.OnDepth(res)
			}
		}
		e.lookOn, e.salt = false, 0
	}
	res.Nodes, res.Elapsed = e.nodes, time.Since(start)
	return res
}

func (e *Searcher) result(depth, multiPV int) Result {
	n := min(multiPV, len(e.rootMoves))
	r := Result{Depth: depth, Lines: make([]Line, n)}
	for i := range n {
		rm := &e.rootMoves[i]
		r.Lines[i] = Line{Move: rm.move, Score: rm.score, PV: e.extendPV(slices.Clone(rm.pv))}
	}
	return r
}

// extendPV completes a PV that was cut short by a TT hit, following
// exact-bound TT moves until the end of the round.
func (e *Searcher) extendPV(pv []game.Move) []game.Move {
	s := e.states[0]
	for _, m := range pv {
		s.Apply(m)
	}
	for len(pv) < game.MaxRoundPly && !s.RoundOver() {
		ent, ok := e.tt.probe(s.Hash ^ e.salt)
		if !ok || ent.bound() != boundExact || !s.Legal(ent.move) {
			break
		}
		pv = append(pv, ent.move)
		s.Apply(ent.move)
	}
	return pv
}

// searchRoot scores every root move. The first k moves get a full window;
// later moves are tested against the current k-th best score and only
// re-searched if they beat it.
func (e *Searcher) searchRoot(depth, k int) {
	root := &e.states[0]
	child := &e.states[1]
	for i := range e.rootMoves {
		rm := &e.rootMoves[i]
		*child = *root
		child.Apply(rm.move)
		var v int
		if i < k {
			v = -e.negamax(1, depth-1, -inf, inf)
		} else {
			t := e.kthBest(i, k)
			v = -e.negamax(1, depth-1, -t-1, -t)
			if v > t && !e.stop {
				v = -e.negamax(1, depth-1, -inf, -t)
			}
		}
		if e.stop {
			return
		}
		rm.score = v
		rm.pv = append(rm.pv[:0], rm.move)
		rm.pv = append(rm.pv, e.pv[1][1:e.pvLen[1]]...)
	}
}

// kthBest returns the k-th highest score among the first n root moves.
func (e *Searcher) kthBest(n, k int) int {
	var top [8]int
	k = min(k, len(top))
	for j := range k {
		top[j] = -inf
	}
	for _, rm := range e.rootMoves[:n] {
		v := rm.score
		for j := range k {
			if v > top[j] {
				v, top[j] = top[j], v
			}
		}
	}
	return top[k-1]
}

// outOfBudget reports whether the node or time limit is reached. The first
// iteration always completes so there is a move to return.
func (e *Searcher) outOfBudget() bool {
	if !e.canStop {
		return false
	}
	if e.maxNodes > 0 && e.nodes >= e.maxNodes {
		return true
	}
	select {
	case <-e.done: // a nil channel never fires
		return true
	default:
	}
	return !e.deadline.IsZero() && time.Now().After(e.deadline)
}

func (e *Searcher) negamax(ply, depth, alpha, beta int) int {
	e.pvLen[ply] = ply
	s := &e.states[ply]
	e.nodes++
	e.ticks++
	if e.ticks&1023 == 0 && e.outOfBudget() {
		e.stop = true
	}
	if e.stop {
		return 0
	}

	if s.RoundOver() {
		if e.lookOn {
			return e.roundEnd(s)
		}
		return e.Weights.Evaluate(s, int(s.ToMove))
	}
	if depth <= 0 {
		e.leafCuts++
		return e.Weights.Evaluate(s, int(s.ToMove))
	}

	alpha0 := alpha
	ttMove := noMove
	key := s.Hash ^ e.salt
	if ent, ok := e.tt.probe(key); ok {
		ttMove = ent.move
		if int(ent.depth) >= depth {
			v := int(ent.score)
			b := ent.bound()
			if b == boundExact || (b == boundLower && v >= beta) || (b == boundUpper && v <= alpha) {
				if ent.depth != exactDepth {
					e.leafCuts++
				}
				return v
			}
		}
	}

	ml := &e.moves[ply]
	sc := &e.scores[ply]
	s.GenMoves(ml)
	e.scoreMoves(s, ml, sc, ttMove, ply)

	cuts0 := e.leafCuts
	best, bestMove := -inf, noMove
	child := &e.states[ply+1]
	for i := 0; i < ml.N; i++ {
		pickBest(ml, sc, i)
		m := ml.Moves[i]
		*child = *s
		child.Apply(m)
		var v int
		if i == 0 {
			v = -e.negamax(ply+1, depth-1, -beta, -alpha)
		} else {
			v = -e.negamax(ply+1, depth-1, -alpha-1, -alpha)
			if v > alpha && v < beta {
				v = -e.negamax(ply+1, depth-1, -beta, -alpha)
			}
		}
		if e.stop {
			return 0
		}
		if v <= best {
			continue
		}
		best, bestMove = v, m
		if v <= alpha {
			continue
		}
		alpha = v
		e.pv[ply][ply] = m
		copy(e.pv[ply][ply+1:], e.pv[ply+1][ply+1:e.pvLen[ply+1]])
		e.pvLen[ply] = e.pvLen[ply+1]
		if v >= beta {
			if m != ttMove {
				e.onCutoff(s.ToMove, m, ply, depth)
			}
			break
		}
	}

	var bound uint8 = boundUpper
	switch {
	case best >= beta:
		bound = boundLower
	case best > alpha0:
		bound = boundExact
	}
	d := depth
	if e.leafCuts == cuts0 {
		d = exactDepth
	}
	e.tt.store(key, best, bestMove, d, bound)
	return best
}
