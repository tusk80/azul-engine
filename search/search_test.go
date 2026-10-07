package search

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/tusk80/azul-engine/eval"
	"github.com/tusk80/azul-engine/game"
)

// brute is plain negamax to the end of the round: no pruning, no TT.
func brute(s *game.State) int {
	if s.RoundOver() {
		return eval.Evaluate(s, int(s.ToMove))
	}
	var ml game.MoveList
	s.GenMoves(&ml)
	best := -inf
	for _, m := range ml.Slice() {
		c := *s
		c.Apply(m)
		best = max(best, -brute(&c))
	}
	return best
}

// reference is plain fail-hard alpha-beta to the end of the round: no TT,
// no move ordering, no PVS. Much faster than brute but still obviously
// correct, so it can check deeper positions.
func reference(s *game.State, alpha, beta int) int {
	return referenceWith(s, alpha, beta, func(s *game.State) int { return eval.Evaluate(s, int(s.ToMove)) })
}

// referenceWith is reference with a custom round-end leaf value.
func referenceWith(s *game.State, alpha, beta int, leaf func(*game.State) int) int {
	if s.RoundOver() {
		return leaf(s)
	}
	var ml game.MoveList
	s.GenMoves(&ml)
	for _, m := range ml.Slice() {
		c := *s
		c.Apply(m)
		if v := -referenceWith(&c, -beta, -alpha, leaf); v > alpha {
			alpha = v
			if alpha >= beta {
				break
			}
		}
	}
	return alpha
}

func TestReferenceMatchesBruteForce(t *testing.T) {
	for i, s := range latePositions(100, 3, 4) {
		if got, want := reference(&s, -inf, inf), brute(&s); got != want {
			t.Fatalf("pos %d: reference %d, brute %d", i, got, want)
		}
	}
}

// groups counts non-empty (source, color) pairs: an upper bound on the moves
// left in the round.
func groups(s *game.State) int {
	n := 0
	for f := range s.Factories {
		for _, k := range s.Factories[f] {
			if k > 0 {
				n++
			}
		}
	}
	for _, k := range s.Center {
		if k > 0 {
			n++
		}
	}
	return n
}

// latePositions collects mid-round positions with minGroups..maxGroups groups
// left, from random games.
func latePositions(n, minGroups, maxGroups int) []game.State {
	var out []game.State
	var ml game.MoveList
	for seed := uint64(0); len(out) < n; seed++ {
		rng := rand.New(rand.NewPCG(seed, 99))
		s := game.NewGame(int(seed % 2))
		s.Refill(rng)
		for !s.GameOver && len(out) < n {
			if s.RoundOver() {
				if !s.ResolveRound() {
					s.Refill(rng)
				}
				continue
			}
			if g := groups(&s); g >= minGroups && g <= maxGroups && rng.IntN(2) == 0 {
				out = append(out, s)
			}
			s.GenMoves(&ml)
			s.Apply(ml.Moves[rng.IntN(ml.N)])
		}
	}
	return out
}

func TestSearchMatchesReference(t *testing.T) {
	n := 300
	if testing.Short() {
		n = 40
	}
	e := New(16)
	for i, s := range latePositions(n, 5, 8) {
		want := reference(&s, -inf, inf)
		orig := s
		res := e.Search(&s, Options{})
		if s != orig {
			t.Fatalf("pos %d: search mutated the root", i)
		}
		if !res.Exact {
			t.Fatalf("pos %d: expected an exact search", i)
		}
		if got := res.Lines[0].Score; got != want {
			t.Fatalf("pos %d: search %d, reference %d\n%v", i, got, want, s)
		}
		// The PV must be legal and reach the end of the round with the same value.
		c := s
		for _, m := range res.Lines[0].PV {
			if !c.Legal(m) {
				t.Fatalf("pos %d: illegal PV move %v", i, m)
			}
			c.Apply(m)
		}
		if c.RoundOver() {
			v := eval.Evaluate(&c, int(c.ToMove))
			if len(res.Lines[0].PV)%2 == 1 {
				v = -v
			}
			if v != want {
				t.Fatalf("pos %d: PV ends at %d, want %d", i, v, want)
			}
		}
	}
}

