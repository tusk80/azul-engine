package selfplay

import (
	"math"
	"testing"

	"github.com/tusk80/azul-engine/internal/eval"
)

func TestMatchIsDeterministic(t *testing.T) {
	a := Player{Weights: eval.Default, Nodes: 2000}
	b := Player{Weights: eval.Weights{}, Nodes: 2000}
	cfg := MatchConfig{Pairs: 6, Workers: 3, Seed: 42}
	first := Match(a, b, cfg)
	if first.Games() != 12 {
		t.Fatalf("played %d games, want 12", first.Games())
	}
	cfg.Workers = 1
	if again := Match(a, b, cfg); again != first {
		t.Fatalf("results differ between runs: %+v vs %+v", first, again)
	}
}

func TestSelfMatchIsBalanced(t *testing.T) {
	// Identical players with swapped seats on the same deals: every pair
	// is exactly mirrored, so the match must be even.
	p := Player{Weights: eval.Default, Nodes: 2000}
	st := Match(p, p, MatchConfig{Pairs: 8, Seed: 7})
	if st.Wins != st.Losses || st.PointDiff != 0 {
		t.Fatalf("self-match not balanced: %+v", st)
	}
}

func TestElo(t *testing.T) {
	if elo, _ := (Stats{Wins: 50, Losses: 50}).Elo(); elo != 0 {
		t.Errorf("even score Elo = %v", elo)
	}
	elo, margin := Stats{Wins: 76, Losses: 24}.Elo()
	if math.Abs(elo-200) > 5 || margin <= 0 {
		t.Errorf("76%% score: elo %.1f ±%.1f, want about +200", elo, margin)
	}
}
