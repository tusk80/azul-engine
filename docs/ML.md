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

**Smaller is stronger in real time.** A bigger network knows more but costs
more per position, so the search sees fewer positions:

| Network | Equal nodes | Equal time, 40 ms/move |
|---|---|---|
| 128×32 | +141 ± 13 | +6 ± 24 |
| 64×16 | +114 ± 25 | **+64 ± 13** (3000 games) |
| 32×16 | +96 ± 25 | +62 ± 24 |

At 200 ms per move the 64×16 network is **+110 ± 35 Elo** (400 games): the
more time each side has, the more the better judgement counts.

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

The trained 64×16 network ships inside the engine: [internal/nn/default-net.json](../internal/nn/default-net.json).

## Where it is used

The network is the default brain on the analysis board and in the overlay;
the classic evaluation stays one click away. The site's
[Neural vs classic](https://azul.yesil.cc/compare.html) page shows these
results as charts.

## Next

- Faster inference (integer weights, updating the first layer incrementally
  as moves are made, as chess engines do) to afford a bigger network.
- Train again on games played by the network itself.
