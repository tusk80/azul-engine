package selfplay

import (
	"math"
	"math/rand/v2"

	"github.com/tusk80/azul-engine/internal/eval"
)

// SPSA tunes eval weights by simultaneous perturbation: each iteration
// shifts every weight by ±step at random, plays the "+" weights against the
// "−" weights, and moves all weights toward whichever side won.
type SPSA struct {
	Iters   int
	Pairs   int    // game pairs per iteration
	Limits  Player // search limits used for both sides (weights ignored)
	Workers int
	LR      float64 // learning rate; 0 = 1
	Seed    uint64
	OnIter  func(iter int, w eval.Weights, result float64)
}

func (t *SPSA) Run(start eval.Weights) eval.Weights {
	lr := t.LR
	if lr == 0 {
		lr = 1
	}
	w := start
	params := w.Params()
	theta := make([]float64, len(params))
	for i, p := range params {
		theta[i] = float64(*p.Ptr)
	}
	rng := rand.New(rand.NewPCG(t.Seed, 0x5b5a))
	delta := make([]float64, len(params))
	// Standard SPSA gain schedules, normalised so iteration 0 has gain 1.
	bigA := 0.1 * float64(t.Iters)
	for k := range t.Iters {
		ck := 1 / math.Pow(float64(k+1), 0.101)
		ak := lr * math.Pow(bigA+1, 0.602) / math.Pow(bigA+float64(k+1), 0.602)

		plus, minus := w, w
		pp, mp := plus.Params(), minus.Params()
		for i, p := range params {
			delta[i] = float64(2*rng.IntN(2) - 1)
			*pp[i].Ptr = int(math.Round(theta[i] + ck*p.Step*delta[i]))
			*mp[i].Ptr = int(math.Round(theta[i] - ck*p.Step*delta[i]))
		}
		a, b := t.Limits, t.Limits
		a.Weights, b.Weights = plus, minus
		st := Match(a, b, MatchConfig{
			Pairs:   t.Pairs,
			Workers: t.Workers,
			Seed:    t.Seed<<20 + uint64(k*t.Pairs),
		})
		result := float64(st.Wins-st.Losses) / float64(max(st.Games(), 1))
		for i, p := range params {
			theta[i] += ak * p.Step * result * delta[i]
			*params[i].Ptr = int(math.Round(theta[i]))
		}
		if t.OnIter != nil {
			t.OnIter(k, w, result)
		}
	}
	return w
}
