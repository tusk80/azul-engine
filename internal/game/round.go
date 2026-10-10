package game

import (
	"fmt"
	"math/rand/v2"
)

// ResolveRound runs the deterministic end of a round: tiling (top row
// first, each placement scored immediately), floor penalties, clamping at 0,
// and, if any wall row is complete, end-of-game bonuses. Otherwise it sets
// up the next round's turn and token; the factories stay empty until Refill.
func (s *State) ResolveRound() (gameOver bool) {
	gameOver = s.ProjectRound()
	if !gameOver {
		s.Round++
		s.ToMove = s.NextFirst
		s.TokenInCenter = true
	}
	s.Hash = s.ComputeHash()
	return gameOver
}

// ProjectRound does the scoring half of ResolveRound — tiling, floor
// penalties, clamping and end-game bonuses — without updating the hash or
// the turn. It is meant for evaluating a copy, including mid-round
// ("if the round ended now").
func (s *State) ProjectRound() (gameOver bool) {
	for p := range s.Players {
		pb := &s.Players[p]
		score := int(pb.Score)
		for r := 0; r < NumRows; r++ {
			l := pb.Lines[r]
			if int(l.Count) != r+1 {
				continue
			}
			score += pb.Wall.Place(r, WallCol(r, l.Color))
			s.Lid[l.Color] += uint8(r)
			pb.Lines[r] = Line{}
		}
		score += FloorPenalty(pb.Floor)
		pb.Floor = 0
		pb.Score = int16(max(score, 0))
	}

	for p := range s.Players {
		if s.Players[p].Wall.CompleteRows() > 0 {
			gameOver = true
		}
	}
	if gameOver {
		for p := range s.Players {
			s.Players[p].Score += int16(s.Players[p].Wall.Bonus())
		}
		s.GameOver = true
	}
	return gameOver
}

// Refill deals the factories from the bag, pouring the lid into the bag when
// it runs dry. If both run out, the remaining factories stay short or empty.
func (s *State) Refill(rng *rand.Rand) {
	defer func() { s.Hash = s.ComputeHash() }()
	for f := 0; f < NumFactories; f++ {
		for t := 0; t < FactorySize; t++ {
			c, ok := s.draw(rng)
			if !ok {
				return
			}
			s.Factories[f][c]++
		}
	}
}

func (s *State) draw(rng *rand.Rand) (Color, bool) {
	total := sumColors(&s.Bag)
	if total == 0 {
		s.Bag, s.Lid = s.Lid, [NumColors]uint8{}
		if total = sumColors(&s.Bag); total == 0 {
			return 0, false
		}
	}
	x := rng.IntN(total)
	for c := range s.Bag {
		if x < int(s.Bag[c]) {
			s.Bag[c]--
			return Color(c), true
		}
		x -= int(s.Bag[c])
	}
	panic("unreachable")
}

// RefillFrom deals the given factory contents, for tests, replays and
// expectimax. If the deal needs more tiles than the bag holds, the bag must
// be drawn out completely and the rest comes from the lid, as in a real deal.
func (s *State) RefillFrom(deal [NumFactories][NumColors]uint8) error {
	var d [NumColors]int
	total := 0
	for f := range deal {
		n := 0
		for c, k := range deal[f] {
			d[c] += int(k)
			n += int(k)
		}
		if n > FactorySize {
			return fmt.Errorf("factory %d: %d tiles", f+1, n)
		}
		total += n
	}

	bag, lid := s.Bag, s.Lid
	if total <= sumColors(&bag) {
		for c := range bag {
			if d[c] > int(bag[c]) {
				return fmt.Errorf("bag has %d %v, deal needs %d", bag[c], Color(c), d[c])
			}
			bag[c] -= uint8(d[c])
		}
	} else {
		for c := range bag {
			rest := d[c] - int(bag[c])
			if rest < 0 || rest > int(lid[c]) {
				return fmt.Errorf("deal of %d %v is inconsistent with bag %d + lid %d", d[c], Color(c), bag[c], lid[c])
			}
			bag[c] = lid[c] - uint8(rest)
		}
		lid = [NumColors]uint8{}
	}
	if total < NumFactories*FactorySize && sumColors(&bag)+sumColors(&lid) > 0 {
		return fmt.Errorf("short deal of %d tiles while tiles remain", total)
	}

	s.Bag, s.Lid = bag, lid
	s.Factories = deal
	s.Hash = s.ComputeHash()
	return nil
}

// Winner returns the winning player, or -1 for a shared victory. Ties on
// score are broken by complete rows. Meaningful only when GameOver.
func (s *State) Winner() int {
	a, b := &s.Players[0], &s.Players[1]
	if a.Score != b.Score {
		if a.Score > b.Score {
			return 0
		}
		return 1
	}
	ra, rb := a.Wall.CompleteRows(), b.Wall.CompleteRows()
	switch {
	case ra > rb:
		return 0
	case rb > ra:
		return 1
	}
	return -1
}

func sumColors(a *[NumColors]uint8) int {
	return int(a[0]) + int(a[1]) + int(a[2]) + int(a[3]) + int(a[4])
}
