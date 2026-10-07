package server

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/tusk80/azul-engine/game"
)

// positionJSON is one position on a line of play, with the step that led
// to it.
type positionJSON struct {
	Move      *moveJSON  `json:"move,omitempty"`   // the move that led here
	Action    string     `json:"action,omitempty"` // or "score" / "deal"
	State     game.State `json:"state"`
	RoundOver bool       `json:"roundOver"`
	GameOver  bool       `json:"gameOver"`
	Winner    *int       `json:"winner,omitempty"` // 0 or 1; -1 = shared; only when the game is over
}

func position(st game.State, m *moveJSON, action string) positionJSON {
	p := positionJSON{Move: m, Action: action, State: st, RoundOver: st.RoundOver(), GameOver: st.GameOver}
	if st.GameOver {
		w := st.Winner()
		p.Winner = &w
	}
	return p
}

type applyRequest struct {
	State json.RawMessage `json:"state"`
	// Moves are played in order. Besides moves ("F2 red->3", "C blue->floor")
	// two actions are accepted at the end of a round: "score" tiles the
	// walls and scores the round, "deal" refills the factories.
	Moves []string `json:"moves"`
	Seed  uint64   `json:"seed"` // for "deal"; 0 = random
}

type applyResponse struct {
	Positions []positionJSON `json:"positions"` // the start position, then one per step
}

// handleApply validates a position (with no moves) or plays a line from it.
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var req applyRequest
	if !decode(w, r, &req) {
		return
	}
	st, err := parseState(req.State)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp := applyResponse{Positions: []positionJSON{position(st, nil, "")}}
	for i, step := range req.Moves {
		switch step {
		case "score":
			if !st.RoundOver() || st.GameOver || !needsScoring(&st) {
				writeError(w, http.StatusBadRequest, fmt.Errorf("step %d: there is no finished round to score", i+1))
				return
			}
			st.ResolveRound()
			resp.Positions = append(resp.Positions, position(st, nil, "score"))
		case "deal":
			if !st.RoundOver() || st.GameOver || needsScoring(&st) {
				writeError(w, http.StatusBadRequest, fmt.Errorf("step %d: score the round before dealing", i+1))
				return
			}
			seed := req.Seed
			if seed == 0 {
				seed = uint64(time.Now().UnixNano())
			}
			st.Refill(rand.New(rand.NewPCG(seed, uint64(i))))
			resp.Positions = append(resp.Positions, position(st, nil, "deal"))
		default:
			m, err := game.ParseMove(step)
			if err != nil || !st.Legal(m) {
				writeError(w, http.StatusBadRequest, fmt.Errorf("step %d: illegal move %q", i+1, step))
				return
			}
			mj := toMoveJSON(&st, m)
			st.Apply(m)
			resp.Positions = append(resp.Positions, position(st, &mj, ""))
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// needsScoring reports whether a finished round has not been scored yet:
// scoring clears the floors and full lines and puts the token back.
func needsScoring(st *game.State) bool {
	if !st.TokenInCenter {
		return true
	}
	for p := range st.Players {
		pb := &st.Players[p]
		if pb.Floor > 0 {
			return true
		}
		for r, l := range pb.Lines {
			if int(l.Count) == r+1 {
				return true
			}
		}
	}
	return false
}

type newRequest struct {
	Seed  uint64 `json:"seed"`  // 0 = random
	First int    `json:"first"` // starting player
}

// handleNew deals a fresh game.
func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	var req newRequest
	if !decode(w, r, &req) {
		return
	}
	if req.First != 0 && req.First != 1 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("first must be 0 or 1"))
		return
	}
	seed := req.Seed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	st := game.NewGame(req.First)
	st.Refill(rand.New(rand.NewPCG(seed, 0)))
	writeJSON(w, http.StatusOK, position(st, nil, "deal"))
}
