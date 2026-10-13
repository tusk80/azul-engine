package dataset

import (
	"testing"

	"github.com/tusk80/azul-engine/internal/game"
	"github.com/tusk80/azul-engine/internal/search"
)

func TestFeatures(t *testing.T) {
	if len(Names()) != NumFeatures {
		t.Fatalf("%d names for %d features", len(Names()), NumFeatures)
	}
	col := map[string]int{}
	for i, n := range Names() {
		col[n] = i
	}
	var s game.State
	s.ToMove = 1
	s.Players[1].Lines[2] = game.Line{Color: 3, Count: 2}
	s.Players[1].Floor, s.Players[1].Score = 2, 9
	s.Players[0].Wall.Rows = 1 << 7
	s.Factories[0][1], s.Factories[4][1], s.Center[4] = 2, 1, 3
	s.NextFirst = 1
	f := Features(&s)
	want := map[string]int16{
		"me_line3_" + game.Color(3).String(): 2, "me_floor": 2, "me_score": 9, "opp_wall_1_2": 1,
		"factories_" + game.Color(1).String(): 3, "center_" + game.Color(4).String(): 3,
		"factories_left": 2, "token_mine": 1,
	}
	for i, n := range Names() {
		if f[i] != want[n] {
			t.Errorf("%s = %d, want %d", n, f[i], want[n])
		}
	}
}

func TestGame(t *testing.T) {
	rows := Game(search.New(1), 7, 300, 0.1)
	if len(rows) < 20 {
		t.Fatalf("only %d rows", len(rows))
	}
	// The two players see opposite results.
	seen := map[int]bool{}
	for _, r := range rows {
		seen[r.Result] = true
		if (r.Margin > 0) != (r.Result > 0) && r.Margin != 0 {
			t.Fatalf("margin %d with result %d", r.Margin, r.Result)
		}
	}
	if !(seen[1] && seen[-1]) && !seen[0] {
		t.Fatalf("results %v: expected both a winner's and a loser's rows", seen)
	}
}
