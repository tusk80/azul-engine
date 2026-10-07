// Command azul is the Azul engine CLI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tusk80/azul-engine/eval"
	"github.com/tusk80/azul-engine/game"
	"github.com/tusk80/azul-engine/search"
	"github.com/tusk80/azul-engine/selfplay"
	"github.com/tusk80/azul-engine/server"
)

const usage = `usage:
  azul                               (double-click) run the engine and open the board
  azul serve [-addr 127.0.0.1:8765] [-time 1s] [-weights W] [-open]
                                     engine + analysis board over HTTP
  azul serve -public -addr 127.0.0.1:8080 -ip-header CF-Connecting-IP
                                     the same, with limits for a public site
  azul analyze <state.json|-> [-time 2s] [-depth N] [-multipv 3] [-weights W]
                                     search for the best move
  azul selfplay -a W -b W [-pairs 200] [-nodes 20000]
                                     engine-vs-engine match, A's results
  azul tune [-from W] [-iters 300] [-pairs 16] [-nodes 5000] [-out tuned.json]
                                     tune eval weights with SPSA
  (W = "default", "zero", or a weights JSON file)
  azul show <state.json|->           print the board and legal moves
  azul perft <state.json|-> <depth>  count move sequences (stops at round end)
  azul random [-seed N] [-json]      play a random game, printing each round`

func main() {
	if len(os.Args) < 2 {
		// Double-clicked: run the engine and open the analysis board.
		if err := cmdServe([]string{"-open"}); err != nil {
			fmt.Fprintln(os.Stderr, "azul:", err)
			fmt.Println("\nPress Enter to close.")
			fmt.Scanln()
			os.Exit(1)
		}
		return
	}
	var err error
	switch os.Args[1] {
	case "show":
		err = cmdShow(os.Args[2:])
	case "perft":
		err = cmdPerft(os.Args[2:])
	case "random":
		err = cmdRandom(os.Args[2:])
	case "analyze":
		err = cmdAnalyze(os.Args[2:])
	case "selfplay":
		err = cmdSelfplay(os.Args[2:])
	case "tune":
		err = cmdTune(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "azul:", err)
		os.Exit(1)
	}
}

func loadState(path string) (game.State, error) {
	var s game.State
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return s, err
	}
	// Windows editors and PowerShell often prepend a UTF-8 BOM.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	err = json.Unmarshal(data, &s)
	return s, err
}

func cmdShow(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("show: want one state file")
	}
	s, err := loadState(args[0])
	if err != nil {
		return err
	}
	fmt.Print(s)
	var ml game.MoveList
	s.GenMoves(&ml)
	fmt.Printf("\n%d legal moves:\n", ml.N)
	for i, m := range ml.Slice() {
		fmt.Printf("  %-18v", m)
		if i%4 == 3 {
			fmt.Println()
		}
	}
	fmt.Println()
	return nil
}

func cmdPerft(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("perft: want a state file and a depth")
	}
	s, err := loadState(args[0])
	if err != nil {
		return err
	}
	depth, err := strconv.Atoi(args[1])
	if err != nil {
		return err
	}
	for d := 1; d <= depth; d++ {
		start := time.Now()
		n := game.Perft(&s, d)
		el := time.Since(start)
		fmt.Printf("perft(%d) = %d  %v", d, n, el.Round(time.Millisecond))
		if el >= time.Millisecond {
			fmt.Printf("  %.1fM leaves/s", float64(n)/el.Seconds()/1e6)
		}
		fmt.Println()
	}
	return nil
}

func cmdAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	moveTime := fs.Duration("time", 2*time.Second, "time limit (0 = none)")
	depth := fs.Int("depth", 0, "max depth in plies (0 = to the end of the round)")
	multiPV := fs.Int("multipv", 3, "number of best moves to show")
	ttMB := fs.Int("hash", 256, "transposition table size in MB")
	weights := fs.String("weights", "default", `eval weights: "default", "zero" or a JSON file`)
	look := lookFlags(fs, "")
	var path string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		path, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" {
		path = fs.Arg(0)
	}
	if path == "" {
		return fmt.Errorf("analyze: want a state file")
	}
	s, err := loadState(path)
	if err != nil {
		return err
	}
	w, err := loadWeights(*weights)
	if err != nil {
		return err
	}
	fmt.Print(s)
	fmt.Printf("\nScores are points for P%d (the side to move).\n\n", s.ToMove+1)

	e := search.New(*ttMB)
	e.Weights = w
	e.Lookahead = look()
	res := e.Search(&s, search.Options{
		MoveTime: *moveTime,
		MaxDepth: *depth,
		MultiPV:  *multiPV,
		OnDepth: func(r search.Result) {
			l := r.Lines[0]
			fmt.Printf("depth %2d  %-12s nodes %-10d %6.2fM n/s  %-8v pv %s\n",
				r.Depth, eval.Format(l.Score), r.Nodes, float64(r.Nodes)/max(r.Elapsed.Seconds(), 1e-3)/1e6,
				r.Elapsed.Round(time.Millisecond), formatPV(l.PV))
		},
	})
	if len(res.Lines) == 0 {
		return fmt.Errorf("no legal moves (round over or game over)")
	}
	exact := ""
	if res.Exact {
		exact = ", searched to the end of the round"
	}
	fmt.Printf("\nbest move: %v  (depth %d%s)\n", res.Lines[0].Move, res.Depth, exact)
	for i, l := range res.Lines {
		fmt.Printf("  %d. %-18v %-12s %s\n", i+1, l.Move, eval.Format(l.Score), formatPV(l.PV))
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8765", "listen address")
	moveTime := fs.Duration("time", time.Second, "default search time per request")
	maxTime := fs.Duration("max-time", 30*time.Second, "longest search a request may ask for")
	ttMB := fs.Int("hash", 256, "transposition table size in MB, per engine")
	engines := fs.Int("engines", 1, "searches that may run at once")
	rate := fs.Int("rate", 0, "searches per minute per client address (0 = unlimited)")
	ipHeader := fs.String("ip-header", "", `header your reverse proxy sets to the visitor's address, e.g. "CF-Connecting-IP" or "X-Forwarded-For"`)
	cors := fs.String("cors", "*", `sites allowed to call the API, comma-separated; "*" = any, "" = none`)
	public := fs.Bool("public", false, "preset for a public site: -max-time 2s -hash 32 -rate 40 -cors \"\" and half the CPUs as engines")
	weights := fs.String("weights", "default", `eval weights: "default", "zero" or a JSON file`)
	look := lookFlags(fs, "")
	open := fs.Bool("open", false, "open the analysis board in the browser")
	logReqs := fs.Bool("log", false, "log one line per API request (path, status, time; no addresses)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *public {
		// Flags given explicitly still win over the preset.
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		preset := func(name string, apply func()) {
			if !set[name] {
				apply()
			}
		}
		preset("max-time", func() { *maxTime = 2 * time.Second })
		preset("hash", func() { *ttMB = 32 })
		preset("rate", func() { *rate = 40 })
		preset("cors", func() { *cors = "" })
		preset("engines", func() { *engines = max(runtime.NumCPU()/2, 1) })
		preset("log", func() { *logReqs = true })
	}
	var logger *slog.Logger
	if *logReqs {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}
	w, err := loadWeights(*weights)
	if err != nil {
		return err
	}
	var origins []string
	for o := range strings.SplitSeq(*cors, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	url := "http://" + *addr + "/"
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		// Most likely the engine is already running: just show the board.
		if *open && engineRunning(url) {
			fmt.Println("The Azul engine is already running.")
			return openBrowser(url)
		}
		return err
	}
	srv := &http.Server{
		Handler: server.New(server.Config{
			Weights:           &w,
			Lookahead:         look(),
			Engines:           *engines,
			HashMB:            *ttMB,
			MoveTime:          *moveTime,
			MaxMoveTime:       *maxTime,
			SearchesPerMinute: *rate,
			ClientIPHeader:    *ipHeader,
			CORSOrigins:       origins,
			Log:               logger,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      *maxTime + 20*time.Second, // search, plus waiting for a free engine
		IdleTimeout:       90 * time.Second,
	}
	fmt.Printf("Azul engine running at %s\n", url)
	if *open {
		fmt.Println("Keep this window open while you play. Close it to stop the engine.")
		if err := openBrowser(url); err != nil {
			fmt.Println("Open", url, "in your browser.")
		}
	}

	// Finish requests in flight on Ctrl+C or a service stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), *maxTime+5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
func engineRunning(url string) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(url + "health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}

// lookFlags registers -<prefix>look and -<prefix>lookdepth and returns a
// getter for the parsed settings.
func lookFlags(fs *flag.FlagSet, prefix string) func() search.Lookahead {
	n := fs.Int(prefix+"look", 0, "round-end lookahead: next-round deals sampled (0 = off)")
	d := fs.Int(prefix+"lookdepth", 1, "round-end lookahead: plies searched per deal")
	return func() search.Lookahead { return search.Lookahead{Samples: *n, Depth: *d} }
}

func loadWeights(name string) (eval.Weights, error) {
	switch name {
	case "default":
		return eval.Default, nil
	case "zero":
		return eval.Weights{}, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return eval.Weights{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.DisallowUnknownFields()
	w := eval.Default // fields missing from the file keep their default
	if err := dec.Decode(&w); err != nil {
		return w, fmt.Errorf("%s: %w", name, err)
	}
	return w, nil
}

func cmdSelfplay(args []string) error {
	fs := flag.NewFlagSet("selfplay", flag.ContinueOnError)
	aName := fs.String("a", "default", "weights for player A")
	bName := fs.String("b", "zero", "weights for player B")
	aLook := lookFlags(fs, "a-")
	bLook := lookFlags(fs, "b-")
	pairs := fs.Int("pairs", 200, "game pairs (each deal played with seats swapped)")
	nodes := fs.Uint64("nodes", 20000, "node budget per move")
	depth := fs.Int("depth", 0, "depth limit per move (0 = none)")
	moveTime := fs.Duration("time", 0, "time limit per move, e.g. 50ms (0 = none; not reproducible)")
	workers := fs.Int("workers", 0, "parallel games (0 = all CPUs)")
	seed := fs.Uint64("seed", 1, "first deal seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	wa, err := loadWeights(*aName)
	if err != nil {
		return err
	}
	wb, err := loadWeights(*bName)
	if err != nil {
		return err
	}
	a := selfplay.Player{Weights: wa, Lookahead: aLook(), Nodes: *nodes, Depth: *depth, Time: *moveTime}
	b := selfplay.Player{Weights: wb, Lookahead: bLook(), Nodes: *nodes, Depth: *depth, Time: *moveTime}
	start := time.Now()
	st := selfplay.Match(a, b, selfplay.MatchConfig{
		Pairs: *pairs, Workers: *workers, Seed: *seed,
		Progress: func(st selfplay.Stats) {
			if st.Games()%20 == 0 {
				printStats(st, time.Since(start))
			}
		},
	})
	fmt.Print("final: ")
	printStats(st, time.Since(start))
	return nil
}

func printStats(st selfplay.Stats, el time.Duration) {
	elo, margin := st.Elo()
	fmt.Printf("games %d  A: +%d =%d -%d  score %.1f%%  elo %+.0f ±%.0f  avg margin %+.1f  (%v)\n",
		st.Games(), st.Wins, st.Draws, st.Losses, 100*st.Score(), elo, margin,
		float64(st.PointDiff)/float64(max(st.Games(), 1)), el.Round(time.Second))
}

func cmdTune(args []string) error {
	fs := flag.NewFlagSet("tune", flag.ContinueOnError)
	from := fs.String("from", "default", "starting weights")
	iters := fs.Int("iters", 300, "SPSA iterations")
	pairs := fs.Int("pairs", 16, "game pairs per iteration")
	nodes := fs.Uint64("nodes", 5000, "node budget per move")
	workers := fs.Int("workers", 0, "parallel games (0 = all CPUs)")
	lr := fs.Float64("lr", 1, "learning rate")
	seed := fs.Uint64("seed", 1, "random seed")
	out := fs.String("out", "tuned.json", "where to write the tuned weights")
	if err := fs.Parse(args); err != nil {
		return err
	}
	w, err := loadWeights(*from)
	if err != nil {
		return err
	}
	save := func(w eval.Weights) error {
		data, err := json.MarshalIndent(w, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(*out, append(data, '\n'), 0o644)
	}
	start := time.Now()
	var saveErr error
	t := selfplay.SPSA{
		Iters: *iters, Pairs: *pairs, Workers: *workers, LR: *lr, Seed: *seed,
		Limits: selfplay.Player{Nodes: *nodes},
		OnIter: func(k int, w eval.Weights, result float64) {
			if (k+1)%10 != 0 && k+1 != *iters {
				return
			}
			fmt.Printf("iter %d/%d (%v)  last result %+.2f\n", k+1, *iters, time.Since(start).Round(time.Second), result)
			for _, p := range w.Params() {
				fmt.Printf("  %-10s %d\n", p.Name, *p.Ptr)
			}
			if err := save(w); err != nil {
				saveErr = err
			}
		},
	}
	w = t.Run(w)
	if saveErr != nil {
		return saveErr
	}
	fmt.Printf("wrote %s\n", *out)
	return save(w)
}

func formatPV(pv []game.Move) string {
	parts := make([]string, len(pv))
	for i, m := range pv {
		parts[i] = m.String()
	}
	return strings.Join(parts, ", ")
}

func cmdRandom(args []string) error {
	fs := flag.NewFlagSet("random", flag.ContinueOnError)
	seed := fs.Uint64("seed", uint64(time.Now().UnixNano()), "random seed")
	asJSON := fs.Bool("json", false, "print each round start as JSON instead of ASCII")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rng := rand.New(rand.NewPCG(*seed, 0))
	s := game.NewGame(0)
	s.Refill(rng)
	show := func() error {
		if !*asJSON {
			fmt.Println(s)
			return nil
		}
		data, err := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(data))
		return err
	}
	if err := show(); err != nil {
		return err
	}
	var ml game.MoveList
	for !s.GameOver {
		if s.RoundOver() {
			if !s.ResolveRound() {
				s.Refill(rng)
			}
			if err := show(); err != nil {
				return err
			}
			continue
		}
		s.GenMoves(&ml)
		s.Apply(ml.Moves[rng.IntN(ml.N)])
	}
	fmt.Printf("seed %d: final %d-%d, winner %d (-1 = shared)\n",
		*seed, s.Players[0].Score, s.Players[1].Score, s.Winner())
	return nil
}
