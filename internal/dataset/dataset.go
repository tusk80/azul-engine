// Package dataset turns positions into rows of numbers for training a
// learned evaluation, and generates those rows by self-play.
package dataset

import (
	"fmt"
	"math/rand/v2"

	"github.com/tusk80/azul-engine/internal/eval"
	"github.com/tusk80/azul-engine/internal/game"
	"github.com/tusk80/azul-engine/internal/search"
)

const (
	// Per player: wall cells, line counts by row and color, floor, score, and
	// the wall and score if the round were scored now.
	perPlayer = 25 + 25 + 2 + 25 + 1
	// NumFeatures is the length of a feature row.
	NumFeatures = 2*perPlayer + 5 + 5 + 4
)

// Features describes s from the side to move's point of view, as small
// whole numbers: the mover's board, the opponent's board, then the table.
// Each board also appears as it would after scoring the round now, the way
// the hand-written eval sees it, so the network need not learn the scoring
// rules from examples.
// Scaling is left to the model, so the same row feeds training and play.
func Features(s *game.State) (f [NumFeatures]int16) {
	me := int(s.ToMove)
	proj := *s
	proj.ProjectRound()
	i := 0
	for _, p := range [2]int{me, 1 - me} {
		pb, pp := &s.Players[p], &proj.Players[p]
		for cell := range 25 {
			f[i+cell] = int16(pb.Wall.Rows >> cell & 1)
		}
		i += 25
		for r, l := range pb.Lines {
			if l.Count > 0 {
				f[i+r*5+int(l.Color)] = int16(l.Count)
			}
		}
		i += 25
		f[i], f[i+1] = int16(pb.Floor), pb.Score
		i += 2
		for cell := range 25 {
			f[i+cell] = int16(pp.Wall.Rows >> cell & 1)
		}
		f[i+25] = pp.Score
		i += 26
	}
	for c := range game.NumColors {
		f[i+c] = int16(s.Center[c])
		for fac := range s.Factories {
			f[i+5+c] += int16(s.Factories[fac][c])
		}
	}
	i += 10
	for fac := range s.Factories {
		if s.Factories[fac] != [game.NumColors]uint8{} {
			f[i]++
		}
	}
	if s.TokenInCenter {
		f[i+1] = 1
	} else if int(s.NextFirst) == me {
		f[i+2] = 1
	}
	f[i+3] = int16(s.Round)
	return f
}

// Names labels the feature columns, in order.
func Names() []string {
	var n []string
	for _, who := range [2]string{"me", "opp"} {
		for cell := range 25 {
			n = append(n, fmt.Sprintf("%s_wall_%d_%d", who, cell/5, cell%5))
		}
		for cell := range 25 {
			n = append(n, fmt.Sprintf("%s_line%d_%s", who, cell/5+1, game.Color(cell%5)))
		}
		n = append(n, who+"_floor", who+"_score")
		for cell := range 25 {
			n = append(n, fmt.Sprintf("%s_scored_wall_%d_%d", who, cell/5, cell%5))
		}
		n = append(n, who+"_scored_score")
	}
	for _, where := range [2]string{"center", "factories"} {
		for c := range game.NumColors {
			n = append(n, fmt.Sprintf("%s_%s", where, game.Color(c)))
		}
	}
	return append(n, "factories_left", "token_in_center", "token_mine", "round")
}

// Row is one training example: a position and what became of it.
type Row struct {
	Features [NumFeatures]int16
	Eval     int // the search's value for the side to move, in eval units
	Margin   int // final score difference for the side to move
	Result   int // 1 win, 0 shared, -1 loss, for the side to move
}

// Game plays one self-play game and returns a row per position searched,
// plus one at the start of each new round, before the deal.
// With probability explore a random move is played instead of the engine's,
// so games from different seeds do not all follow the same habits.
func Game(eng *search.Searcher, seed, nodes uint64, explore float64) []Row {
	rng := rand.New(rand.NewPCG(seed, 0))
	s := game.NewGame(int(seed % 2))
	s.Refill(rng)
	var rows []Row
	var movers []int
	var ml game.MoveList
	for !s.GameOver {
		if s.RoundOver() {
			if !s.ResolveRound() {
				// The search values round ends by scoring them, so a learned eval
				// meets positions with an empty table too: record one per round.
				rows = append(rows, Row{Features: Features(&s), Eval: eval.Evaluate(&s, int(s.ToMove))})
				movers = append(movers, int(s.ToMove))
				s.Refill(rng)
			}
			continue
		}
		res := eng.Search(&s, search.Options{MaxNodes: nodes, MultiPV: 1})
		m := res.Lines[0].Move
		// Positions the search already knows are won or lost carry no eval worth learning.
		if v := res.Lines[0].Score; v > -eval.Win/2 && v < eval.Win/2 {
			rows = append(rows, Row{Features: Features(&s), Eval: v})
			movers = append(movers, int(s.ToMove))
		}
		if rng.Float64() < explore {
			s.GenMoves(&ml)
			m = ml.Moves[rng.IntN(ml.N)]
		}
		s.Apply(m)
	}
	for i, p := range movers {
		rows[i].Margin = int(s.Players[p].Score) - int(s.Players[1-p].Score)
		switch s.Winner() {
		case p:
			rows[i].Result = 1
		case 1 - p:
			rows[i].Result = -1
		}
	}
	return rows
}
