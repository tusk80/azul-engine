// Package eval scores positions for the search.
package eval

import (
	"fmt"
	"math/bits"

	"github.com/tusk80/azul-engine/game"
)

const (
	// Scale is the number of eval units per game point.
	Scale = 100
	// Win is added to (or subtracted from) the value of a finished game, so
	// any win outranks any unfinished line and the margin breaks ties.
	Win = 1_000_000
)

// Weights are the tunable evaluation terms, in eval units (1/100 point)
// unless noted. The zero value is a pure "points after this round" eval.
type Weights struct {
	LineFill  int `json:"lineFill"`  // % of a partial line's placement points, scaled by how full it is
	LineBase  int `json:"lineBase"`  // per partial line; negative means a blocked row costs
	Adjacency int `json:"adjacency"` // per pair of adjacent wall tiles
	Row4      int `json:"row4"`      // per wall row with 4 tiles
	Col3      int `json:"col3"`      // per wall column with 3 tiles
	Col4      int `json:"col4"`      // per wall column with 4 tiles
	Color3    int `json:"color3"`    // per color with 3 tiles on the wall
	Color4    int `json:"color4"`    // per color with 4 tiles on the wall
	Token     int `json:"token"`     // holding the first-player token for next round
	Overflow  int `json:"overflow"`  // mid-round: per remaining tile the player has no line room for
	EndLead   int `json:"endLead"`   // per point of lead, per wall row with 4+ tiles on either board

	LineMissing   int `json:"lineMissing"`   // per tile still missing from partial pattern lines
	WallPotential int `json:"wallPotential"` // per point the empty wall cells would score if filled now
	Tempo         int `json:"tempo"`         // mid-round: having the move
	Completable   int `json:"completable"`   // mid-round: per partial line the tiles left on the table can still finish
}

// Default holds the current best weights: weights/tuned4.json, from 1000
// SPSA iterations after adding Completable. Against tuned3: +25 ± 12 and
// +37 ± 12 Elo at 20k nodes per move (two 3000-game runs on different
// deals) and +35 ± 24 at 40 ms per move (800 games). tuned3 was +158 ± 14
// over tuned1, which was +298 ± 30 over points-only.
//
// Adjacency is negative because WallPotential measures what adjacency was
// a proxy for. The bonus-proximity terms shrank too.
var Default = Weights{
	LineFill:      60,
	LineBase:      -151,
	Adjacency:     -34,
	Row4:          50,
	Col3:          -6,
	Col4:          -32,
	Color3:        19,
	Color4:        55,
	Token:         91,
	Overflow:      -90,
	EndLead:       -6,
	LineMissing:   -23,
	WallPotential: 36,
	Tempo:         -2,
	Completable:   17,
}

// Param names one weight for tuning, with its SPSA perturbation size.
type Param struct {
	Name string
	Ptr  *int
	Step float64
}

func (w *Weights) Params() []Param {
	return []Param{
		{"lineFill", &w.LineFill, 10},
		{"lineBase", &w.LineBase, 10},
		{"adjacency", &w.Adjacency, 5},
		{"row4", &w.Row4, 20},
		{"col3", &w.Col3, 20},
		{"col4", &w.Col4, 30},
		{"color3", &w.Color3, 20},
		{"color4", &w.Color4, 30},
		{"token", &w.Token, 15},
		{"overflow", &w.Overflow, 10},
		{"endLead", &w.EndLead, 2},
		{"lineMissing", &w.LineMissing, 8},
		{"wallPotential", &w.WallPotential, 2},
		{"tempo", &w.Tempo, 15},
		{"completable", &w.Completable, 15},
	}
}

// Evaluate scores s for player p with the Default weights.
func Evaluate(s *game.State, p int) int { return Default.Evaluate(s, p) }

// Evaluate returns the value of s for player p in eval units.
//
// The round is scored as if it ended now (full lines tiled, floor penalties
// applied). If s is at the end of a round and that ends the game, the exact
// result is returned; a mid-round projection never claims a win. Positional
// terms are then added on the projected boards.
func (w *Weights) Evaluate(s *game.State, p int) int {
	if s.GameOver {
		return Terminal(s, p)
	}
	c := *s
	over := c.ProjectRound()
	if over && s.RoundOver() {
		return Terminal(&c, p)
	}
	q := 1 - p
	lead := int(c.Players[p].Score) - int(c.Players[q].Score)
	v := lead * Scale
	if !s.RoundOver() {
		v += w.Overflow * (overflow(s, p) - overflow(s, q))
		v += w.Completable * (completable(s, p) - completable(s, q))
		if int(s.ToMove) == p {
			v += w.Tempo
		} else {
			v -= w.Tempo
		}
	}
	if over {
		// The game ends this round: future-round terms no longer matter.
		return v
	}
	v += w.board(&c.Players[p]) - w.board(&c.Players[q])
	if !s.TokenInCenter {
		if int(s.NextFirst) == p {
			v += w.Token
		} else {
			v -= w.Token
		}
	}
	near := nearRows(&c.Players[p].Wall) + nearRows(&c.Players[q].Wall)
	v += w.EndLead * near * max(min(lead, 20), -20)
	return v
}

