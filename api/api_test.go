package api

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tusk80/azul-engine/game"
	"github.com/tusk80/azul-engine/search"
)

func midround(t *testing.T) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("../testdata/midround.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOptionsCapsTime(t *testing.T) {
	tests := []struct {
		req  BestMoveRequest
		want time.Duration
		pv   int
	}{
		{BestMoveRequest{}, time.Second, 3},
		{BestMoveRequest{TimeMs: 500}, 500 * time.Millisecond, 3},
		{BestMoveRequest{TimeMs: 60000, MultiPV: 99}, 2 * time.Second, 8},
	}
	for _, tt := range tests {
		opt := tt.req.Options(time.Second, 2*time.Second)
		if opt.MoveTime != tt.want || opt.MultiPV != tt.pv {
			t.Errorf("%+v: time %v multipv %d, want %v and %d", tt.req, opt.MoveTime, opt.MultiPV, tt.want, tt.pv)
		}
	}
}

func TestBestMove(t *testing.T) {
	st, err := ParseState(midround(t))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := BestMove(search.New(16), &st, search.Options{MoveTime: 200 * time.Millisecond, MultiPV: 3})
	if err != nil {
		t.Fatal(err)
	}
	m, err := game.ParseMove(resp.Move.Text)
	if err != nil || !st.Legal(m) {
		t.Fatalf("move %q is not legal", resp.Move.Text)
	}
	if len(resp.Lines) != 3 || resp.PV[0] != resp.Move.Text || resp.Reason == "" {
		t.Fatalf("incomplete response: %+v", resp)
	}
}

func TestErrorsCarryAStatus(t *testing.T) {
	status := func(err error) int {
		var e *Error
		if !errors.As(err, &e) {
			t.Fatalf("not an api.Error: %v", err)
		}
		return e.Status
	}
	if _, err := ParseState(nil); status(err) != 400 {
		t.Errorf("missing state: %v", err)
	}
	if _, err := ParseState([]byte(`{"factories":[["purple"]]}`)); status(err) != 400 {
		t.Errorf("bad state: %v", err)
	}
	for _, moves := range [][]string{{"F1 red->1"}, {"score"}, {"deal"}, {"nonsense"}} {
		if _, err := Apply(ApplyRequest{State: midround(t), Moves: moves}); status(err) != 400 {
			t.Errorf("%v: %v", moves, err)
		}
	}
	if _, err := NewGame(NewRequest{First: 2}); status(err) != 400 {
		t.Errorf("bad first player: %v", err)
	}

	// A finished round has no moves to search.
	pos, err := NewGame(NewRequest{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	st := pos.State
	var ml game.MoveList
	for !st.RoundOver() {
		st.GenMoves(&ml)
		st.Apply(ml.Moves[0])
	}
	if _, err := BestMove(search.New(1), &st, search.Options{MaxDepth: 1}); status(err) != 422 {
		t.Errorf("round over: %v", err)
	}
}

func TestNewGameIsReproducible(t *testing.T) {
	a, _ := NewGame(NewRequest{Seed: 9})
	b, _ := NewGame(NewRequest{Seed: 9})
	c, _ := NewGame(NewRequest{Seed: 10})
	if a.State != b.State || a.State == c.State {
		t.Fatal("the same seed should give the same deal, and another seed a different one")
	}
}

func TestDispatch(t *testing.T) {
	eng := search.New(16)
	lim := Limits{MoveTime: 100 * time.Millisecond, MaxMoveTime: 200 * time.Millisecond}
	call := func(path, body string) (int, map[string]any) {
		t.Helper()
		status, out := Dispatch(eng, lim, path, []byte(body))
		var v map[string]any
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatalf("%s: response is not JSON: %s", path, out)
		}
		return status, v
	}

	if status, v := call("/config", ""); status != 200 || v["maxTimeMs"] != float64(200) {
		t.Fatalf("/config: %d %v", status, v)
	}
	status, v := call("/new", `{"seed":3}`)
	if status != 200 || v["state"] == nil {
		t.Fatalf("/new: %d %v", status, v)
	}
	state, _ := json.Marshal(v["state"])

	start := time.Now()
	status, v = call("/bestmove", `{"state":`+string(state)+`,"timeMs":60000}`)
	if status != 200 || v["move"] == nil || v["reason"] == "" {
		t.Fatalf("/bestmove: %d %v", status, v)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("a 60 s request ran %v despite a 200 ms cap", el)
	}
	move := v["move"].(map[string]any)["text"].(string)

	if status, v = call("/apply", `{"state":`+string(state)+`,"moves":["`+move+`"]}`); status != 200 || len(v["positions"].([]any)) != 2 {
		t.Fatalf("/apply: %d %v", status, v)
	}
	for path, want := range map[string]int{"/nope": 404, "/apply": 400, "/bestmove": 400} {
		if status, v := call(path, `{}`); status != want || v["error"] == nil {
			t.Errorf("%s with no state: %d %v, want %d", path, status, v, want)
		}
	}
	if status, _ := call("/apply", `not json`); status != 400 {
		t.Errorf("bad JSON: status %d, want 400", status)
	}
}
