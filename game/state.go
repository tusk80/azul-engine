package game

import "fmt"

const (
	NumPlayers    = 2
	NumFactories  = 5
	NumRows       = 5
	FactorySize   = 4
	TilesPerColor = 20
	FloorSlots    = 7
)

// Line is one pattern line. Row r holds up to r+1 tiles of a single color.
type Line struct {
	Color Color // meaningful only when Count > 0
	Count uint8
}

// PlayerBoard is one player's wall, pattern lines, floor and score.
type PlayerBoard struct {
	Wall  Wall
	Lines [NumRows]Line
	// Floor is the number of occupied floor slots (0..7), including the
	// first-player token. Floor tiles go to the lid as soon as they land,
	// so only the slot count matters.
	Floor uint8
	Score int16
}

// CanPlace reports whether tiles of color c may go into pattern line r.
func (p *PlayerBoard) CanPlace(r int, c Color) bool {
	l := p.Lines[r]
	if l.Count == 0 {
		return !p.Wall.HasColor(r, c)
	}
	return l.Color == c && int(l.Count) < r+1
}

// State is a full 2-player game state. It holds no pointers, so copying it
// is a plain memcpy; search uses copy-make.
//
// Within a round only lines, floors, factories, center, token, lid and side
// to move change. Walls and scores change only in ResolveRound.
type State struct {
	Players   [NumPlayers]PlayerBoard
	Factories [NumFactories][NumColors]uint8
	Center    [NumColors]uint8
	Bag       [NumColors]uint8
	Lid       [NumColors]uint8
	Hash      uint64
	ToMove    uint8
	// NextFirst starts the next round: whoever took the token, or this
	// round's starting player if nobody did.
	NextFirst     uint8
	TokenInCenter bool
	Round         uint8
	GameOver      bool
}

// NewGame returns a fresh game with a full bag and empty factories.
// Call Refill or RefillFrom to deal the first round.
func NewGame(first int) State {
	var s State
	for c := range s.Bag {
		s.Bag[c] = TilesPerColor
	}
	s.ToMove = uint8(first)
	s.NextFirst = uint8(first)
	s.TokenInCenter = true
	s.Round = 1
	s.Hash = s.ComputeHash()
	return s
}

// RoundOver reports whether the factories and center are empty.
func (s *State) RoundOver() bool {
	for f := range s.Factories {
		if s.Factories[f] != ([NumColors]uint8{}) {
			return false
		}
	}
	return s.Center == [NumColors]uint8{}
}

// tileCounts returns the number of tiles of each color across bag, lid,
// factories, center, pattern lines and walls.
func (s *State) tileCounts() (n [NumColors]int) {
	for c := 0; c < NumColors; c++ {
		n[c] += int(s.Bag[c]) + int(s.Lid[c]) + int(s.Center[c])
		for f := range s.Factories {
			n[c] += int(s.Factories[f][c])
		}
		for p := range s.Players {
			n[c] += s.Players[p].Wall.ColorCount(Color(c))
		}
	}
	for p := range s.Players {
		for _, l := range s.Players[p].Lines {
			if l.Count > 0 {
				n[l.Color] += int(l.Count)
			}
		}
	}
	return n
}

// Validate checks structural invariants and tile conservation (20 per color).
func (s *State) Validate() error {
	if s.ToMove >= NumPlayers || s.NextFirst >= NumPlayers {
		return fmt.Errorf("player index out of range")
	}
	for f := range s.Factories {
		n := 0
		for _, k := range s.Factories[f] {
			n += int(k)
		}
		if n > FactorySize {
			return fmt.Errorf("factory %d holds %d tiles", f+1, n)
		}
	}
	for p := range s.Players {
		pb := &s.Players[p]
		if pb.Floor > FloorSlots {
			return fmt.Errorf("P%d floor has %d slots", p+1, pb.Floor)
		}
		if pb.Score < 0 {
			return fmt.Errorf("P%d score is negative", p+1)
		}
		for r, l := range pb.Lines {
			if int(l.Count) > r+1 {
				return fmt.Errorf("P%d line %d holds %d tiles", p+1, r+1, l.Count)
			}
			if l.Count > 0 && (l.Color >= NumColors || pb.Wall.HasColor(r, l.Color)) {
				return fmt.Errorf("P%d line %d color %v is invalid for that row", p+1, r+1, l.Color)
			}
		}
	}
	for c, n := range s.tileCounts() {
		if n != TilesPerColor {
			return fmt.Errorf("%v: %d tiles, want %d", Color(c), n, TilesPerColor)
		}
	}
	return nil
}
