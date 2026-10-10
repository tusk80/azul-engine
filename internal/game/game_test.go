package game

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"testing"
)

// scenarioStart deals a fixed first round:
//
//	F1: B B R W   F2: Y Y Y K   F3: R R R R   F4: B Y K W   F5: K K W W
func scenarioStart(tb testing.TB) State {
	tb.Helper()
	s := NewGame(0)
	err := s.RefillFrom([NumFactories][NumColors]uint8{
		{2, 0, 1, 0, 1},
		{0, 3, 0, 1, 0},
		{0, 0, 4, 0, 0},
		{1, 1, 0, 1, 1},
		{0, 0, 0, 2, 2},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

func play(tb testing.TB, s *State, moves ...string) {
	tb.Helper()
	for _, str := range moves {
		m, err := ParseMove(str)
		if err != nil {
			tb.Fatal(err)
		}
		if !s.Legal(m) {
			tb.Fatalf("illegal move %v\n%v", m, s)
		}
		s.Apply(m)
		if s.Hash != s.ComputeHash() {
			tb.Fatalf("hash drift after %v", m)
		}
	}
}

func TestMoveRoundTrip(t *testing.T) {
	for src := 0; src <= SrcCenter; src++ {
		for c := Color(0); c < NumColors; c++ {
			for dst := 0; dst <= DstFloor; dst++ {
				m := NewMove(src, c, dst)
				if m.Src() != src || m.Color() != c || m.Dst() != dst {
					t.Fatalf("pack/unpack mismatch for %d %v %d", src, c, dst)
				}
				p, err := ParseMove(m.String())
				if err != nil || p != m {
					t.Fatalf("ParseMove(%q) = %v, %v", m.String(), p, err)
				}
			}
		}
	}
}

func TestGenMovesOpening(t *testing.T) {
	s := scenarioStart(t)
	var ml MoveList
	s.GenMoves(&ml)
	// 12 distinct (factory, color) pairs × 6 targets on empty boards.
	if ml.N != 72 {
		t.Fatalf("got %d moves, want 72", ml.N)
	}
}

func TestGenMovesDedupesIdenticalFactories(t *testing.T) {
	s := NewGame(0)
	if err := s.RefillFrom([NumFactories][NumColors]uint8{
		{2, 2, 0, 0, 0},
		{2, 2, 0, 0, 0},
		{0, 0, 4, 0, 0},
		{0, 0, 0, 4, 0},
		{0, 0, 0, 0, 4},
	}); err != nil {
		t.Fatal(err)
	}
	var ml MoveList
	s.GenMoves(&ml)
	// F1 (2 colors) + F3 + F4 + F5 = 5 pairs × 6 targets; F2 is skipped.
	if ml.N != 30 {
		t.Fatalf("got %d moves, want 30", ml.N)
	}
	for _, m := range ml.Slice() {
		if m.Src() == 1 {
			t.Fatalf("generated move from duplicate factory: %v", m)
		}
	}
	if !s.Legal(NewMove(1, Blue, 0)) {
		t.Errorf("Legal should still accept the duplicate factory")
	}
}

func TestGenMovesFiltersTargets(t *testing.T) {
	s := NewGame(0)
	s.Center = [NumColors]uint8{Blue: 2}
	s.TokenInCenter = false
	p := &s.Players[0]
	p.Wall.set(0, WallCol(0, Blue))          // row 1: blue already on wall
	p.Lines[1] = Line{Color: Red, Count: 1}  // row 2: other color
	p.Lines[2] = Line{Color: Blue, Count: 3} // row 3: full
	p.Lines[3] = Line{Color: Blue, Count: 1} // row 4: same color, room left
	var ml MoveList
	s.GenMoves(&ml)
	var got []string
	for _, m := range ml.Slice() {
		got = append(got, m.String())
	}
	want := []string{"C blue->4", "C blue->5", "C blue->floor"}
	if len(got) != len(want) {
		t.Fatalf("moves = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("moves = %v, want %v", got, want)
		}
	}
}

// TestScenarioRound plays a full hand-checked round and resolves it.
func TestScenarioRound(t *testing.T) {
	s := scenarioStart(t)

	play(t, &s, "F1 blue->2")
	if s.Center != [NumColors]uint8{Red: 1, White: 1} {
		t.Fatalf("center after F1 = %v", s.Center)
	}
	play(t, &s, "F2 yellow->3", "C red->1")
	if s.TokenInCenter || s.NextFirst != 0 || s.Players[0].Floor != 1 {
		t.Fatalf("token not taken by P1: %v", s)
	}
	play(t, &s, "F3 red->4", "F4 black->3", "F5 white->2", "C black->3")
	if l := s.Players[0].Lines[2]; l != (Line{Black, 3}) || s.Players[0].Floor != 2 || s.Lid[Black] != 1 {
		t.Fatalf("overflow wrong: line %v floor %d lid %v", l, s.Players[0].Floor, s.Lid)
	}
	play(t, &s, "C white->floor", "C blue->4", "C yellow->1")
	if !s.RoundOver() {
		t.Fatalf("round should be over:\n%v", s)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}

	if s.ResolveRound() {
		t.Fatal("game should not be over")
	}
	// P1: red, blue(2), black(3) all isolated = 3, floor 2 = -2 → 1.
	// P2: yellow, white(2), yellow(3), red(4) all isolated = 4, floor 2 = -2 → 2.
	if s.Players[0].Score != 1 || s.Players[1].Score != 2 {
		t.Fatalf("scores = %d, %d; want 1, 2", s.Players[0].Score, s.Players[1].Score)
	}
	if l := s.Players[0].Lines[3]; l != (Line{Blue, 1}) {
		t.Fatalf("incomplete line should stay, got %v", l)
	}
	if want := [NumColors]uint8{1, 2, 3, 3, 3}; s.Lid != want {
		t.Fatalf("lid = %v, want %v", s.Lid, want)
	}
	if s.Round != 2 || s.ToMove != 0 || !s.TokenInCenter || s.Players[0].Floor != 0 {
		t.Fatalf("next round setup wrong:\n%v", s)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMoveEffect(t *testing.T) {
	s := scenarioStart(t)
	play(t, &s, "F1 blue->2", "F2 yellow->3") // center: R W K
	s.Players[0].Wall = wallOf("xx...")       // blue, yellow in row 1
	s.Hash = s.ComputeHash()

	m, _ := ParseMove("C red->1")
	e := s.MoveEffect(m)
	// Red goes to (0,2) next to two tiles: 3 points. The token costs 1.
	want := Effect{Tiles: 1, Placed: 1, Token: true, CompleteLine: true, WallPoints: 3, FloorDelta: -1}
	if e != want {
		t.Fatalf("effect = %+v, want %+v", e, want)
	}

	play(t, &s, "C red->1", "F3 red->4")
	// P1's line 1 is full (red) and line 2 is full (blue).
	for _, str := range []string{"F5 black->1", "F5 black->2"} {
		if m, _ := ParseMove(str); s.Legal(m) {
			t.Fatalf("%s should be illegal", str)
		}
	}
	m, _ = ParseMove("F5 black->floor")
	e = s.MoveEffect(m)
	// The floor already holds the token: 1 slot (-1) → 3 slots (-4).
	if want := (Effect{Tiles: 2, ToFloor: 2, FloorDelta: -3}); e != want {
		t.Fatalf("effect = %+v, want %+v", e, want)
	}
}

func TestMoveEffectBonus(t *testing.T) {
	s := NewGame(0)
	s.Center[White] = 1
	s.TokenInCenter = false
	s.Players[0].Wall = wallOf("xxxx.")
	e := s.MoveEffect(NewMove(SrcCenter, White, 0))
	if e.WallPoints != 5 || e.Bonus != 2 {
		t.Fatalf("effect = %+v, want 5 points and a +2 row bonus", e)
	}
}

func TestTilingIsTopRowFirst(t *testing.T) {
	s := NewGame(0)
	p := &s.Players[0]
	// All three land in column 1: blue, white, black.
	p.Lines[0] = Line{Blue, 1}
	p.Lines[1] = Line{White, 2}
	p.Lines[2] = Line{Black, 3}
	s.ResolveRound()
	if p.Score != 1+2+3 {
		t.Fatalf("score = %d, want 6", p.Score)
	}
	if s.Lid[White] != 1 || s.Lid[Black] != 2 {
		t.Fatalf("leftover tiles not in lid: %v", s.Lid)
	}
}

func TestScoreClamp(t *testing.T) {
	tests := []struct {
		name  string
		score int16
		floor uint8
		wall  []string
		line0 Line
		want  int16
	}{
		{"penalty clamps at zero", 2, 3, nil, Line{}, 0},
		{"full floor clamps", 10, 7, nil, Line{}, 0},
		{"ordinary penalty", 10, 2, nil, Line{}, 8},
		// Placement (+3) is added before the penalty (-2); clamping only at the end.
		{"placement before penalty", 0, 2, []string{"xx..."}, Line{Red, 1}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewGame(0)
			p := &s.Players[0]
			p.Score, p.Floor, p.Wall, p.Lines[0] = tt.score, tt.floor, wallOf(tt.wall...), tt.line0
			s.ResolveRound()
			if p.Score != tt.want {
				t.Errorf("score = %d, want %d", p.Score, tt.want)
			}
		})
	}
}

func TestGameEndAndTiebreak(t *testing.T) {
	s := NewGame(0)
	a, b := &s.Players[0], &s.Players[1]
	a.Score, a.Wall, a.Lines[0] = 10, wallOf("xxxx."), Line{White, 1}
	b.Score = 17
	if !s.ResolveRound() || !s.GameOver {
		t.Fatal("completing a row should end the game")
	}
	// 10 + 5 (row run) + 2 (row bonus) = 17; tie broken by complete rows.
	if a.Score != 17 {
		t.Fatalf("score = %d, want 17", a.Score)
	}
	if w := s.Winner(); w != 0 {
		t.Fatalf("winner = %d, want 0", w)
	}
	b.Wall = wallOf("xxxxx")
	if w := s.Winner(); w != -1 {
		t.Fatalf("winner = %d, want shared (-1)", w)
	}
}

func TestNobodyTakesToken(t *testing.T) {
	s := NewGame(1)
	if err := s.RefillFrom([NumFactories][NumColors]uint8{
		{4, 0, 0, 0, 0}, {0, 4, 0, 0, 0}, {0, 0, 4, 0, 0}, {0, 0, 0, 4, 0}, {0, 0, 0, 0, 4},
	}); err != nil {
		t.Fatal(err)
	}
	play(t, &s, "F1 blue->4", "F2 yellow->4", "F3 red->5", "F4 black->5", "F5 white->floor")
	if !s.RoundOver() || !s.TokenInCenter {
		t.Fatal("round should end with the token untouched")
	}
	s.ResolveRound()
	if s.ToMove != 1 {
		t.Fatalf("P2 started and nobody took the token; P%d starts instead", s.ToMove+1)
	}
}

func TestRefillFromUsesLidWhenBagShort(t *testing.T) {
	s := NewGame(0)
	s.Bag = [NumColors]uint8{Red: 2}
	s.Lid = [NumColors]uint8{Red: 3, Blue: 10}
	deal := [NumFactories][NumColors]uint8{{Red: 3, Blue: 1}, {Blue: 4}, {Blue: 4}}
	if err := s.RefillFrom(deal); err == nil {
		t.Fatal("short deal while tiles remain should fail")
	}
	deal[3] = [NumColors]uint8{Red: 2}
	// 14 tiles dealt, but one blue is still left in the lid.
	if err := s.RefillFrom(deal); err == nil {
		t.Fatal("short deal while a blue remains should fail")
	}
	deal[3] = [NumColors]uint8{Red: 2, Blue: 1}
	if err := s.RefillFrom(deal); err != nil {
		t.Fatal(err)
	}
	if s.Bag != ([NumColors]uint8{}) || s.Lid != ([NumColors]uint8{}) {
		t.Fatalf("bag %v lid %v should be empty", s.Bag, s.Lid)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	s := scenarioStart(t)
	play(t, &s, "F1 blue->2", "F2 yellow->3", "C red->1", "F3 red->4")
	s.Players[0].Wall.set(4, 2)
	s.Bag[WallColor(4, 2)]--
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Hash = s.ComputeHash()

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if got != s {
		t.Fatalf("round trip mismatch:\n%v\n%v", s, got)
	}
}

func TestJSONTestdataMatchesScenario(t *testing.T) {
	data, err := os.ReadFile("../../testdata/opening.json")
	if err != nil {
		t.Fatal(err)
	}
	var got State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if want := scenarioStart(t); got != want {
		t.Fatalf("testdata/opening.json differs from scenarioStart:\n%v\n%v", got, want)
	}
}

func TestJSONInfersBag(t *testing.T) {
	in := `{"toMove":0,"nextFirst":0,"tokenInCenter":true,
	  "factories":[["blue","blue","red","white"]],"center":[],
	  "players":[{"score":3,"wall":["B....",".....",".....",".....","....."],
	              "lines":[{},{"color":"red","count":1},{},{},{}],"floor":0},
	             {"score":0,"wall":[".....",".....",".....",".....","....."],
	              "lines":[{},{},{},{},{}],"floor":0}]}`
	var s State
	if err := json.Unmarshal([]byte(in), &s); err != nil {
		t.Fatal(err)
	}
	if want := [NumColors]uint8{17, 20, 18, 20, 19}; s.Bag != want {
		t.Fatalf("bag = %v, want %v", s.Bag, want)
	}
}

func TestJSONRejectsBadInput(t *testing.T) {
	base := func(wall0, line0 string) string {
		return `{"factories":[],"center":[],"players":[{"score":0,"wall":["` + wall0 +
			`",".....",".....",".....","....."],"lines":[` + line0 + `,{},{},{},{}],"floor":0},` +
			`{"score":0,"wall":[".....",".....",".....",".....","....."],"lines":[{},{},{},{},{}],"floor":0}]}`
	}
	tests := map[string]string{
		"wrong color on wall": base("Y....", "{}"),
		"line too long":       base(".....", `{"color":"red","count":2}`),
		"line color on wall":  base("x....", `{"color":"blue","count":1}`),
	}
	for name, in := range tests {
		var s State
		if err := json.Unmarshal([]byte(in), &s); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// TestRandomGames plays random games and checks invariants after every step.
func TestRandomGames(t *testing.T) {
	games := 300
	if testing.Short() {
		games = 30
	}
	for seed := range uint64(games) {
		rng := rand.New(rand.NewPCG(seed, 0))
		s := NewGame(int(seed % 2))
		s.Refill(rng)
		var ml MoveList
		for !s.GameOver {
			if s.Round > 60 {
				t.Fatalf("seed %d: game did not end", seed)
			}
			if s.RoundOver() {
				if !s.ResolveRound() {
					s.Refill(rng)
				}
			} else {
				s.GenMoves(&ml)
				if ml.N == 0 {
					t.Fatalf("seed %d: no moves mid-round\n%v", seed, s)
				}
				s.Apply(ml.Moves[rng.IntN(ml.N)])
			}
			if h := s.ComputeHash(); h != s.Hash {
				t.Fatalf("seed %d: hash drift\n%v", seed, s)
			}
			if err := s.Validate(); err != nil {
				t.Fatalf("seed %d: %v\n%v", seed, err, s)
			}
		}
		_ = s.String()
	}
}

func TestHashIgnoresFactoryOrder(t *testing.T) {
	a := scenarioStart(t)
	b := a
	b.Factories[0], b.Factories[3] = b.Factories[3], b.Factories[0]
	if a.ComputeHash() != b.ComputeHash() {
		t.Fatal("permuted factories should hash equal")
	}
	b.ToMove = 1
	if a.ComputeHash() == b.ComputeHash() {
		t.Fatal("side to move should change the hash")
	}
}

func TestPerft(t *testing.T) {
	s := scenarioStart(t)
	// Depth 1 is hand-checked (see TestGenMovesOpening); deeper values are
	// regression values frozen from this implementation.
	want := []uint64{1, 72, 4752, 239196, 10206755}
	for d, w := range want {
		if got := Perft(&s, d); got != w {
			t.Errorf("perft(%d) = %d, want %d", d, got, w)
		}
	}
}

func BenchmarkGenMoves(b *testing.B) {
	s := scenarioStart(b)
	var ml MoveList
	for b.Loop() {
		s.GenMoves(&ml)
	}
}

func BenchmarkApply(b *testing.B) {
	s := scenarioStart(b)
	m, _ := ParseMove("F1 blue->2")
	for b.Loop() {
		c := s
		c.Apply(m)
	}
}

func BenchmarkResolveRound(b *testing.B) {
	s := NewGame(0)
	p := &s.Players[0]
	p.Wall = wallOf("xx...", ".x...", "..x..")
	p.Lines[0] = Line{Red, 1}
	p.Lines[3] = Line{Black, 4}
	p.Floor = 3
	for b.Loop() {
		c := s
		c.ResolveRound()
	}
}

func BenchmarkRandomRound(b *testing.B) {
	start := scenarioStart(b)
	rng := rand.New(rand.NewPCG(1, 2))
	var ml MoveList
	for b.Loop() {
		s := start
		for !s.RoundOver() {
			s.GenMoves(&ml)
			s.Apply(ml.Moves[rng.IntN(ml.N)])
		}
		s.ResolveRound()
	}
}

func BenchmarkPerft3(b *testing.B) {
	s := scenarioStart(b)
	for b.Loop() {
		Perft(&s, 3)
	}
}
