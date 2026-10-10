# How the engine works

A tour of the ideas, in the order a move is found.

## 1. The position

A whole 2-player game fits in about 110 bytes with no pointers
([game/state.go](../internal/game/state.go)). That makes copying a position cheaper than
undoing a move, so the search just copies ("copy-make") and never has to get
an undo right.

- **Walls** are 25-bit masks, stored twice: by row and by column. A row or a
  column is then one shift and a 5-bit mask, and scoring a placement is two
  lookups in a 32×5 table of run lengths.
- **The floor is a number, not a list.** Every tile that lands on the floor
  ends up in the lid anyway, so only the count of occupied slots matters.
- **Factories are an unordered set.** Two factories holding the same tiles
  lead to the same positions, so the move generator skips the duplicate. The
  hash treats them the same way: it adds a key per factory instead of
  XOR-ing, so identical factories do not cancel out and any ordering of the
  same factories hashes equal.

Move generation, applying a move and resolving a round allocate nothing.

## 2. Why a round can be solved

Inside a round Azul has no hidden information and no luck: every tile is on
the table. Luck only enters when the factories are refilled. So within a round
the engine searches like a chess engine:

- **Negamax with alpha-beta (PVS)** and iterative deepening.
- **A transposition table**, which matters a lot here because the same position
  is reached by many move orders.
- **Move ordering**: the table's move first, then killer moves, then a cheap
  guess (tiles placed, a bonus for exactly finishing a line, a penalty for
  floor tiles), then history.

The end of the round is a leaf. There the round is scored for real (tiling,
adjacency points, floor penalties, end-of-game bonuses) and the evaluation
adds its judgement of what comes next.

**Exact results stay exact.** Every node records whether its subtree reached
the end of the round on every line. If it did, the stored value is valid at any
depth, and when the whole tree is like that the search stops early and reports
the round as solved. Late in a round this happens in milliseconds.

**Winning comes first.** A finished game is worth a large constant plus the
margin, so any win outranks any unfinished line, a bigger win outranks a
smaller one, and ties on points go to complete rows, as in the rules.

## 3. The evaluation

At a leaf the engine starts from the real score difference and adds a handful
of terms ([eval/eval.go](../internal/eval/eval.go)):

| Term | What it sees |
|---|---|
| Partial lines | Tiles committed to a line that is not finished: worth a share of the points they would score, minus a cost that grows with how many tiles are missing. |
| Wall potential | The points every empty wall cell would score if filled next. It rewards walls that set up future scoring. |
| Bonus proximity | Rows, columns and colors that are close to their end-of-game bonus. |
| First player | Holding the first-player token for the next round. |
| Overflow and completable lines | Mid-round only: tiles left on the table the player has no room for, and partial lines the remaining tiles can still finish. |
| End of game | Whether finishing the game soon suits whoever is ahead. |

Wall potential is computed without placing anything: a placement scores its
row run plus its column run, so the total splits into a per-row part and a
per-column part (two 32-entry tables) plus the count of empty cells with no
neighbours (one mask and a popcount).

## 4. Tuning by self-play

Every weight was set by playing the engine against itself
([selfplay/](../internal/selfplay)):

- Each deal is played twice with the seats swapped, so the luck of the deal
  cancels out.
- Searches are limited by node count, not time, so matches are reproducible.
- **SPSA** nudges all weights at once: shift every weight by a small random
  amount in both directions, play the two versions against each other, and
  move toward the winner.

A change is adopted only if it wins by clearly more than the noise, at equal
work and again at equal thinking time.

| Step | Result |
|---|---|
| Tuned weights vs points-only | about +300 Elo |
| Adding line-size cost, wall potential and tempo | +158 ± 14 Elo |
| Adding completable lines | about +30 Elo |
| Second tuning pass on the same terms | +8 ± 12 Elo (noise: not adopted) |

Not everything worked. Valuing the end of a round by **sampling next-round
deals** and searching each a few moves deep lost 10–50 Elo at equal work in
every configuration tried, so it is off by default
([search/lookahead.go](../internal/search/lookahead.go)).

## 5. Checking that it is right

- **Rules**: hand-computed scoring cases, a full round checked move by move,
  and random games that verify after every step that no tile is lost and the
  incremental hash matches a full recompute.
- **Search**: the engine's result must equal a plain alpha-beta with no table
  and no tricks, which itself must equal brute force on small positions. These
  tests were checked by planting bugs in the search and confirming each one
  is caught.
- **Speed**: benchmarks for move generation, evaluation and search.

## 6. One engine, two homes

The request layer ([api/](../internal/api)) knows nothing about HTTP. The native binary
wraps it in a web server with limits for public use; the browser build
([cmd/wasm](../cmd/wasm)) wraps the same code as WebAssembly and runs it in a
Web Worker. The page talks to both the same way, so a position analysed online
and offline gives the same answer.
