package search

import (
	"math/rand/v2"

	"github.com/tusk80/azul-engine/eval"
	"github.com/tusk80/azul-engine/game"
)

// Lookahead configures round-end evaluation by sampling: at the end of a
// round the position is scored, then Samples next-round deals are drawn
// from the bag (and lid) and each is searched Depth plies deep. The value
// is the average.
//
// Every round-end leaf in a search uses the same deal seeds. Within a round
// the bag never changes, so sibling leaves are compared on identical deals
// (common random numbers), which keeps the noise out of move choice.
type Lookahead struct {
	Samples int
	Depth   int
}

const (
	lookSalt      = 0x9e37_79b9_7f4a_7c15
	lookSeed      = 0x5eed_a2c1
	lookCacheBits = 16
)

type lookEntry struct {
	key uint64
	v   int32 // value for player 0
	ok  bool
}

type lookState struct {
	inner *Searcher // searches the sampled deals; has no lookahead itself
	pcg   *rand.PCG
	rng   *rand.Rand
	cache []lookEntry
}

func (l *lookState) clear() {
	clear(l.cache)
	l.inner.Clear()
}

// roundEnd values a finished round for the side to move at s.
func (e *Searcher) roundEnd(s *game.State) int {
	me := int(s.ToMove)
	next := *s
	if next.ResolveRound() {
		return eval.Terminal(&next, me)
	}
	if e.look == nil {
		pcg := rand.NewPCG(0, 0)
		e.look = &lookState{
			inner: &Searcher{tt: NewTT(8)},
			pcg:   pcg,
			rng:   rand.New(pcg),
			cache: make([]lookEntry, 1<<lookCacheBits),
		}
	}
	l := e.look
	slot := &l.cache[next.Hash&(1<<lookCacheBits-1)]
	if !slot.ok || slot.key != next.Hash {
		l.inner.Weights = e.Weights
		before := l.inner.nodes
		sum := 0
		for k := range e.Lookahead.Samples {
			d := next
			l.pcg.Seed(lookSeed, uint64(k))
			d.Refill(l.rng)
			v := l.inner.fixedDepth(&d, e.Lookahead.Depth)
			if d.ToMove != 0 {
				v = -v
			}
			sum += v
		}
		e.nodes += l.inner.nodes - before
		*slot = lookEntry{key: next.Hash, v: int32(sum / e.Lookahead.Samples), ok: true}
	}
	if me == 0 {
		return int(slot.v)
	}
	return -int(slot.v)
}

// fixedDepth returns the negamax value of s for its side to move, searched
// depth plies with a full window and no limits.
func (e *Searcher) fixedDepth(s *game.State, depth int) int {
	e.states[0] = *s
	e.stop, e.canStop = false, false
	return e.negamax(0, depth, -inf, inf)
}
