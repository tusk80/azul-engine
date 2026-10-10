//go:build js && wasm

// Command wasm is the engine compiled for the browser. It exposes one
// function, azulCall(path, jsonBody) -> {status, body}, which answers the
// same requests as the HTTP server. The page runs it in a Web Worker, so a
// static host with no backend can serve the whole analysis board.
//
//	GOOS=js GOARCH=wasm go build -o engine.wasm ./cmd/wasm
package main

import (
	"syscall/js"
	"time"

	"github.com/tusk80/azul-engine/internal/api"
	"github.com/tusk80/azul-engine/internal/search"
)

func main() {
	// The visitor's own machine does the work, so the only limits are
	// patience and memory.
	eng := search.New(32)
	lim := api.Limits{MoveTime: time.Second, MaxMoveTime: 10 * time.Second}

	js.Global().Set("azulCall", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 2 {
			return map[string]any{"status": 400, "body": `{"error":"azulCall(path, body)"}`}
		}
		status, out := api.Dispatch(eng, lim, args[0].String(), []byte(args[1].String()))
		return map[string]any{"status": status, "body": string(out)}
	}))
	select {} // keep the program alive for calls
}
