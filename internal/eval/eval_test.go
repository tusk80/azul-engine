package eval

import (
	"math/rand/v2"
	"testing"

	"github.com/tusk80/azul-engine/internal/game"
)

func wallOf(rows ...string) game.Wall {
	var w game.Wall
	for r, row := range rows {
		for col := 0; col < len(row); col++ {
			if row[col] == 'x' {
				w.Place(r, col)
			}
		}
	}
	return w
}

func TestAdjacency(t *testing.T) {
	tests := []struct {
		wall []string
		want int
	}{
		{[]string{"....."}, 0},
		{[]string{"xx..."}, 1},
		{[]string{"xxxxx"}, 4},
		{[]string{"x....", "x...."}, 1},
		{[]string{"....x", "x...."}, 0}, // no wrap across rows
		{[]string{"xx...", "xx..."}, 4},
		{[]string{"xxxxx", "xxxxx", "xxxxx", "xxxxx", "xxxxx"}, 40},
	}
	for _, tt := range tests {
		if got := Adjacency(wallOf(tt.wall...)); got != tt.want {
			t.Errorf("Adjacency(%v) = %d, want %d", tt.wall, got, tt.want)
		}
	}
}

func TestWallPotential(t *testing.T) {
	tests := []struct {
		wall []string
		want int
	}{
		{nil, 25},                       // every cell isolated
		{[]string{"x...."}, 22 + 2 + 2}, // (0,1) and (1,0) would score 2
		{[]string{"xxxxx", "xxxxx", "xxxxx", "xxxxx", "xxxxx"}, 0},
	}
	for _, tt := range tests {
		if got := WallPotential(wallOf(tt.wall...)); got != tt.want {
			t.Errorf("WallPotential(%v) = %d, want %d", tt.wall, got, tt.want)
		}
	}
}

// slowWallPotential places a tile on every empty cell of a copy.
func slowWallPotential(w game.Wall) int {
	n := 0
	for r := range 5 {
		for col := range 5 {
			if !w.Has(r, col) {
				c := w
				n += c.Place(r, col)
			}
		}
	}
	return n
}

func TestWallPotentialMatchesSlow(t *testing.T) {
	rng := rand.New(rand.NewPCG(4, 4))
	for range 20000 {
		var w game.Wall
		bitsSet := rng.Uint32() & rng.Uint32() & (1<<25 - 1) // sparse and dense walls
		for i := range 25 {
			if bitsSet>>i&1 != 0 {
				w.Place(i/5, i%5)
			}
		}
		if got, want := WallPotential(w), slowWallPotential(w); got != want {
			t.Fatalf("wall %025b: got %d, want %d", w.Rows, got, want)
		}
	}
}

func TestCompletable(t *testing.T) {
	s := game.NewGame(0)
	s.Factories[0] = [game.NumColors]uint8{game.Red: 2, game.Blue: 1}
	s.Center[game.Red] = 1
	p := &s.Players[0]
	p.Lines[4] = game.Line{Color: game.Red, Count: 2}   // needs 3 red: 3 left → yes
	p.Lines[3] = game.Line{Color: game.Blue, Count: 1}  // needs 3 blue: 1 left → no
	p.Lines[1] = game.Line{Color: game.White, Count: 2} // full: not partial
	if got := completable(&s, 0); got != 1 {
		t.Fatalf("completable = %d, want 1", got)
	}
}

func TestLineMissing(t *testing.T) {
	w := Weights{LineMissing: -10}
	var pb game.PlayerBoard
	pb.Lines[4] = game.Line{Color: game.Red, Count: 2}  // 3 missing
	pb.Lines[1] = game.Line{Color: game.Blue, Count: 1} // 1 missing
	if got := w.board(&pb); got != -40 {
		t.Fatalf("board = %d, want -40", got)
	}
}

func TestBoardTerms(t *testing.T) {
	w := Weights{LineFill: 100, LineBase: -10}
	var pb game.PlayerBoard
	pb.Wall = wallOf("xx...")
	// Row 3 holds 2 of 3 blues; blue would land isolated at (2,2) for 1 point.
	pb.Lines[2] = game.Line{Color: game.Blue, Count: 2}
	if got, want := w.board(&pb), 100*1*2/3-10; got != want {
		t.Fatalf("board = %d, want %d", got, want)
	}

	w = Weights{Col3: 7, Col4: 11, Row4: 13, Color3: 17, Color4: 19}
	pb = game.PlayerBoard{Wall: wallOf("xxxx.", "x....", "x....", "x....")}
	// Row 1 and column 1 each have 4 tiles. Colors: row 1 holds blue, yellow,
	// red, black; column 1 below it holds white, black, red. No color reaches 3.
	if got, want := w.board(&pb), 13+11; got != want {
		t.Fatalf("bonus terms = %d, want %d", got, want)
	}
}

func TestZeroWeightsIsProjectedScore(t *testing.T) {
	var w Weights
	s := game.NewGame(0)
	s.Players[0].Score = 10
	s.Players[0].Lines[0] = game.Line{Color: game.Blue, Count: 1}
	s.Players[1].Floor = 2
	s.Players[1].Score = 1
	// P1: 10 + 1 = 11. P2: 1 - 2 → clamped 0.
	if got := w.Evaluate(&s, 0); got != 11*Scale {
		t.Fatalf("eval = %d, want %d", got, 11*Scale)
	}
}

func TestSymmetric(t *testing.T) {
	var ml game.MoveList
	for seed := range uint64(50) {
		rng := rand.New(rand.NewPCG(seed, 3))
		s := game.NewGame(0)
		s.Refill(rng)
		for !s.GameOver {
			if a, b := Evaluate(&s, 0), Evaluate(&s, 1); a != -b {
				t.Fatalf("seed %d: eval not symmetric: %d vs %d\n%v", seed, a, b, s)
			}
			if s.RoundOver() {
				if !s.ResolveRound() {
					s.Refill(rng)
				}
				continue
			}
			s.GenMoves(&ml)
			s.Apply(ml.Moves[rng.IntN(ml.N)])
		}
	}
}

func TestMidRoundProjectionNeverClaimsWin(t *testing.T) {
	s := game.NewGame(0)
	s.Center[game.Red] = 1 // round still running
	s.Players[0].Wall = wallOf("xxxx.")
	s.Players[0].Lines[0] = game.Line{Color: game.White, Count: 1}
	if v := Evaluate(&s, 0); v > Win/2 {
		t.Fatalf("mid-round eval %d claims a win", v)
	}
	s.Center[game.Red] = 0
	if v := Evaluate(&s, 0); v < Win/2 {
		t.Fatalf("end-of-round eval %d should be a win", v)
	}
}

func BenchmarkEvaluate(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 1))
	s := game.NewGame(0)
	s.Refill(rng)
	var ml game.MoveList
	for range 6 {
		s.GenMoves(&ml)
		s.Apply(ml.Moves[rng.IntN(ml.N)])
	}
	for b.Loop() {
		Evaluate(&s, 0)
	}
}
