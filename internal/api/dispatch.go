package api

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/tusk80/azul-engine/internal/search"
)

// Limits bound the search time a caller may ask for.
type Limits struct {
	MoveTime    time.Duration // when the request names none
	MaxMoveTime time.Duration
}

// Dispatch answers one request by path, the way the HTTP server would:
// "/bestmove", "/apply", "/new" and "/config", with a JSON body in and a
// status and JSON body out. It lets a host with no HTTP stack (the
// WebAssembly build) serve the same API.
func Dispatch(eng *search.Searcher, lim Limits, path string, body []byte) (status int, out []byte) {
	v, err := dispatch(eng, lim, path, body)
	if err != nil {
		status = 500
		var ae *Error
		if errors.As(err, &ae) {
			status = ae.Status
		}
		out, _ = json.Marshal(map[string]string{"error": err.Error()})
		return status, out
	}
	out, err = json.Marshal(v)
	if err != nil {
		return 500, []byte(`{"error":"encoding the response failed"}`)
	}
	return 200, out
}

func dispatch(eng *search.Searcher, lim Limits, path string, body []byte) (any, error) {
	decode := func(v any) error {
		if len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, v); err != nil {
			return badRequest("request: %v", err)
		}
		return nil
	}
	switch path {
	case "/config":
		return Config{
			DefaultTimeMs: lim.MoveTime.Milliseconds(),
			MaxTimeMs:     lim.MaxMoveTime.Milliseconds(),
			Version:       Version(),
		}, nil
	case "/bestmove":
		var req BestMoveRequest
		if err := decode(&req); err != nil {
			return nil, err
		}
		st, err := ParseState(req.State)
		if err != nil {
			return nil, err
		}
		return BestMove(eng, &st, req.Options(lim.MoveTime, lim.MaxMoveTime))
	case "/apply":
		var req ApplyRequest
		if err := decode(&req); err != nil {
			return nil, err
		}
		return Apply(req)
	case "/new":
		var req NewRequest
		if err := decode(&req); err != nil {
			return nil, err
		}
		return NewGame(req)
	}
	return nil, &Error{Status: 404, Msg: "unknown path " + path}
}