// board scores one player's projected board: partial lines, wall shape and
// bonus proximity.
func (w *Weights) board(pb *game.PlayerBoard) int {
	v := 0
	for r, l := range pb.Lines {
		if l.Count == 0 {
			continue
		}
		wall := pb.Wall
		pts := wall.Place(r, game.WallCol(r, l.Color))
		v += w.LineFill*pts*int(l.Count)/(r+1) + w.LineBase + w.LineMissing*(r+1-int(l.Count))
	}
	if w.WallPotential != 0 {
		v += w.WallPotential * WallPotential(pb.Wall)
	}
	v += w.Adjacency * Adjacency(pb.Wall)
	for i := range 5 {
		switch bits.OnesCount32(pb.Wall.RowMask(i)) {
		case 4:
			v += w.Row4
		}
		switch bits.OnesCount32(pb.Wall.ColMask(i)) {
		case 3:
			v += w.Col3
		case 4:
			v += w.Col4
		}
		switch pb.Wall.ColorCount(game.Color(i)) {
		case 3:
			v += w.Color3
		case 4:
			v += w.Color4
		}
	}
	return v
}

// noWrap has the bits of columns 0-3 in every row, so a shift by one pairs
// only cells within the same row (or, on the transposed wall, column).
const noWrap = 0b01111 * (1 | 1<<5 | 1<<10 | 1<<15 | 1<<20)

// Adjacency counts horizontally and vertically adjacent pairs of tiles.
func Adjacency(w game.Wall) int {
	return bits.OnesCount32(w.Rows&(w.Rows>>1)&noWrap) + bits.OnesCount32(w.Cols&(w.Cols>>1)&noWrap)
}

// WallPotential sums the points every empty wall cell would score if it
// were filled next, on the current wall.
//
// A placement scores h + v, counting each run only if it is longer than one,
// or 1 if the tile has no neighbours. So the total splits into a row part and
// a column part, each a table lookup on a 5-bit mask, plus the number of
// empty cells without neighbours.
func WallPotential(w game.Wall) int {
	n := 0
	for i := range 5 {
		n += int(linePotential[w.RowMask(i)]) + int(linePotential[w.ColMask(i)])
	}
	const all = 1<<25 - 1
	t := w.Rows
	nb := (t<<1)&(noWrap<<1) | (t>>1)&noWrap | t<<5 | t>>5
	return n + bits.OnesCount32(^t&^nb&all)
}

// linePotential[m] sums, over the empty cells of a 5-cell line with mask m,
// the run length a tile there would join if that run is longer than one.
var linePotential [32]uint8

func init() {
	for m := range 32 {
		for c := range 5 {
			if m>>c&1 != 0 {
				continue
			}
			if h := runLen(m|1<<c, c); h > 1 {
				linePotential[m] += uint8(h)
			}
		}
	}
}

func runLen(m, p int) int {
	lo, hi := p, p
	for lo > 0 && m>>(lo-1)&1 != 0 {
		lo--
	}
	for hi < 4 && m>>(hi+1)&1 != 0 {
		hi++
	}
	return hi - lo + 1
}

// completable counts player p's partial pattern lines that the tiles still
// on the table (factories and center) could finish this round.
func completable(s *game.State, p int) int {
	var left [game.NumColors]int
	for c := range left {
		left[c] = int(s.Center[c])
		for f := range s.Factories {
			left[c] += int(s.Factories[f][c])
		}
	}
	n := 0
	for r, l := range s.Players[p].Lines {
		if l.Count > 0 && int(l.Count) <= r && left[l.Color] >= r+1-int(l.Count) {
			n++
		}
	}
	return n
}

func nearRows(w *game.Wall) int {
	n := 0
	for r := range 5 {
		if bits.OnesCount32(w.RowMask(r)) >= 4 {
			n++
		}
	}
	return n
}

// overflow counts tiles still on the table that player p could not place in
// any pattern line — a rough measure of floor exposure for the rest of the
// round.
func overflow(s *game.State, p int) int {
	pb := &s.Players[p]
	n := 0
	for c := game.Color(0); c < game.NumColors; c++ {
		left := int(s.Center[c])
		for f := range s.Factories {
			left += int(s.Factories[f][c])
		}
		if left == 0 {
			continue
		}
		room := 0
		for r := range game.NumRows {
			if pb.CanPlace(r, c) {
				room += r + 1 - int(pb.Lines[r].Count)
			}
		}
		n += max(left-room, 0)
	}
	return n
}

// Terminal returns the value of a finished game for player p: ±Win for the
// result, plus the score margin, plus the complete-row difference as the
// tiebreak (always smaller than one point).
func Terminal(s *game.State, p int) int {
	a, b := &s.Players[p], &s.Players[1-p]
	v := (int(a.Score)-int(b.Score))*Scale + a.Wall.CompleteRows() - b.Wall.CompleteRows()
	switch s.Winner() {
	case p:
		v += Win
	case 1 - p:
		v -= Win
	}
	return v
}

// Format renders a value in points, e.g. "+3.00", "win +12.00", "loss -4.00".
func Format(v int) string {
	switch {
	case v > Win/2:
		return fmt.Sprintf("win %+.2f", float64(v-Win)/Scale)
	case v < -Win/2:
		return fmt.Sprintf("loss %+.2f", float64(v+Win)/Scale)
	}
	return fmt.Sprintf("%+.2f", float64(v)/Scale)
}
