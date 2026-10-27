package search

import (
	"sync/atomic"

	"github.com/tusk80/azul-engine/internal/game"
)

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

func (e *ttEntry) pack() uint64 {
	return uint64(uint32(e.score)) | uint64(e.move)<<32 | uint64(uint8(e.depth))<<48 | uint64(e.meta)<<56
}

func unpack(key, d uint64) ttEntry {
	return ttEntry{key: key, score: int32(uint32(d)), move: game.Move(d >> 32), depth: int8(d >> 48), meta: uint8(d >> 56)}
}

// slot holds one entry as two words: the data, and the key XOR the data.
// Searchers on several threads share the table without locks: a slot torn
// by two writers fails the key check and reads as a miss.
type slot struct {
	check, data atomic.Uint64
}

// TT is a single-slot transposition table indexed by the low hash bits,
// safe to share between searchers running at the same time.
type TT struct {
	slots []slot
	mask  uint64
	gen   uint8
}

// NewTT returns a table using at most mb megabytes (rounded down to a power
// of two entries).
func NewTT(mb int) *TT {
	n := uint64(1)
	for n*2*16 <= uint64(max(mb, 1))<<20 {
		n *= 2
	}
	return &TT{slots: make([]slot, n), mask: n - 1}
}

func (t *TT) newSearch() { t.gen = (t.gen + 1) & 63 }

// Clear empties the table. Call it only while no search is using it.
func (t *TT) Clear() { clear(t.slots) }

func (t *TT) probe(key uint64) (ttEntry, bool) {
	s := &t.slots[key&t.mask]
	d := s.data.Load()
	e := unpack(key, d)
	return e, s.check.Load()^d == key && e.meta != 0
}

// store keeps the deeper entry unless the slot is from an older search or
// holds the same position.
func (t *TT) store(key uint64, score int, m game.Move, depth int, bound uint8) {
	s := &t.slots[key&t.mask]
	old := s.data.Load()
	if e := unpack(0, old); s.check.Load()^old != key && e.gen() == t.gen && int(e.depth) > depth {
		return
	}
	d := (&ttEntry{score: int32(score), move: m, depth: int8(depth), meta: bound | t.gen<<2}).pack()
	s.data.Store(d)
	s.check.Store(key ^ d)
}
