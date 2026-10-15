package nn

import (
	"bufio"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/tusk80/azul-engine/internal/dataset"
	"github.com/tusk80/azul-engine/internal/eval"
)

// Data is a set of positions with their labels, in points for the side to move.
type Data struct {
	X      [][dataset.NumFeatures]int16
	Eval   []float32 // the search's value
	Margin []float32 // the final score margin
	Result []int8
}

func (d *Data) Len() int { return len(d.X) }

// Append adds o's rows after d's.
func (d *Data) Append(o *Data) {
	d.X = append(d.X, o.X...)
	d.Eval = append(d.Eval, o.Eval...)
	d.Margin = append(d.Margin, o.Margin...)
	d.Result = append(d.Result, o.Result...)
}

// Split returns the first and the last part of d, cut at frac of the rows.
// Rows of one game are written together, so almost every game lands on one
// side only.
func (d *Data) Split(frac float64) (a, b *Data) {
	k := int(float64(d.Len()) * frac)
	return &Data{d.X[:k], d.Eval[:k], d.Margin[:k], d.Result[:k]},
		&Data{d.X[k:], d.Eval[k:], d.Margin[k:], d.Result[k:]}
}

// LoadCSV reads a file written by azul gendata.
func LoadCSV(path string) (*Data, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	if !sc.Scan() {
		return nil, fmt.Errorf("%s: empty", path)
	}
	if want := strings.Join(append(dataset.Names(), "eval", "margin", "result"), ","); sc.Text() != want {
		return nil, fmt.Errorf("%s: columns differ from this build's features; regenerate the data", path)
	}
	d := &Data{}
	var vals [dataset.NumFeatures + 3]int
	for line := 2; sc.Scan(); line++ {
		fields := strings.Split(sc.Text(), ",")
		if len(fields) != len(vals) {
			return nil, fmt.Errorf("%s:%d: %d columns, want %d", path, line, len(fields), len(vals))
		}
		for i, s := range fields {
			if vals[i], err = strconv.Atoi(s); err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, line, err)
			}
		}
		var x [dataset.NumFeatures]int16
		for i := range x {
			x[i] = int16(vals[i])
		}
		n := dataset.NumFeatures
		d.X = append(d.X, x)
		d.Eval = append(d.Eval, float32(vals[n])/eval.Scale)
		d.Margin = append(d.Margin, float32(vals[n+1]))
		d.Result = append(d.Result, int8(vals[n+2]))
	}
	return d, sc.Err()
}

// Config controls training.
type Config struct {
	Epochs  int
	Batch   int
	LR      float64 // Adam step size at the start; it falls linearly to a tenth
	Lambda  float64 // target = Lambda*eval + (1-Lambda)*margin
	Workers int
	Seed    uint64
	// Log is called after each epoch with the mean training loss and the
	// validation report.
	Log func(epoch int, trainLoss float64, val Report)
}

func (c *Config) target(d *Data, i int) float32 {
	return float32(c.Lambda)*d.Eval[i] + float32(1-c.Lambda)*d.Margin[i]
}

// FitScale sets each input's divisor to its root mean square over d, so
// every input arrives around 1 without moving zeros (which the sparse first
// layer skips).
func (n *Net) FitScale(d *Data) {
	sum := make([]float64, n.In)
	for _, x := range d.X {
		for i, v := range x {
			sum[i] += float64(v) * float64(v)
		}
	}
	for i := range sum {
		n.Scale[i] = 1
		if rms := math.Sqrt(sum[i] / float64(max(d.Len(), 1))); rms > 0 {
			n.Scale[i] = float32(rms)
		}
	}
}

// params lists the trainable slices of n, in a fixed order.
func (n *Net) params() [][]float32 { return [][]float32{n.W1, n.B1, n.W2, n.B2, n.W3, n.B3} }

// zeroLike returns a network of n's shape with every weight zero.
func (n *Net) zeroLike() *Net {
	z := &Net{In: n.In, H1: n.H1, H2: n.H2, Scale: n.Scale}
	ps := n.params()
	zs := make([][]float32, len(ps))
	for i, p := range ps {
		zs[i] = make([]float32, len(p))
	}
	z.W1, z.B1, z.W2, z.B2, z.W3, z.B3 = zs[0], zs[1], zs[2], zs[3], zs[4], zs[5]
	return z
}

