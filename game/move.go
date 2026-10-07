package game

import (
	"fmt"
	"strings"
)

// Move packs source, color and destination into 9 bits:
// bits 0-2 source (0-4 factory, 5 center), 3-5 color, 6-8 destination
// (0-4 pattern line, 5 floor).
type Move uint16

const (
	SrcCenter = 5
	DstFloor  = 5
	// MaxMoves bounds the moves in any position: 6 sources × 5 colors × 6 targets.
	MaxMoves = 6 * NumColors * 6
)

func NewMove(src int, c Color, dst int) Move {
	return Move(src | int(c)<<3 | dst<<6)
}

func (m Move) Src() int     { return int(m & 7) }
func (m Move) Color() Color { return Color(m >> 3 & 7) }
func (m Move) Dst() int     { return int(m >> 6 & 7) }

// String formats a move with 1-based numbers, e.g. "F2 red->3" or "C blue->floor".
func (m Move) String() string {
	src := "C"
	if m.Src() != SrcCenter {
		src = fmt.Sprintf("F%d", m.Src()+1)
	}
	dst := "floor"
	if m.Dst() != DstFloor {
		dst = fmt.Sprint(m.Dst() + 1)
	}
	return fmt.Sprintf("%s %v->%s", src, m.Color(), dst)
}

// ParseMove parses the String format. The arrow is optional.
func ParseMove(str string) (Move, error) {
	f := strings.Fields(strings.ReplaceAll(str, "->", " "))
	if len(f) != 3 {
		return 0, fmt.Errorf("move %q: want \"<F1..F5|C> <color> <1..5|floor>\"", str)
	}
	var src int
	switch s := strings.ToUpper(f[0]); {
	case s == "C":
		src = SrcCenter
	case len(s) == 2 && s[0] == 'F' && s[1] >= '1' && s[1] <= '5':
		src = int(s[1] - '1')
	default:
		return 0, fmt.Errorf("move %q: bad source %q", str, f[0])
	}
	c, err := ParseColor(f[1])
	if err != nil {
		return 0, fmt.Errorf("move %q: %w", str, err)
	}
	var dst int
	switch d := strings.ToLower(f[2]); {
	case d == "floor" || d == "f":
		dst = DstFloor
	case len(d) == 1 && d[0] >= '1' && d[0] <= '5':
		dst = int(d[0] - '1')
	default:
		return 0, fmt.Errorf("move %q: bad destination %q", str, f[2])
	}
	return NewMove(src, c, dst), nil
}

// MoveList is a fixed-size move buffer; keep one per search ply.
type MoveList struct {
	N     int
	Moves [MaxMoves]Move
}

func (ml *MoveList) Slice() []Move { return ml.Moves[:ml.N] }

// GenMoves fills ml with all legal moves for the side to move. A factory
// with the same contents as an earlier one is skipped: the resulting states
// are equivalent because factories are an unordered multiset.
func (s *State) GenMoves(ml *MoveList) {
	ml.N = 0
	if s.GameOver {
		return
	}
	p := &s.Players[s.ToMove]
	for f := 0; f < NumFactories; f++ {
		fac := &s.Factories[f]
		if *fac == ([NumColors]uint8{}) || s.dupFactory(f) {
			continue
		}
		for c := Color(0); c < NumColors; c++ {
			if fac[c] > 0 {
				ml.addTargets(p, f, c)
			}
		}
	}
	for c := Color(0); c < NumColors; c++ {
		if s.Center[c] > 0 {
			ml.addTargets(p, SrcCenter, c)
		}
	}
}

func (s *State) dupFactory(f int) bool {
	for g := 0; g < f; g++ {
		if s.Factories[g] == s.Factories[f] {
			return true
		}
	}
	return false
}

func (ml *MoveList) addTargets(p *PlayerBoard, src int, c Color) {
	for r := 0; r < NumRows; r++ {
		if p.CanPlace(r, c) {
			ml.Moves[ml.N] = NewMove(src, c, r)
			ml.N++
		}
	}
	ml.Moves[ml.N] = NewMove(src, c, DstFloor)
	ml.N++
}

// Legal reports whether m is legal for the side to move. Unlike GenMoves it
// accepts moves from duplicate factories.
func (s *State) Legal(m Move) bool {
	src, c, dst := m.Src(), m.Color(), m.Dst()
	if s.GameOver || src > SrcCenter || c >= NumColors || dst > DstFloor {
		return false
	}
	var n uint8
	if src == SrcCenter {
		n = s.Center[c]
	} else {
		n = s.Factories[src][c]
	}
	if n == 0 {
		return false
	}
	return dst == DstFloor || s.Players[s.ToMove].CanPlace(dst, c)
}

// Apply plays a legal move for the side to move and updates the hash
// incrementally. It does not check legality.
func (s *State) Apply(m Move) {
	src, c, dst := m.Src(), m.Color(), m.Dst()
	me := int(s.ToMove)
	var n uint8
	token := false

	if src == SrcCenter {
		n = s.Center[c]
		s.setCenter(c, 0)
		if s.TokenInCenter {
			token = true
			s.TokenInCenter = false
			s.Hash -= zob.token
			s.Hash += zob.nextFirst[me] - zob.nextFirst[s.NextFirst]
			s.NextFirst = uint8(me)
		}
	} else {
		f := &s.Factories[src]
		s.Hash -= zob.factory[factoryIndex(f)]
		n = f[c]
		for oc := Color(0); oc < NumColors; oc++ {
			if oc != c && f[oc] > 0 {
				s.setCenter(oc, s.Center[oc]+f[oc])
			}
		}
		*f = [NumColors]uint8{}
		s.Hash += zob.factory[0]
	}

	pb := &s.Players[me]
	spill := n
	if dst != DstFloor {
		l := &pb.Lines[dst]
		s.Hash -= lineKey(me, dst, *l)
		put := min(n, uint8(dst+1)-l.Count)
		l.Color = c
		l.Count += put
		s.Hash += lineKey(me, dst, *l)
		spill = n - put
	}
	if spill > 0 {
		s.setLid(c, s.Lid[c]+spill)
	}
	slots := spill
	if token {
		slots++
	}
	if slots > 0 {
		nf := min(pb.Floor+slots, FloorSlots)
		s.Hash += zob.floor[me][nf] - zob.floor[me][pb.Floor]
		pb.Floor = nf
	}

	if s.ToMove == 1 {
		s.Hash -= zob.side
	} else {
		s.Hash += zob.side
	}
	s.ToMove ^= 1
}

func (s *State) setCenter(c Color, n uint8) {
	s.Hash += zob.center[c][n] - zob.center[c][s.Center[c]]
	s.Center[c] = n
}

func (s *State) setLid(c Color, n uint8) {
	s.Hash += zob.lid[c][n] - zob.lid[c][s.Lid[c]]
	s.Lid[c] = n
}
