package nn

import (
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/tusk80/azul-engine/internal/dataset"
	"github.com/tusk80/azul-engine/internal/eval"
	"github.com/tusk80/azul-engine/internal/game"
)

func sample(rng *rand.Rand) (f [dataset.NumFeatures]int16) {
	for i := range f {
		if rng.IntN(3) == 0 {
			f[i] = int16(rng.IntN(5))
		}
	}
	return f
}

// The hand-written backward pass must match the slope found by nudging
// each weight a little.
func TestGradientCheck(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	n := New(8, 4, 3)
	for i := range n.B1 {
		n.B1[i] = 0.5 // keep units away from the ReLU kink
	}
	for i := range n.B2 {
		n.B2[i] = 0.5
	}
	f := sample(rng)
	const target = 3
	loss := func() float64 {
		d := float64(n.Predict(&f)) - target
		return 0.5 * d * d
	}
	g, sc := n.zeroLike(), n.newScratch()
	n.backward(sc, n.forward(&f, sc), target, g, make([]float32, n.H1))

	checked := 0
	for pi, p := range n.params() {
		for i := range p {
			if pi == 0 && f[i/n.H1] == 0 {
				continue // a zero input has no gradient to check
			}
			old := p[i]
			const h = 1e-2
			p[i] = old + h
			up := loss()
			p[i] = old - h
			down := loss()
			p[i] = old
			num := (up - down) / (2 * h)
			got := float64(g.params()[pi][i])
			if math.Abs(num-got) > 1e-2*max(1, math.Abs(num)) {
				t.Fatalf("param %d[%d]: backprop %.5f, numeric %.5f", pi, i, got, num)
			}
			checked++
		}
	}
	if checked < 100 {
		t.Fatalf("only %d weights checked", checked)
	}
}

// Training must learn a simple rule well beyond guessing.
func TestTrainLearns(t *testing.T) {
	rng := rand.New(rand.NewPCG(2, 2))
	d := &Data{}
	for range 4000 {
		f := sample(rng)
		y := float32(f[0]) - 2*float32(f[60]) + float32(f[3]*f[4])
		d.X = append(d.X, f)
		d.Eval = append(d.Eval, y)
		d.Margin = append(d.Margin, y)
		d.Result = append(d.Result, 0)
	}
	train, val := d.Split(0.9)
	n := New(32, 16, 4)
	n.FitScale(train)
	before := n.Report(val).NetMSE
	Train(n, train, val, Config{Epochs: 30, Batch: 64, LR: 3e-3, Lambda: 0.5, Workers: 4, Seed: 5})
	after := n.Report(val).NetMSE
	if after > before/10 {
		t.Fatalf("validation error %.3f -> %.3f: did not learn", before, after)
	}
}

func TestEvaluateAndSaveLoad(t *testing.T) {
	n := New(16, 8, 7)
	path := filepath.Join(t.TempDir(), "net.json")
	if err := n.Save(path); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := game.NewGame(0)
	s.Refill(rand.New(rand.NewPCG(9, 9)))
	e := m.Evaluator()
	// The two players' values are opposite.
	if a, b := e.Evaluate(&s, 0), e.Evaluate(&s, 1); a != -b {
		t.Fatalf("values %d and %d are not opposite", a, b)
	}
	f := dataset.Features(&s)
	if got, want := e.Evaluate(&s, int(s.ToMove)), int(n.Predict(&f)*eval.Scale); got != want {
		t.Fatalf("loaded net gives %d, original %d", got, want)
	}
	// A finished game is scored exactly.
	s.GameOver = true
	if got, want := e.Evaluate(&s, 0), eval.Terminal(&s, 0); got != want {
		t.Fatalf("finished game: %d, want %d", got, want)
	}
}

// The cost of one position's value, against the hand-written eval.
func BenchmarkEvaluate(b *testing.B) {
	s := game.NewGame(0)
	s.Refill(rand.New(rand.NewPCG(9, 9)))
	var ml game.MoveList
	s.GenMoves(&ml)
	s.Apply(ml.Moves[0])
	b.Run("handwritten", func(b *testing.B) {
		for b.Loop() {
			eval.Evaluate(&s, 0)
		}
	})
	for _, h := range [][2]int{{32, 16}, {64, 16}, {128, 32}, {256, 32}} {
		e := New(h[0], h[1], 1).Evaluator()
		b.Run(fmt.Sprintf("net-%dx%d", h[0], h[1]), func(b *testing.B) {
			for b.Loop() {
				e.Evaluate(&s, 0)
			}
		})
	}
}
