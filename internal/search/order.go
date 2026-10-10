package search

import "github.com/tusk80/azul-engine/internal/game"

const (
	scoreTT     = 1 << 30
	scoreKiller = 1 << 22
	historyMax  = 1 << 16
)

// quickScore is a cheap static guess of how good a move is: tiles placed,
// a bonus for exactly finishing a line, and penalties for floor tiles and
// for taking the first-player token.
func quickScore(s *game.State, m game.Move) int32 {
	src, c, dst := m.Src(), m.Color(), m.Dst()
	var n int32
	token := false
	if src == game.SrcCenter {
		n = int32(s.Center[c])
		token = s.TokenInCenter
	} else {
		n = int32(s.Factories[src][c])
	}
	var sc int32
	spill := n
	if dst != game.DstFloor {
		room := int32(dst+1) - int32(s.Players[s.ToMove].Lines[dst].Count)
		put := min(n, room)
		spill = n - put
		sc += put * 10
		if put == room {
			sc += 50
		}
	}
	sc -= spill * 25
	if token {
		sc -= 15
	}
	return sc
}

func (e *Searcher) scoreMoves(s *game.State, ml *game.MoveList, sc *[game.MaxMoves]int32, ttMove game.Move, ply int) {
	hist := &e.history[s.ToMove]
	for i, m := range ml.Slice() {
		switch m {
		case ttMove:
			sc[i] = scoreTT
		case e.killers[ply][0]:
			sc[i] = scoreKiller + 1
		case e.killers[ply][1]:
			sc[i] = scoreKiller
		default:
			sc[i] = quickScore(s, m)*1024 + hist[m]
		}
	}
}

// pickBest moves the highest-scored remaining move to position i.
func pickBest(ml *game.MoveList, sc *[game.MaxMoves]int32, i int) {
	best := i
	for j := i + 1; j < ml.N; j++ {
		if sc[j] > sc[best] {
			best = j
		}
	}
	ml.Moves[i], ml.Moves[best] = ml.Moves[best], ml.Moves[i]
	sc[i], sc[best] = sc[best], sc[i]
}

func (e *Searcher) onCutoff(side uint8, m game.Move, ply, depth int) {
	if k := &e.killers[ply]; k[0] != m {
		k[1], k[0] = k[0], m
	}
	h := &e.history[side]
	h[m] += int32(depth * depth)
	if h[m] > historyMax {
		for i := range h {
			h[i] /= 2
		}
	}
}
