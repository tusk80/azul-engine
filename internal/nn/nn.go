// Package nn is a small neural network that values Azul positions: a
// two-hidden-layer perceptron over dataset.Features, trained by Train and
// plugged into the search through Net.Evaluate.
package nn

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sync"

	"github.com/tusk80/azul-engine/internal/dataset"
	"github.com/tusk80/azul-engine/internal/eval"
	"github.com/tusk80/azul-engine/internal/game"
)

// Net predicts, for the side to move, the final score margin in points.
//
// The first layer is stored input-major (W1[i*H1+j] links input i to hidden
// unit j) so a forward pass only touches the rows of nonzero inputs: most
// features are empty wall cells and lines.
type Net struct {
	In, H1, H2 int
	Scale      []float32 // inputs are divided by these before the first layer
	W1, B1     []float32
	W2, B2     []float32 // W2[j*H1+i]: hidden 1 unit i to hidden 2 unit j
	W3, B3     []float32 // B3 has one element
}

// New returns a randomly initialised network (He initialisation).
func New(h1, h2 int, seed uint64) *Net {
	in := dataset.NumFeatures
	n := &Net{
		In: in, H1: h1, H2: h2,
		Scale: make([]float32, in),
		W1:    make([]float32, in*h1), B1: make([]float32, h1),
		W2: make([]float32, h1*h2), B2: make([]float32, h2),
		W3: make([]float32, h2), B3: make([]float32, 1),
	}
	for i := range n.Scale {
		n.Scale[i] = 1
	}
	rng := rand.New(rand.NewPCG(seed, 1))
	fill := func(w []float32, fanIn int) {
		sd := math.Sqrt(2 / float64(fanIn))
		for i := range w {
			w[i] = float32(rng.NormFloat64() * sd)
		}
	}
	fill(n.W1, in)
	fill(n.W2, h1)
	fill(n.W3, h2)
	return n
}

// scratch holds one forward pass's activations.
type scratch struct {
	x      []float32
	h1, h2 []float32
}

func (n *Net) newScratch() *scratch {
	return &scratch{x: make([]float32, n.In), h1: make([]float32, n.H1), h2: make([]float32, n.H2)}
}

// forward fills sc from the raw features and returns the output.
func (n *Net) forward(f *[dataset.NumFeatures]int16, sc *scratch) float32 {
	copy(sc.h1, n.B1)
	for i, v := range f {
		if v == 0 {
			sc.x[i] = 0
			continue
		}
		x := float32(v) / n.Scale[i]
		sc.x[i] = x
		w := n.W1[i*n.H1 : (i+1)*n.H1]
		for j := range sc.h1 {
			sc.h1[j] += w[j] * x
		}
	}
	for j := range sc.h1 {
		sc.h1[j] = max(sc.h1[j], 0)
	}
	out := n.B3[0]
	for k := range sc.h2 {
		w := n.W2[k*n.H1 : (k+1)*n.H1]
		a := n.B2[k]
		for j, h := range sc.h1 {
			a += w[j] * h
		}
		a = max(a, 0)
		sc.h2[k] = a
		out += n.W3[k] * a
	}
	return out
}

// Predict returns the predicted margin in points for the side to move.
func (n *Net) Predict(f *[dataset.NumFeatures]int16) float32 {
	return n.forward(f, n.newScratch())
}

// q1 is the fixed-point scale of the evaluator's first layer.
const q1 = 1 << 12

// rebuildAbove is how many changed inputs make a fresh first layer cheaper
// than updating the last one.
const rebuildAbove = 24

// Evaluator values positions with n for the search, the way chess engines
// run their networks. The first layer is kept from one call to the next and
// only the inputs that changed are applied: the search evaluates positions
// that differ by a move or two, so that is a few rows instead of dozens. It
// works in integers (the inputs pre-divided by Scale, in fixed point) so
// thousands of updates add up exactly, with no drift. Use one per Searcher.
type Evaluator struct {
	net  *Net
	w1   []int32 // W1 / Scale * q1, input-major like W1
	b1   []int32
	acc  []int32 // first layer before ReLU, for prev
	prev [dataset.NumFeatures]int16
	ok   bool      // acc holds prev
	w2t  []float32 // W2 transposed, hidden-1-major: only active units are read
	acc2 []float32
}

