package game

import "math/bits"

// Wall is a 5x5 wall stored twice: row-major (bit r*5+col) and transposed
// (bit col*5+r), so both row and column masks come out with one shift.
type Wall struct {
	Rows uint32
	Cols uint32
}

var (
	// runLen[mask][pos] is the length of the contiguous run through pos in a
	// 5-bit line mask, or 0 if pos is not set.
	runLen [32][5]uint8
	// colorMask[c] is the diagonal of wall cells holding color c (row-major).
	colorMask [NumColors]uint32
)

func init() {
	for m := 0; m < 32; m++ {
		for p := 0; p < 5; p++ {
			if m>>p&1 == 0 {
				continue
			}
			lo, hi := p, p
			for lo > 0 && m>>(lo-1)&1 != 0 {
				lo--
			}
			for hi < 4 && m>>(hi+1)&1 != 0 {
				hi++
			}
			runLen[m][p] = uint8(hi - lo + 1)
		}
	}
	for c := Color(0); c < NumColors; c++ {
		for r := 0; r < NumRows; r++ {
			colorMask[c] |= 1 << (r*5 + WallCol(r, c))
		}
	}
}

func (w Wall) Has(r, col int) bool          { return w.Rows>>(r*5+col)&1 != 0 }
func (w Wall) HasColor(r int, c Color) bool { return w.Has(r, WallCol(r, c)) }
func (w Wall) RowMask(r int) uint32         { return w.Rows >> (r * 5) & 31 }
func (w Wall) ColMask(col int) uint32       { return w.Cols >> (col * 5) & 31 }

func (w *Wall) set(r, col int) {
	w.Rows |= 1 << (r*5 + col)
	w.Cols |= 1 << (col*5 + r)
}

// Place puts a tile at (r, col) and returns the points it scores.
// The cell must be empty.
func (w *Wall) Place(r, col int) int {
	w.set(r, col)
	h := int(runLen[w.RowMask(r)][col])
	v := int(runLen[w.ColMask(col)][r])
	if h == 1 && v == 1 {
		return 1
	}
	pts := 0
	if h > 1 {
		pts += h
	}
	if v > 1 {
		pts += v
	}
	return pts
}

func (w Wall) CompleteRows() int {
	n := 0
	for r := 0; r < NumRows; r++ {
		if w.RowMask(r) == 31 {
			n++
		}
	}
	return n
}

func (w Wall) CompleteCols() int {
	n := 0
	for col := 0; col < 5; col++ {
		if w.ColMask(col) == 31 {
			n++
		}
	}
	return n
}

func (w Wall) CompleteColors() int {
	n := 0
	for c := range colorMask {
		if w.Rows&colorMask[c] == colorMask[c] {
			n++
		}
	}
	return n
}

// Bonus returns the end-of-game bonus: 2 per row, 7 per column, 10 per color.
func (w Wall) Bonus() int {
	return 2*w.CompleteRows() + 7*w.CompleteCols() + 10*w.CompleteColors()
}

// ColorCount returns how many tiles of color c are on the wall.
func (w Wall) ColorCount(c Color) int { return bits.OnesCount32(w.Rows & colorMask[c]) }

// floorPenalty[n] is the cumulative penalty for n occupied floor slots.
var floorPenalty = [FloorSlots + 1]int{0, -1, -2, -4, -6, -8, -11, -14}

func FloorPenalty(slots uint8) int { return floorPenalty[slots] }