func TestLookaheadMatchesReference(t *testing.T) {
	look := Lookahead{Samples: 3, Depth: 1}
	e := New(16)
	e.Lookahead = look
	// An independent searcher computes the same round-end values for the
	// reference: they depend only on the position and the settings.
	leafEng := New(4)
	leafEng.Lookahead = look
	leaf := func(s *game.State) int { return leafEng.roundEnd(s) }
	for i, s := range latePositions(40, 3, 5) {
		want := referenceWith(&s, -inf, inf, leaf)
		res := e.Search(&s, Options{})
		if !res.Exact || res.Lines[0].Score != want {
			t.Fatalf("pos %d: search %d (exact %v), reference %d", i, res.Lines[0].Score, res.Exact, want)
		}
	}
}

func TestLookaheadCountsNodes(t *testing.T) {
	s := latePositions(1, 4, 4)[0]
	plain := New(4).Search(&s, Options{}).Nodes
	e := New(4)
	e.Lookahead = Lookahead{Samples: 2, Depth: 1}
	if looked := e.Search(&s, Options{}).Nodes; looked <= plain {
		t.Fatalf("lookahead nodes %d should exceed plain %d", looked, plain)
	}
}

func TestMultiPVMatchesReference(t *testing.T) {
	e := New(16)
	for i, s := range latePositions(60, 4, 7) {
		var ml game.MoveList
		s.GenMoves(&ml)
		var want []int
		for _, m := range ml.Slice() {
			c := s
			c.Apply(m)
			want = append(want, -reference(&c, -inf, inf))
		}
		slices.SortFunc(want, func(a, b int) int { return b - a })
		res := e.Search(&s, Options{MultiPV: 3})
		if len(res.Lines) != min(3, len(want)) {
			t.Fatalf("pos %d: %d lines, want %d", i, len(res.Lines), min(3, len(want)))
		}
		for j, l := range res.Lines {
			if l.Score != want[j] {
				t.Fatalf("pos %d line %d: score %d, want %d (all %v)", i, j, l.Score, want[j], want)
			}
		}
	}
}

// playGame pits the engine (fixed depth) against a random mover and
// returns the engine's result: 1 win, 0 shared, -1 loss.
func playGame(e *Searcher, seed uint64, engineSeat int, depth int) int {
	rng := rand.New(rand.NewPCG(seed, 7))
	s := game.NewGame(int(seed % 2))
	s.Refill(rng)
	var ml game.MoveList
	for !s.GameOver {
		if s.RoundOver() {
			if !s.ResolveRound() {
				s.Refill(rng)
			}
			continue
		}
		if int(s.ToMove) == engineSeat {
			m, _ := e.Search(&s, Options{MaxDepth: depth}).Best()
			s.Apply(m)
		} else {
			s.GenMoves(&ml)
			s.Apply(ml.Moves[rng.IntN(ml.N)])
		}
	}
	switch s.Winner() {
	case engineSeat:
		return 1
	case -1:
		return 0
	}
	return -1
}

func TestBeatsRandom(t *testing.T) {
	games := 20
	if testing.Short() {
		games = 4
	}
	e := New(16)
	wins := 0
	for g := range games {
		if playGame(e, uint64(g), g%2, 2) == 1 {
			wins++
		}
	}
	if wins < games {
		t.Fatalf("engine won %d/%d against random", wins, games)
	}
}

func TestMoveTime(t *testing.T) {
	s := game.NewGame(0)
	s.Refill(rand.New(rand.NewPCG(1, 1)))
	e := New(16)
	start := time.Now()
	res := e.Search(&s, Options{MoveTime: 100 * time.Millisecond})
	if el := time.Since(start); el > 400*time.Millisecond {
		t.Fatalf("search took %v with a 100ms limit", el)
	}
	if _, ok := res.Best(); !ok || res.Depth < 1 {
		t.Fatalf("no move after timed search: %+v", res)
	}
}

func TestContextCancelStopsSearch(t *testing.T) {
	s := game.NewGame(0)
	s.Refill(rand.New(rand.NewPCG(1, 1)))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	// No time, node or depth limit: only the cancellation can end this.
	res := New(16).Search(&s, Options{Ctx: ctx})
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("search ran %v after cancellation", el)
	}
	if _, ok := res.Best(); !ok {
		t.Fatal("a cancelled search should still return its best move so far")
	}
}

func BenchmarkSearchOpeningDepth5(b *testing.B) {
	s := game.NewGame(0)
	s.Refill(rand.New(rand.NewPCG(1, 1)))
	e := New(64)
	var nodes uint64
	for b.Loop() {
		e.tt.Clear()
		nodes += e.Search(&s, Options{MaxDepth: 5}).Nodes
	}
	b.ReportMetric(float64(nodes)/b.Elapsed().Seconds(), "nodes/s")
}
