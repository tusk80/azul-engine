// Package puzzle finds positions with one clearly best move, for the
// analysis board's puzzle mode.
package puzzle

import (
	"math/rand/v2"

	"github.com/tusk80/azul-engine/internal/eval"
	"github.com/tusk80/azul-engine/internal/game"
	"github.com/tusk80/azul-engine/internal/search"
)

// MinGain is how much better than every other move the answer must be.
const MinGain = 3 * eval.Scale

// A Puzzle is a position whose round the search solved exactly, where the
// best move beats the second best by at least MinGain and is not the move
// a greedy player would make.
type Puzzle struct {
	State  game.State `json:"state"`
	Best   string     `json:"best"`
	Second string     `json:"second"`
	Gain   float64    `json:"gain"`           // points the best move is worth over the second
	Wins   bool       `json:"wins,omitempty"` // the best move wins the game and the second does not
	PV     []string   `json:"pv"`             // the best line to the end of the round
	Level  int        `json:"level"`          // see Rate
}

// Find checks whether s is a puzzle, searching with eng up to nodes nodes.
func Find(eng *search.Searcher, s *game.State, nodes uint64) (Puzzle, bool) {
	res := eng.Search(s, search.Options{MaxNodes: nodes, MultiPV: 2})
	if !res.Exact || len(res.Lines) < 2 {
		return Puzzle{}, false
	}
	a, b := res.Lines[0], res.Lines[1]
	if a.Score-b.Score < MinGain || a.Move == greedy(s) {
		return Puzzle{}, false
	}
	p := Puzzle{State: *s, Best: a.Move.String(), Second: b.Move.String()}
	if a.Score >= eval.Win/2 && b.Score < eval.Win/2 {
		p.Wins = true
	} else {
		p.Gain = float64(a.Score-b.Score) / eval.Scale
	}
	for _, m := range a.PV {
		p.PV = append(p.PV, m.String())
	}
	p.Level = Rate(s, a.Move)
	return p, true
}

// levelDepths are the search depths of the classic engine that rate a
// puzzle: Level 1 if the shallowest finds the answer, 2 if the next does,
// 3 if neither does.
var levelDepths = [...]int{2, 4}

// Rate returns how hard it is to find best in s: 1 easy, 2 medium, 3 hard.
func Rate(s *game.State, best game.Move) int {
	eng := search.New(16)
	for i, d := range levelDepths {
		if m, _ := eng.Search(s, search.Options{MaxDepth: d}).Best(); m == best {
			return i + 1
		}
	}
	return len(levelDepths) + 1
}

// greedy is the move that is best for the mover's own board right now.
func greedy(s *game.State) game.Move {
	var ml game.MoveList
	s.GenMoves(&ml)
	me := int(s.ToMove)
	best, bestV := ml.Moves[0], 0
	for i, m := range ml.Slice() {
		c := *s
		c.Apply(m)
		if v := eval.Default.Own(&c, me); i == 0 || v > bestV {
			best, bestV = m, v
		}
	}
	return best
}

// Game plays one self-play game with eng (nodes per move) and returns at
// most one puzzle per round, each checked with up to check nodes.
func Game(eng *search.Searcher, seed, nodes, check uint64) []Puzzle {
	rng := rand.New(rand.NewPCG(seed, 1))
	s := game.NewGame(int(seed % 2))
	s.Refill(rng)
	var out []Puzzle
	found := false
	for !s.GameOver {
		if s.RoundOver() {
			if !s.ResolveRound() {
				s.Refill(rng)
			}
			found = false
			continue
		}
		if !found {
			var p Puzzle
			if p, found = Find(eng, &s, check); found {
				out = append(out, p)
			}
		}
		s.Apply(eng.Search(&s, search.Options{MaxNodes: nodes}).Lines[0].Move)
	}
	return out
}
