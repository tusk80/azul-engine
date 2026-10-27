# A learned evaluation

The engine's search needs a function that says how good a position is. The
original one is hand-written: fifteen terms (line progress, wall shape, bonus
proximity, the first-player token…) whose weights were tuned by self-play. This
experiment replaces it with a small neural network trained on self-play
games, and measures the result in games won.

The network was trained twice, from scratch in Go and with PyTorch on a GPU,
as a benchmark and as a check that the hand-written Go training is correct.

## Pipeline

1. **Data.** `azul gendata` plays the engine against itself (30,000 games,
   20,000 nodes per move, 5% random moves for variety) and writes each
   position as a row: 170 small integers describing the position from the
   side to move, plus three labels: the search's value, the final score margin
   and the result. 1.55 million rows, 7 minutes on 24 cores.
2. **Training.** A perceptron with two hidden ReLU layers predicts the final
   margin in points. The target mixes the search's value and the real margin
   half and half. Adam, batch 1024, 10 epochs, the last 5% of rows held out.
   - Go: `azul train`, about 300 lines in [internal/nn](../internal/nn), no
     dependencies, data-parallel over CPU cores. A gradient check test compares
     backpropagation with numerical derivatives.
   - PyTorch: [ml/train.py](../ml/train.py), same network, split, target and
     optimiser, saving to the same JSON format.
3. **Play.** The network plugs into the search in place of the hand-written
   eval ([nn.Evaluator](../internal/nn/nn.go)). Its first layer only visits
   nonzero inputs, since most features are empty wall cells.
4. **Measure.** Matches against the hand-written eval, each deal played twice
   with seats swapped, at equal nodes (knowledge) and equal time (strength).

## Results

**What the network sees matters most.** With the raw board only, the network
matched the hand-written eval at equal nodes but no better. Adding the board as
it would look if the round were scored now (projected walls and scores, which
the hand-written eval also uses) gave the jump:

| Features | Equal nodes (20k/move) |
|---|---|
| Raw board, 128×32 | −7 ± 34 Elo (400 games) |
| Raw + scored-now board, 128×32 | **+141 ± 13 Elo** (3000 games) |

**Smaller was stronger in real time, at first.** A bigger network knows more
but costs more per position, so the search sees fewer positions. These are the
first version's numbers, before the speed-up below:

| Network | Equal nodes | Equal time, 40 ms/move |
|---|---|---|
| 128×32 | +141 ± 13 | +6 ± 24 |
| 64×16 | +114 ± 25 | **+64 ± 13** (3000 games) |
| 32×16 | +96 ± 25 | +62 ± 24 |

At 200 ms per move the 64×16 network was **+110 ± 35 Elo** (400 games): the
more time each side has, the more the better judgement counts.

**Speed is strength.** The first version cost 2,161 ns per searched position
against the classic eval's 309 (search benchmark, `go test ./internal/nn -bench
Search`). The evaluator now runs the network the way chess engines run NNUE:

- The search evaluates positions that differ by a move or two, so the first
  layer is kept between calls and only the rows of inputs that changed are
  applied (a fresh pass if more than 24 changed).
- That layer works in fixed-point integers with the input scaling folded into
  the weights, so thousands of updates add up exactly: a test checks the
  incremental result equals a fresh one, position by position, and stays within
  0.02 points of the float network.
- The second layer reads only the active (nonzero after ReLU) first-layer units.

| | ns per position | 64×16 vs classic, 40 ms/move |
|---|---|---|
| Classic eval | 309 | |
| Network, first version | 2,161 | +64 ± 13 (3000 games) |
| Network, incremental | **704** | **+112 ± 13** (3000 games) |

At 200 ms per move it is +111 ± 36 (400 games). The speed-up also made the
bigger network usable: 128×32 went from +6 ± 24 to +135 ± 26 at 40 ms per move.
Head to head it ties the 64×16 (−10 ± 12 over 3000 games), so the smaller,
lighter one stays the default.

**The target mix.** Training only on the search's own values, or only on game
outcomes, did worse than half and half (raw features, 128×32, equal nodes):

| Target | Elo |
|---|---|
| Search value only | −127 ± 36 |
| Half and half | −7 ± 34 |
| 25% value, 75% outcome | −63 ± 34 |
| Outcome only | −84 ± 35 |

**Go versus PyTorch.** Same data, same network, 10 epochs:

| | Go, CPU (24 threads) | PyTorch, RTX 5070 Laptop |
|---|---|---|
| Training, 64×16 | 19 s | 24 s |
| Training, 128×32 | 42 s | 22 s |
| Validation error, 128×32 (points²) | 153.0 | 153.2 |
| 64×16 at equal time | +66 ± 24 | +64 ± 24 |

The two agree to the decimal, which checks the hand-written Go training. For a
network this small the GPU is no faster: each step is too little work to
outweigh moving the batch. It pays off only for the bigger network.

## Reproduce

```
azul gendata -games 30000 -seed 1000000 -out ml/data.csv
azul train -data ml/data.csv -h1 64 -h2 16 -out net.json
azul selfplay -a-net net.json -b default -pairs 1500 -time 40ms
```

## Self-play

[ml/selfplay-loop.sh](../ml/selfplay-loop.sh) runs generations: the current
network plays 10,000 games against itself, a new 64×16 network is trained on
the last two generations' games, and it plays 800 games against its parent and
800 against the classic engine at 40 ms per move. It takes over only if it
beats its parent. It ran on a 4-core server; with the faster evaluator a
generation takes about 40 minutes.

| Generation | vs its parent | vs classic |
|---|---|---|
| 0 (trained on the classic engine's games) | | +112 ± 13 (3000 games) |
| **1** | **+82 ± 13** (3000 games) | **+148 ± 14** (3000 games) |
| 2 | −2 ± 24 | +107 ± 25 |
| 3 | −10 ± 24 | +99 ± 25 |
| 4 | +12 ± 24, then −9 ± 12 over 3000 games | +120 ± 25 |

One generation of learning from its own games was worth +82 Elo; then it
levelled off. Generation 4 passed the 800-game check by luck and tied
generation 1 over 3000 games, a reminder of why every adoption is confirmed
with a long match.

Generation 1 ships inside the engine: [internal/nn/default-net.json](../internal/nn/default-net.json).

## What it looks at

[ml/inspect_net.py](../ml/inspect_net.py) runs the network over 105,305
positions from 2,000 fresh games it never trained on. Its error falls from 16.1
points RMS in round 1 (it picks the winner 56% of the time) to 11.6 in round 5
(79%): early uncertainty is mostly the random deals still to come. Permutation
importance (rise in MSE when a group of inputs is shuffled): the board as if
scored now 198 and 186 (opponent's, own), walls 53 and 45, scores and floors
34, pattern lines 34, token and round 2, tiles on the table 1. The search
handles what happens to the tiles on the table; the network values the boards.

## Where it is used

The network is the default brain on the analysis board and in the overlay;
the classic evaluation stays one click away. The site's
[Neural vs classic](https://azul.yesil.cc/compare.html) page shows these
results as charts.

## Next

- Break the self-play plateau: more games per generation, a lower learning
  rate when fine-tuning, or more exploration in the games.
- Update the first layer on each move inside the search (true NNUE), so even
  the feature extraction is incremental.