// backward adds the gradient of 0.5*(out-t)² for one sample to g, using the
// activations forward left in sc, and returns that loss.
func (n *Net) backward(sc *scratch, out, t float32, g *Net, dh1 []float32) float64 {
	d := out - t
	g.B3[0] += d
	clear(dh1)
	for k, h := range sc.h2 {
		g.W3[k] += d * h
		if h <= 0 {
			continue
		}
		dk := d * n.W3[k]
		g.B2[k] += dk
		w, gw := n.W2[k*n.H1:(k+1)*n.H1], g.W2[k*n.H1:(k+1)*n.H1]
		for j, a := range sc.h1 {
			gw[j] += dk * a
			dh1[j] += dk * w[j]
		}
	}
	for j, a := range sc.h1 {
		if a <= 0 {
			dh1[j] = 0
		}
		g.B1[j] += dh1[j]
	}
	for i, x := range sc.x {
		if x == 0 {
			continue
		}
		gw := g.W1[i*n.H1 : (i+1)*n.H1]
		for j, dj := range dh1 {
			gw[j] += dj * x
		}
	}
	return 0.5 * float64(d) * float64(d)
}

// Train fits n to train with minibatch Adam, reporting on val after every
// epoch. Call FitScale first.
func Train(n *Net, train, val *Data, cfg Config) {
	workers := max(cfg.Workers, 1)
	type worker struct {
		g    *Net
		sc   *scratch
		dh1  []float32
		loss float64
	}
	ws := make([]worker, workers)
	for i := range ws {
		ws[i] = worker{g: n.zeroLike(), sc: n.newScratch(), dh1: make([]float32, n.H1)}
	}
	m, v := n.zeroLike(), n.zeroLike()
	const b1, b2, eps = 0.9, 0.999, 1e-8
	step := 0
	rng := rand.New(rand.NewPCG(cfg.Seed, 2))
	order := make([]int, train.Len())
	for i := range order {
		order[i] = i
	}
	batches := (len(order) + cfg.Batch - 1) / cfg.Batch
	for epoch := 1; epoch <= cfg.Epochs; epoch++ {
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		total := 0.0
		for b := range batches {
			batch := order[b*cfg.Batch : min((b+1)*cfg.Batch, len(order))]
			var wg sync.WaitGroup
			for w := range ws {
				wg.Add(1)
				go func() {
					defer wg.Done()
					wk := &ws[w]
					for _, p := range wk.g.params() {
						clear(p)
					}
					wk.loss = 0
					for k := w; k < len(batch); k += workers {
						i := batch[k]
						out := n.forward(&train.X[i], wk.sc)
						wk.loss += n.backward(wk.sc, out, cfg.target(train, i), wk.g, wk.dh1)
					}
				}()
			}
			wg.Wait()

			// Sum the workers' gradients into the first, then take an Adam step.
			g := ws[0].g.params()
			total += ws[0].loss
			for w := 1; w < workers; w++ {
				total += ws[w].loss
				for pi, p := range ws[w].g.params() {
					for i, x := range p {
						g[pi][i] += x
					}
				}
			}
			step++
			progress := float64((epoch-1)*batches+b) / float64(cfg.Epochs*batches)
			lr := cfg.LR * (1 - 0.9*progress)
			c1, c2 := 1-math.Pow(b1, float64(step)), 1-math.Pow(b2, float64(step))
			inv := 1 / float32(len(batch))
			mp, vp := m.params(), v.params()
			for pi, p := range n.params() {
				for i := range p {
					gi := g[pi][i] * inv
					mp[pi][i] = b1*mp[pi][i] + (1-b1)*gi
					vp[pi][i] = b2*vp[pi][i] + (1-b2)*gi*gi
					p[i] -= float32(lr * float64(mp[pi][i]) / c1 / (math.Sqrt(float64(vp[pi][i])/c2) + eps))
				}
			}
		}
		if cfg.Log != nil {
			cfg.Log(epoch, total/float64(len(order)), n.Report(val))
		}
	}
}

// Report compares the network with the hand-written evaluation on d, both
// as predictors of the final margin.
type Report struct {
	NetMSE, EvalMSE   float64 // mean squared error against the final margin, in points²
	NetSign, EvalSign float64 // share of decided games whose winner each one picks
	NetVsEval         float64 // mean squared difference between the two
}

func (n *Net) Report(d *Data) Report {
	var r Report
	sc := n.newScratch()
	decided := 0
	for i := range d.X {
		p := float64(n.forward(&d.X[i], sc))
		e, m := float64(d.Eval[i]), float64(d.Margin[i])
		r.NetMSE += (p - m) * (p - m)
		r.EvalMSE += (e - m) * (e - m)
		r.NetVsEval += (p - e) * (p - e)
		if res := d.Result[i]; res != 0 {
			decided++
			if (p > 0) == (res > 0) {
				r.NetSign++
			}
			if (e > 0) == (res > 0) {
				r.EvalSign++
			}
		}
	}
	k := float64(max(d.Len(), 1))
	r.NetMSE /= k
	r.EvalMSE /= k
	r.NetVsEval /= k
	r.NetSign /= float64(max(decided, 1))
	r.EvalSign /= float64(max(decided, 1))
	return r
}
