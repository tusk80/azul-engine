package game

// Effect describes what a move does for the player making it, for
// explanations and the analysis board.
type Effect struct {
	Tiles        int  // tiles taken
	Placed       int  // tiles put into the pattern line
	ToFloor      int  // tiles dropped on the floor
	Token        bool // takes the first-player token
	CompleteLine bool // the pattern line is full after the move
	// WallPoints is what the completed line scores when tiled, judged on
	// the current wall (other lines tiled first this round are ignored).
	WallPoints int
	// FloorDelta is the change in this round's floor penalty (≤ 0).
	FloorDelta int
	// Bonus is the end-game bonus the tiled tile would complete
	// (row +2, column +7, color +10).
	Bonus int
}

// MoveEffect computes the effect of a legal move for the side to move.
func (s *State) MoveEffect(m Move) Effect {
	src, c, dst := m.Src(), m.Color(), m.Dst()
	var e Effect
	if src == SrcCenter {
		e.Tiles = int(s.Center[c])
		e.Token = s.TokenInCenter
	} else {
		e.Tiles = int(s.Factories[src][c])
	}
	pb := &s.Players[s.ToMove]
	e.ToFloor = e.Tiles
	if dst != DstFloor {
		l := pb.Lines[dst]
		room := dst + 1 - int(l.Count)
		e.Placed = min(e.Tiles, room)
		e.ToFloor = e.Tiles - e.Placed
		if e.Placed == room {
			e.CompleteLine = true
			w := pb.Wall
			e.WallPoints = w.Place(dst, WallCol(dst, c))
			e.Bonus = w.Bonus() - pb.Wall.Bonus()
		}
	}
	slots := e.ToFloor
	if e.Token {
		slots++
	}
	after := min(int(pb.Floor)+slots, FloorSlots)
	e.FloorDelta = FloorPenalty(uint8(after)) - FloorPenalty(pb.Floor)
	return e
}
