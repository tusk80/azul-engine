package game

// MaxRoundPly bounds the moves in one round: every move takes at least one
// of the at most 20 dealt tiles.
const MaxRoundPly = NumFactories * FactorySize

// Perft counts move sequences of the given length, stopping early at the end
// of the round (a finished round counts as one leaf).
func Perft(s *State, depth int) uint64 {
	// Per-ply buffers allocated once; passing &child to a recursive call
	// would otherwise force every copy onto the heap.
	st := new(perftStack)
	st.states[0] = *s
	return st.perft(0, depth)
}

type perftStack struct {
	states [MaxRoundPly + 1]State
	moves  [MaxRoundPly]MoveList
}

func (st *perftStack) perft(ply, depth int) uint64 {
	s := &st.states[ply]
	if depth == 0 || s.RoundOver() {
		return 1
	}
	ml := &st.moves[ply]
	s.GenMoves(ml)
	if depth == 1 {
		return uint64(ml.N)
	}
	var n uint64
	for _, m := range ml.Slice() {
		st.states[ply+1] = *s
		st.states[ply+1].Apply(m)
		n += st.perft(ply+1, depth-1)
	}
	return n
}
