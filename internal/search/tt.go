package search

import "github.com/tusk80/azul-engine/internal/game"

const (
	boundUpper = 1
	boundLower = 2
	boundExact = boundUpper | boundLower
)

// exactDepth marks an entry whose subtree was searched to the end of the
// round with no depth cutoff, so it is valid at any remaining depth.
const exactDepth = 127

type ttEntry struct {
	key   uint64
	score int32
	move  game.Move
	depth int8
	meta  uint8 // bits 0-1 bound, bits 2-7 search generation
}

func (e *ttEntry) bound() uint8 { return e.meta & 3 }
func (e *ttEntry) gen() uint8   { return e.meta >> 2 }

// TT is a single-slot transposition table indexed by the low hash bits.
type TT struct {
	entries []ttEntry
	mask    uint64
	gen     uint8
}

// NewTT returns a table using at most mb megabytes (rounded down to a power
// of two entries).
func NewTT(mb int) *TT {
	n := uint64(1)
	for n*2*16 <= uint64(max(mb, 1))<<20 {
		n *= 2
	}
	return &TT{entries: make([]ttEntry, n), mask: n - 1}
}

func (t *TT) newSearch() { t.gen = (t.gen + 1) & 63 }

func (t *TT) Clear() { clear(t.entries) }

func (t *TT) probe(key uint64) (ttEntry, bool) {
	e := t.entries[key&t.mask]
	return e, e.key == key && e.meta != 0
}

// store keeps the deeper entry unless the slot is from an older search or
// holds the same position.
func (t *TT) store(key uint64, score int, m game.Move, depth int, bound uint8) {
	e := &t.entries[key&t.mask]
	if e.key != key && e.gen() == t.gen && int(e.depth) > depth {
		return
	}
	*e = ttEntry{key: key, score: int32(score), move: m, depth: int8(depth), meta: bound | t.gen<<2}
}
