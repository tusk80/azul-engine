package game

import (
	"encoding/json"
	"fmt"
)

// JSON format. Walls are 5 strings, one per row: '.' is empty, a color
// letter (B Y R K W) or 'x' is filled. Bag and lid are optional; if either
// is missing it is inferred from tile conservation (unknown split → all in
// the bag).
type jsonState struct {
	Round         int                    `json:"round"`
	ToMove        int                    `json:"toMove"`
	NextFirst     int                    `json:"nextFirst"`
	TokenInCenter bool                   `json:"tokenInCenter"`
	GameOver      bool                   `json:"gameOver,omitempty"`
	Factories     [][]string             `json:"factories"`
	Center        []string               `json:"center"`
	Bag           map[string]int         `json:"bag,omitempty"`
	Lid           map[string]int         `json:"lid,omitempty"`
	Players       [NumPlayers]jsonPlayer `json:"players"`
}

type jsonPlayer struct {
	Score int               `json:"score"`
	Wall  [NumRows]string   `json:"wall"`
	Lines [NumRows]jsonLine `json:"lines"`
	Floor int               `json:"floor"` // occupied slots, including the token
}

type jsonLine struct {
	Color string `json:"color,omitempty"`
	Count int    `json:"count"`
}

func (s State) MarshalJSON() ([]byte, error) {
	js := jsonState{
		Round:         int(s.Round),
		ToMove:        int(s.ToMove),
		NextFirst:     int(s.NextFirst),
		TokenInCenter: s.TokenInCenter,
		GameOver:      s.GameOver,
		Factories:     make([][]string, NumFactories),
		Center:        tileList(&s.Center),
		Bag:           colorMap(&s.Bag),
		Lid:           colorMap(&s.Lid),
	}
	for f := range s.Factories {
		js.Factories[f] = tileList(&s.Factories[f])
	}
	for p := range s.Players {
		pb, jp := &s.Players[p], &js.Players[p]
		jp.Score = int(pb.Score)
		jp.Floor = int(pb.Floor)
		for r := 0; r < NumRows; r++ {
			row := []byte(".....")
			for col := 0; col < 5; col++ {
				if pb.Wall.Has(r, col) {
					row[col] = WallColor(r, col).Letter()
				}
			}
			jp.Wall[r] = string(row)
			if l := pb.Lines[r]; l.Count > 0 {
				jp.Lines[r] = jsonLine{Color: l.Color.String(), Count: int(l.Count)}
			}
		}
	}
	return json.Marshal(js)
}

