"""Azul Engine (Go, over HTTP) against cestpasphoto/alpha-zero-general's Azul AlphaZero.

The game runs in their simulator. Each deal seed is played twice with the seats
swapped. Our engine answers from `azul serve` on 127.0.0.1:8765.

    python ml/alphazero_match.py <their repo dir> <pairs> <our timeMs> [first seed] [numMCTSSims]

Needs numpy, numba, torch, torchvision, onnx, onnxruntime, onnxscript, tqdm and
colorama (CPU builds are enough).
"""
import json
import sys
import time
import urllib.request

import numpy as np
import torch

# Their checkpoints hold only plain types and tensors: never unpickle code.
_load = torch.load
torch.load = lambda *a, **k: _load(*a, **{**k, 'weights_only': True})
# Their code exports the net with dynamic_axes, which the legacy exporter handles.
_export = torch.onnx.export
torch.onnx.export = lambda *a, **k: _export(*a, **{**k, 'dynamo': False})
# The export writes a temporary file into the working directory.
import os  # noqa: E402
os.chdir(os.path.dirname(os.path.abspath(__file__)))

REPO, PAIRS, OUR_MS = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
SEED0 = int(sys.argv[4]) if len(sys.argv) > 4 else 1
SIMS = int(sys.argv[5]) if len(sys.argv) > 5 else None
sys.path.insert(0, REPO)

import pit  # noqa: E402
from utils import dotdict  # noqa: E402
from azul.AzulGame import AzulGame  # noqa: E402
from azul.AzulLogicNumba import Board, observation_size  # noqa: E402

COLORS = ['blue', 'yellow', 'red', 'black', 'white']
F = 5  # factories with 2 players
ROW_COLOURS, ROW_NUMBERS, ROW_WALLS = 4 + F, 4 + F + 2, 4 + F + 4


def to_json(b):
    """Their canonical board (side to move is player 0) as our state JSON."""
    holder = [p for p in range(2) if b[ROW_COLOURS + p, 5] == 1]
    players = []
    for p in range(2):
        lines = []
        for i in range(5):
            n = int(b[ROW_NUMBERS + p, i])
            lines.append({'color': COLORS[b[ROW_COLOURS + p, i]], 'count': n} if n else {})
        wall = [''.join('x' if b[ROW_WALLS + 5 * p + r, c] else '.' for c in range(5)) for r in range(5)]
        players.append({'score': int(b[0, p]), 'wall': wall, 'lines': lines,
                        'floor': min(int(b[ROW_NUMBERS + p, 5]), 7)})
    return {
        'round': max(int(b[0, 2]), 1), 'toMove': 0,
        'nextFirst': holder[0] if holder else 0,
        'tokenInCenter': bool(b[3, 5]),
        'factories': [[COLORS[c] for c in range(5) for _ in range(b[4 + f, c])] for f in range(F)],
        'center': [COLORS[c] for c in range(5) for _ in range(b[3, c])],
        # Their lid also holds this round's floor tiles, which our format counts
        # on the floor: give the bag only and let the lid be inferred.
        'bag': {COLORS[c]: int(b[1, c]) for c in range(5)},
        'players': players,
    }


def ours(board, _n):
    body = json.dumps({'state': to_json(board), 'timeMs': OUR_MS, 'engine': 'neural', 'multiPV': 1}).encode()
    req = urllib.request.Request('http://127.0.0.1:8765/bestmove', body, {'Content-Type': 'application/json'})
    with urllib.request.urlopen(req) as r:
        m = json.load(r)['move']
    src = 0 if m['source'] == 'center' else m['factory']
    return src * 30 + COLORS.index(m['color']) * 6 + (5 if m['floor'] else m['line'] - 1)


def new_board(seed):
    b = Board(2)
    b.copy_state(np.zeros(observation_size(2), dtype=np.int8), False)
    b.bag[0, :5] = 20
    b.player_colours[:, :5] = -1
    b.setup_new_round(seed, 0)
    return b.get_state().copy()


def play(game, seats, seed):
    """seats[i] plays as player i (player 0 starts). Returns final scores."""
    board, cur, n = new_board(seed), 0, 0
    while not game.getGameEnded(board, cur).any():
        canon = game.getCanonicalForm(board, cur)
        a = seats[cur](canon, n)
        if not game.getValidMoves(canon, 0)[a]:
            raise SystemExit(f'seed {seed}: illegal move {a} from seat {cur}')
        board, cur = game.getNextState(board, cur, a, random_seed=seed)
        n += 1
    return [int(game.getScore(board, p)) for p in range(2)], game.getGameEnded(board, cur)


def main():
    args = dotdict({'game': 'azul', 'numMCTSSims': SIMS, 'cpuct': None, 'fpu': None,
                    'universes': None, 'strict': False,
                    **{f'{k}{i}': None for k in 'mcfu' for i in (1, 2)}})
    factory, mcts_args = pit.create_player(f'{REPO}/azul/pretrained_2pl.pt', args, 0)
    print(f'AlphaZero: {mcts_args.numMCTSSims} simulations per move; us: {OUR_MS} ms per move', flush=True)
    game = pit.game
    timing = {'us': [0.0, 0], 'az': [0.0, 0]}

    def timed(name, f):
        def g(b, n):
            t = time.perf_counter()
            a = f(b, n)
            timing[name][0] += time.perf_counter() - t
            timing[name][1] += 1
            return a
        return g

    w = d = l = 0
    margin = 0
    for seed in range(SEED0, SEED0 + PAIRS):
        for us_seat in (0, 1):
            seats = [None, None]
            seats[us_seat] = timed('us', ours)
            seats[1 - us_seat] = timed('az', factory())
            scores, ended = play(game, seats, seed)
            r = ended[us_seat]
            if r == 1:
                w += 1
            elif r == -1:
                l += 1
            else:
                d += 1
            margin += scores[us_seat] - scores[1 - us_seat]
        g = w + d + l
        s = (w + d / 2) / g
        print(f'games {g}  us: +{w} ={d} -{l}  score {s:.1%}  avg margin {margin / g:+.1f}  '
              f'move time us {timing["us"][0] / max(timing["us"][1], 1) * 1000:.0f} ms, '
              f'az {timing["az"][0] / max(timing["az"][1], 1) * 1000:.0f} ms', flush=True)


if __name__ == '__main__':
    main()
