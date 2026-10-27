"""Where does the network go wrong, and what does it look at?

Runs a network (the Go JSON format) over held-out positions from azul gendata
and reports its error by stage of the game, then permutation importance: how
much worse it gets when one group of inputs is shuffled across positions.

    python ml/inspect_net.py --net internal/nn/default-net.json --data ml/holdout.csv
"""

import argparse
import json

import numpy as np
import pandas as pd


def forward(net, x):
    h1 = np.maximum(x / net["Scale"] @ net["W1"] + net["B1"], 0)
    h2 = np.maximum(h1 @ net["W2"].T + net["B2"], 0)
    return h2 @ net["W3"] + net["B3"][0]


def load_net(path):
    n = json.load(open(path))
    a = {k: np.asarray(n[k], dtype=np.float32) for k in ("Scale", "W1", "B1", "W2", "B2", "W3", "B3")}
    a["W1"] = a["W1"].reshape(n["In"], n["H1"])  # input-major, as stored
    a["W2"] = a["W2"].reshape(n["H2"], n["H1"])
    return a


def groups(names):
    """Input groups by meaning, from the column names dataset.Names writes."""
    def group(c):
        side = "own" if c.startswith("me_") else "opponent's" if c.startswith("opp_") else ""
        if "scored_" in c:
            return f"{side} board as if scored now"
        if "_wall_" in c:
            return f"{side} wall"
        if "_line" in c:
            return f"{side} pattern lines"
        if c.endswith(("_floor", "_score")):
            return f"{side} score and floor"
        if c.startswith(("center_", "factories_")):
            return "tiles on the table"
        return "token and round"
    out = {}
    for i, c in enumerate(names):
        out.setdefault(group(c), []).append(i)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--net", default="internal/nn/default-net.json")
    ap.add_argument("--data", default="ml/holdout.csv")
    ap.add_argument("--seed", type=int, default=1)
    a = ap.parse_args()

    df = pd.read_csv(a.data, dtype="int32")
    names = list(df.columns[:-3])
    x = df[names].to_numpy(np.float32)
    margin = df["margin"].to_numpy(np.float32)
    search = df["eval"].to_numpy(np.float32) / 100
    net = load_net(a.net)
    pred = forward(net, x)
    mse = lambda p: float(np.mean((p - margin) ** 2))
    base = mse(pred)
    print(f"{len(df)} positions. Network error against the final margin: MSE {base:.1f} (RMS {base ** .5:.1f} points);")
    print(f"the search's own value: MSE {mse(search):.1f}.\n")

    # The round the position is in, and how far into the round (tiles left).
    rnd = df["round"].to_numpy()
    left = df[[c for c in names if c.startswith(("center_", "factories_"))]].to_numpy().sum(1)
    stage = np.where(left > 12, "early", np.where(left > 4, "middle", "late"))
    print("Error by round (RMS points; share of decided games whose winner it picks):")
    print("round  positions  network  search  picks winner")
    res = df["result"].to_numpy()
    for r in sorted(set(rnd)):
        m = rnd == r
        if m.sum() < 500:
            continue
        d = m & (res != 0)
        win = np.mean((pred[d] > 0) == (res[d] > 0)) * 100
        print(f"{r:5d}  {m.sum():9d}  {np.sqrt(np.mean((pred[m] - margin[m]) ** 2)):7.1f}  "
              f"{np.sqrt(np.mean((search[m] - margin[m]) ** 2)):6.1f}  {win:11.0f}%")
    print("\nError by stage of the round (RMS points):")
    for s in ("early", "middle", "late"):
        m = stage == s
        print(f"  {s:6s} ({left[m].min()}-{left[m].max()} tiles left)  {np.sqrt(np.mean((pred[m] - margin[m]) ** 2)):.1f}")

    # Permutation importance: shuffle one group of inputs across positions.
    rng = np.random.default_rng(a.seed)
    print("\nPermutation importance (rise in MSE when the group is shuffled):")
    rows = []
    for g, cols in groups(names).items():
        xs = x.copy()
        xs[:, cols] = xs[rng.permutation(len(xs))][:, cols]
        rows.append((mse(forward(net, xs)) - base, g, len(cols)))
    for rise, g, k in sorted(rows, reverse=True):
        print(f"  {rise:7.1f}  {g} ({k} inputs)")


if __name__ == "__main__":
    main()
