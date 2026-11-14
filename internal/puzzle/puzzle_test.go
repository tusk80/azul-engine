package puzzle

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tusk80/azul-engine/internal/eval"
	"github.com/tusk80/azul-engine/internal/game"
	"github.com/tusk80/azul-engine/internal/search"
)

// The board's puzzles must load as valid positions with both moves legal.
func TestShippedPuzzles(t *testing.T) {
	data, err := os.ReadFile("../server/ui/puzzles.json")
	if err != nil {
		t.Fatal(err)
	}
	var all []Puzzle
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	if len(all) < 100 {
		t.Fatalf("only %d puzzles", len(all))
	}
	for i, p := range all {
		var ml game.MoveList
		p.State.GenMoves(&ml)
		legal := map[string]bool{}
		for _, m := range ml.Slice() {
			legal[m.String()] = true
		}
		if !legal[p.Best] || !legal[p.Second] || p.Best == p.Second {
			t.Fatalf("puzzle %d: moves %q / %q", i+1, p.Best, p.Second)
		}
		if p.Level < 1 || p.Level > len(levelDepths)+1 {
			t.Fatalf("puzzle %d: level %d", i+1, p.Level)
		}
		if len(p.PV) == 0 || p.PV[0] != p.Best {
			t.Fatalf("puzzle %d: line %v does not start with %q", i+1, p.PV, p.Best)
		}
	}
}

// Every puzzle's answer must be legal, beat the second move by MinGain when
// both are replayed and searched again, and differ from the greedy move.
func TestGamePuzzles(t *testing.T) {
	eng := search.New(16)
	var all []Puzzle
	for seed := uint64(1); len(all) < 3 && seed < 20; seed++ {
		all = append(all, Game(eng, seed, 5000, 200000)...)
	}
	if len(all) == 0 {
		t.Fatal("no puzzles in 19 games")
	}
	check := search.New(16)
	for i, p := range all {
		values := map[string]int{}
		var ml game.MoveList
		p.State.GenMoves(&ml)
		for _, m := range ml.Slice() {
			if s := m.String(); s == p.Best || s == p.Second {
				c := p.State
				c.Apply(m)
				v := 0
				if !c.RoundOver() {
					res := check.Search(&c, search.Options{})
					v = -res.Lines[0].Score
				} else {
					v = eval.Evaluate(&c, int(p.State.ToMove))
				}
				values[s] = v
			}
		}
		best, ok1 := values[p.Best]
		second, ok2 := values[p.Second]
		if !ok1 || !ok2 {
			t.Fatalf("puzzle %d: moves %q / %q not legal", i, p.Best, p.Second)
		}
		if best-second < MinGain {
			t.Fatalf("puzzle %d: best %d, second %d: gain below %d", i, best, second, MinGain)
		}
		if greedy(&p.State).String() == p.Best {
			t.Fatalf("puzzle %d: the answer is the greedy move", i)
		}
	}
}
