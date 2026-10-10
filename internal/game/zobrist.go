package game

// The hash is additive (sum of keys mod 2^64) rather than XOR, so the
// factories can be hashed as a multiset: two identical factories add their
// key twice instead of cancelling, and permuted factories hash equal.

const maxScoreKey = 512

var zob struct {
	wall      [NumPlayers][25]uint64
	line      [NumPlayers][NumRows][NumColors][NumRows + 1]uint64
	floor     [NumPlayers][FloorSlots + 1]uint64
	score     [NumPlayers][maxScoreKey]uint64
	factory   [625 * 5]uint64 // base-5 index of per-color counts (each 0..4)
	center    [NumColors][TilesPerColor + 1]uint64
	bag       [NumColors][TilesPerColor + 1]uint64
	lid       [NumColors][TilesPerColor + 1]uint64
	nextFirst [NumPlayers]uint64
	token     uint64
	side      uint64
}

func init() {
	x := uint64(0x2545F4914F6CDD1D)
	next := func() uint64 { // splitmix64
		x += 0x9E3779B97F4A7C15
		z := x
		z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
		z = (z ^ z>>27) * 0x94D049BB133111EB
		return z ^ z>>31
	}
	fill := func(keys []uint64) {
		for i := range keys {
			keys[i] = next()
		}
	}
	for p := 0; p < NumPlayers; p++ {
		fill(zob.wall[p][:])
		for r := 0; r < NumRows; r++ {
			for c := 0; c < NumColors; c++ {
				fill(zob.line[p][r][c][:])
			}
		}
		fill(zob.floor[p][:])
		fill(zob.score[p][:])
	}
	fill(zob.factory[:])
	for c := 0; c < NumColors; c++ {
		fill(zob.center[c][:])
		fill(zob.bag[c][:])
		fill(zob.lid[c][:])
	}
	fill(zob.nextFirst[:])
	zob.token = next()
	zob.side = next()
}

func factoryIndex(f *[NumColors]uint8) int {
	return int(f[0]) + 5*int(f[1]) + 25*int(f[2]) + 125*int(f[3]) + 625*int(f[4])
}

func lineKey(p, r int, l Line) uint64 {
	if l.Count == 0 {
		return 0
	}
	return zob.line[p][r][l.Color][l.Count]
}

func scoreKey(p int, score int16) uint64 {
	return zob.score[p][min(max(int(score), 0), maxScoreKey-1)]
}

// ComputeHash recomputes the hash from scratch.
func (s *State) ComputeHash() uint64 {
	var h uint64
	for p := range s.Players {
		pb := &s.Players[p]
		for i := 0; i < 25; i++ {
			if pb.Wall.Rows>>i&1 != 0 {
				h += zob.wall[p][i]
			}
		}
		for r, l := range pb.Lines {
			h += lineKey(p, r, l)
		}
		h += zob.floor[p][pb.Floor]
		h += scoreKey(p, pb.Score)
	}
	for f := range s.Factories {
		h += zob.factory[factoryIndex(&s.Factories[f])]
	}
	for c := 0; c < NumColors; c++ {
		h += zob.center[c][s.Center[c]] + zob.bag[c][s.Bag[c]] + zob.lid[c][s.Lid[c]]
	}
	h += zob.nextFirst[s.NextFirst]
	if s.TokenInCenter {
		h += zob.token
	}
	if s.ToMove == 1 {
		h += zob.side
	}
	return h
}
