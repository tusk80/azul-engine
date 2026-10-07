// Package selfplay runs engine-vs-engine matches and tunes eval weights.
package selfplay

import (
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/tusk80/azul-engine/eval"
	"github.com/tusk80/azul-engine/game"
	"github.com/tusk80/azul-engine/search"
)

// Player is an engine configuration: weights plus search limits.
type Player struct {
	Weights   eval.Weights
	Lookahead search.Lookahead
	Nodes     uint64        // per-move node budget (deterministic)
	Depth     int           // per-move depth limit
	Time      time.Duration // per-move time limit (not deterministic)
}

func (p *Player) options() search.Options {
	return search.Options{MaxNodes: p.Nodes, MaxDepth: p.Depth, MoveTime: p.Time}
}

// maxRounds guards against engines that never finish a wall row.
const maxRounds = 40

// Game plays one game with pl[i] in seat i (seat 0 moves first) and
// returns the final state. With node or depth limits it is deterministic
// for a given seed.
func Game(eng [2]*search.Searcher, pl [2]*Player, seed uint64) game.State {
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	for i := range eng {
		eng[i].Weights = pl[i].Weights
		eng[i].Lookahead = pl[i].Lookahead
		eng[i].Clear()
	}
	s := game.NewGame(0)
	s.Refill(rng)
	for !s.GameOver && s.Round <= maxRounds {
		if s.RoundOver() {
			if !s.ResolveRound() {
				s.Refill(rng)
			}
			continue
		}
		p := s.ToMove
		m, _ := eng[p].Search(&s, pl[p].options()).Best()
		s.Apply(m)
	}
	return s
}

// Stats are match results from player A's point of view.
type Stats struct {
	Wins, Draws, Losses int
	PointDiff           int // A's points minus B's, summed over games
}

func (s Stats) Games() int { return s.Wins + s.Draws + s.Losses }

// Score is A's result rate: (wins + draws/2) / games.
func (s Stats) Score() float64 {
	if s.Games() == 0 {
		return 0.5
	}
	return (float64(s.Wins) + float64(s.Draws)/2) / float64(s.Games())
}

// Elo returns A's Elo advantage and the half-width of its 95% interval.
func (s Stats) Elo() (elo, margin float64) {
	n := float64(s.Games())
	if n == 0 {
		return 0, 0
	}
	mu := s.Score()
	variance := (float64(s.Wins)+float64(s.Draws)/4)/n - mu*mu
	se := math.Sqrt(max(variance, 0) / n)
	return eloOf(mu), (eloOf(mu+1.96*se) - eloOf(mu-1.96*se)) / 2
}

func eloOf(score float64) float64 {
	score = min(max(score, 1e-3), 1-1e-3)
	return -400 * math.Log10(1/score-1)
}

func (s *Stats) add(o Stats) {
	s.Wins += o.Wins
	s.Draws += o.Draws
	s.Losses += o.Losses
	s.PointDiff += o.PointDiff
}

// MatchConfig controls a match between two players.
type MatchConfig struct {
	Pairs    int    // game pairs; each deal is played twice with seats swapped
	Workers  int    // parallel games; 0 = NumCPU
	Seed     uint64 // first deal seed; pair i uses Seed+i
	TTMB     int    // transposition table per engine; 0 = 4 MB
	Progress func(Stats)
}

// Match plays A against B and returns A's results.
func Match(a, b Player, cfg MatchConfig) Stats {
	workers := cfg.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	ttMB := cfg.TTMB
	if ttMB <= 0 {
		ttMB = 4
	}
	jobs := make(chan int)
	results := make(chan Stats)
	var wg sync.WaitGroup
	for range min(workers, max(cfg.Pairs, 1)) {
		wg.Go(func() {
			ea, eb := search.New(ttMB), search.New(ttMB)
			for i := range jobs {
				seed := cfg.Seed + uint64(i)
				var st Stats
				s := Game([2]*search.Searcher{ea, eb}, [2]*Player{&a, &b}, seed)
				st.record(&s, 0)
				s = Game([2]*search.Searcher{eb, ea}, [2]*Player{&b, &a}, seed)
				st.record(&s, 1)
				results <- st
			}
		})
	}
	go func() {
		for i := range cfg.Pairs {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	var total Stats
	for st := range results {
		total.add(st)
		if cfg.Progress != nil {
			cfg.Progress(total)
		}
	}
	return total
}

// record adds one finished game where A sat in seat seatA.
func (st *Stats) record(s *game.State, seatA int) {
	st.PointDiff += int(s.Players[seatA].Score) - int(s.Players[1-seatA].Score)
	switch s.Winner() {
	case seatA:
		st.Wins++
	case -1:
		st.Draws++
	default:
		st.Losses++
	}
}