func (n *Net) Evaluator() *Evaluator {
	e := &Evaluator{
		net:  n,
		w1:   make([]int32, len(n.W1)),
		b1:   make([]int32, n.H1),
		acc:  make([]int32, n.H1),
		w2t:  make([]float32, len(n.W2)),
		acc2: make([]float32, n.H2),
	}
	for k := range n.H2 {
		for j := range n.H1 {
			e.w2t[j*n.H2+k] = n.W2[k*n.H1+j]
		}
	}
	for i := range n.In {
		for j := range n.H1 {
			e.w1[i*n.H1+j] = int32(math.Round(float64(n.W1[i*n.H1+j]/n.Scale[i]) * q1))
		}
	}
	for j, b := range n.B1 {
		e.b1[j] = int32(math.Round(float64(b) * q1))
	}
	return e
}

// value is the network's output for f, reusing the first layer of the last
// call when few inputs changed.
func (e *Evaluator) value(f *[dataset.NumFeatures]int16) float32 {
	n := e.net
	h := n.H1
	var changed [rebuildAbove]uint8
	nc := 0
	if e.ok {
		for i, v := range f {
			if v != e.prev[i] {
				if nc == rebuildAbove {
					nc = -1
					break
				}
				changed[nc] = uint8(i)
				nc++
			}
		}
	}
	if !e.ok || nc < 0 {
		copy(e.acc, e.b1)
		for i, v := range f {
			if v != 0 {
				addRow(e.acc, e.w1[i*h:(i+1)*h], int32(v))
			}
		}
	} else {
		for _, i := range changed[:nc] {
			addRow(e.acc, e.w1[int(i)*h:(int(i)+1)*h], int32(f[i]-e.prev[i]))
		}
	}
	e.prev, e.ok = *f, true

	// Second layer from the active first-layer units only: ReLU zeroes
	// about half of them.
	copy(e.acc2, n.B2)
	h2 := n.H2
	for j, a := range e.acc {
		if a > 0 {
			axpy(e.acc2, e.w2t[j*h2:(j+1)*h2], float32(a)*(1.0/q1))
		}
	}
	out := n.B3[0]
	for k, a := range e.acc2 {
		if a > 0 {
			out += n.W3[k] * a
		}
	}
	return out
}

// addRow adds d times w to acc.
func addRow(acc, w []int32, d int32) {
	w = w[:len(acc)]
	for j := range acc {
		acc[j] += w[j] * d
	}
}

// axpy adds x times w to y.
func axpy(y, w []float32, x float32) {
	w = w[:len(y)]
	for k := range y {
		y[k] += w[k] * x
	}
}

// Evaluate returns the value of s for player p in eval units, the same
// contract as eval.Weights.Evaluate. A finished round is scored first and
// the network values the start of the next one; a finished game is exact.
func (e *Evaluator) Evaluate(s *game.State, p int) int {
	if s.GameOver {
		return eval.Terminal(s, p)
	}
	c := s
	if s.RoundOver() {
		r := *s
		if r.ResolveRound() {
			return eval.Terminal(&r, p)
		}
		c = &r
	}
	f := dataset.Features(c)
	v := int(e.value(&f) * eval.Scale)
	// A predicted margin is not a result: keep it clear of the win range.
	v = max(min(v, eval.Win/4), -eval.Win/4)
	if int(c.ToMove) != p {
		return -v
	}
	return v
}

// Save writes n as JSON.
func (n *Net) Save(path string) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

//go:embed default-net.json
var defaultNet []byte

// Default is the network the engine ships with: 64x16, trained on 1.5
// million self-play positions (see docs/ML.md).
var Default = sync.OnceValue(func() *Net {
	n, err := parse(defaultNet, "default-net.json")
	if err != nil {
		panic(err)
	}
	return n
})

// Load reads a network written by Save (or by the PyTorch trainer).
func Load(path string) (*Net, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(data, path)
}

func parse(data []byte, path string) (*Net, error) {
	var n Net
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if n.In != dataset.NumFeatures || len(n.Scale) != n.In || len(n.W1) != n.In*n.H1 || len(n.B1) != n.H1 ||
		len(n.W2) != n.H1*n.H2 || len(n.B2) != n.H2 || len(n.W3) != n.H2 || len(n.B3) != 1 {
		return nil, fmt.Errorf("%s: layer sizes do not match (%d inputs, want %d)", path, n.In, dataset.NumFeatures)
	}
	return &n, nil
}