func (s *State) UnmarshalJSON(data []byte) error {
	var js jsonState
	if err := json.Unmarshal(data, &js); err != nil {
		return err
	}
	var ns State
	if js.Round < 0 || js.Round > 255 {
		return fmt.Errorf("round %d out of range", js.Round)
	}
	ns.Round = uint8(max(js.Round, 1))
	if js.ToMove < 0 || js.ToMove >= NumPlayers || js.NextFirst < 0 || js.NextFirst >= NumPlayers {
		return fmt.Errorf("toMove/nextFirst must be 0 or 1")
	}
	ns.ToMove, ns.NextFirst = uint8(js.ToMove), uint8(js.NextFirst)
	ns.TokenInCenter = js.TokenInCenter
	ns.GameOver = js.GameOver

	if len(js.Factories) > NumFactories {
		return fmt.Errorf("%d factories, max %d", len(js.Factories), NumFactories)
	}
	for f, tiles := range js.Factories {
		if len(tiles) > FactorySize {
			return fmt.Errorf("factory %d: %d tiles", f+1, len(tiles))
		}
		if err := addTiles(&ns.Factories[f], tiles); err != nil {
			return fmt.Errorf("factory %d: %w", f+1, err)
		}
	}
	if err := addTiles(&ns.Center, js.Center); err != nil {
		return fmt.Errorf("center: %w", err)
	}

	for p := range js.Players {
		jp, pb := &js.Players[p], &ns.Players[p]
		if jp.Score < 0 || jp.Score >= maxScoreKey {
			return fmt.Errorf("P%d score %d out of range", p+1, jp.Score)
		}
		pb.Score = int16(jp.Score)
		if jp.Floor < 0 || jp.Floor > FloorSlots {
			return fmt.Errorf("P%d floor %d out of range", p+1, jp.Floor)
		}
		pb.Floor = uint8(jp.Floor)
		for r, row := range jp.Wall {
			if err := parseWallRow(&pb.Wall, r, row); err != nil {
				return fmt.Errorf("P%d wall: %w", p+1, err)
			}
		}
		for r, jl := range jp.Lines {
			if jl.Count < 0 || jl.Count > r+1 {
				return fmt.Errorf("P%d line %d: count %d", p+1, r+1, jl.Count)
			}
			if jl.Count == 0 {
				continue
			}
			c, err := ParseColor(jl.Color)
			if err != nil {
				return fmt.Errorf("P%d line %d: %w", p+1, r+1, err)
			}
			pb.Lines[r] = Line{Color: c, Count: uint8(jl.Count)}
		}
	}

	used := ns.tileCounts()
	var rem [NumColors]int
	for c := range rem {
		if rem[c] = TilesPerColor - used[c]; rem[c] < 0 {
			return fmt.Errorf("%v: %d tiles on the table, max %d", Color(c), used[c], TilesPerColor)
		}
	}
	var err error
	switch {
	case js.Bag == nil && js.Lid == nil:
		ns.Bag, err = fromCounts(rem)
	case js.Bag == nil:
		if ns.Lid, err = parseColorMap(js.Lid); err == nil {
			ns.Bag, err = subtract(rem, &ns.Lid)
		}
	case js.Lid == nil:
		if ns.Bag, err = parseColorMap(js.Bag); err == nil {
			ns.Lid, err = subtract(rem, &ns.Bag)
		}
	default:
		if ns.Bag, err = parseColorMap(js.Bag); err == nil {
			ns.Lid, err = parseColorMap(js.Lid)
		}
	}
	if err != nil {
		return err
	}
	if err := ns.Validate(); err != nil {
		return err
	}
	ns.Hash = ns.ComputeHash()
	*s = ns
	return nil
}

func parseWallRow(w *Wall, r int, row string) error {
	if len(row) != 5 {
		return fmt.Errorf("row %d %q: want 5 characters", r+1, row)
	}
	for col := 0; col < 5; col++ {
		ch := row[col]
		switch ch {
		case '.':
			continue
		case 'x', 'X':
		default:
			c, err := ParseColor(string(ch))
			if err != nil {
				return fmt.Errorf("row %d %q: %w", r+1, row, err)
			}
			if want := WallColor(r, col); c != want {
				return fmt.Errorf("row %d column %d holds %v, not %v", r+1, col+1, want, c)
			}
		}
		w.set(r, col)
	}
	return nil
}

func addTiles(dst *[NumColors]uint8, tiles []string) error {
	for _, t := range tiles {
		c, err := ParseColor(t)
		if err != nil {
			return err
		}
		if dst[c] == TilesPerColor {
			return fmt.Errorf("too many %v tiles", c)
		}
		dst[c]++
	}
	return nil
}

func tileList(counts *[NumColors]uint8) []string {
	out := make([]string, 0, FactorySize)
	for c, n := range counts {
		for range n {
			out = append(out, Color(c).String())
		}
	}
	return out
}

func colorMap(counts *[NumColors]uint8) map[string]int {
	m := make(map[string]int, NumColors)
	for c, n := range counts {
		m[Color(c).String()] = int(n)
	}
	return m
}

func parseColorMap(m map[string]int) (out [NumColors]uint8, err error) {
	for k, n := range m {
		c, err := ParseColor(k)
		if err != nil {
			return out, err
		}
		if n < 0 || n > TilesPerColor {
			return out, fmt.Errorf("%v count %d out of range", c, n)
		}
		out[c] = uint8(n)
	}
	return out, nil
}

func fromCounts(n [NumColors]int) (out [NumColors]uint8, err error) {
	for c := range n {
		out[c] = uint8(n[c])
	}
	return out, nil
}

func subtract(rem [NumColors]int, given *[NumColors]uint8) (out [NumColors]uint8, err error) {
	for c := range rem {
		d := rem[c] - int(given[c])
		if d < 0 {
			return out, fmt.Errorf("%v: bag/lid count %d exceeds the %d unaccounted tiles", Color(c), given[c], rem[c])
		}
		out[c] = uint8(d)
	}
	return out, nil
}
