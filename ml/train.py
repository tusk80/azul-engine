"""Train the Azul evaluation network with PyTorch, on the GPU if there is one.

The same network, data split, target and optimiser as `azul train`, so the two
can be compared. The result is saved in the Go format and loads with
`azul selfplay -a-net`.

    python ml/train.py --data ml/data.csv --out ml/net-torch.json
"""

import argparse
import json
import time

import pandas as pd
import torch
from torch import nn


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="ml/data.csv")
    ap.add_argument("--out", default="ml/net-torch.json")
    ap.add_argument("--h1", type=int, default=128)
    ap.add_argument("--h2", type=int, default=32)
    ap.add_argument("--epochs", type=int, default=10)
    ap.add_argument("--batch", type=int, default=1024)
    ap.add_argument("--lr", type=float, default=1e-3)
    ap.add_argument("--lam", type=float, default=0.5, help="target mix: 1 = search eval only, 0 = final margin only")
    ap.add_argument("--val", type=float, default=0.05, help="share of rows (the last ones) held out")
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--device", default="cuda" if torch.cuda.is_available() else "cpu")
    a = ap.parse_args()
    torch.manual_seed(a.seed)
    dev = torch.device(a.device)

    t0 = time.time()
    df = pd.read_csv(a.data, dtype="int32")
    names = list(df.columns[:-3])
    x = torch.tensor(df[names].to_numpy(), dtype=torch.float32)
    ev = torch.tensor(df["eval"].to_numpy(), dtype=torch.float32) / 100
    margin = torch.tensor(df["margin"].to_numpy(), dtype=torch.float32)
    result = torch.tensor(df["result"].to_numpy(), dtype=torch.int8)
    del df
    k = int(len(x) * (1 - a.val))
    print(f"{k} training and {len(x) - k} validation positions, loaded in {time.time() - t0:.1f}s on {dev}")

    # Each input is divided by its root mean square over the training rows,
    # as in Go: zeros stay zero.
    scale = x[:k].pow(2).mean(0).sqrt()
    scale[scale == 0] = 1
    target = a.lam * ev + (1 - a.lam) * margin
    xs = (x / scale).to(dev)
    target, ev, margin, result = target.to(dev), ev.to(dev), margin.to(dev), result.to(dev)

    net = nn.Sequential(
        nn.Linear(len(names), a.h1), nn.ReLU(),
        nn.Linear(a.h1, a.h2), nn.ReLU(),
        nn.Linear(a.h2, 1),
    ).to(dev)
    for m in net:
        if isinstance(m, nn.Linear):
            nn.init.kaiming_normal_(m.weight, nonlinearity="relu")
            nn.init.zeros_(m.bias)
    opt = torch.optim.Adam(net.parameters(), lr=a.lr)
    steps = a.epochs * ((k + a.batch - 1) // a.batch)
    sched = torch.optim.lr_scheduler.LambdaLR(opt, lambda s: 1 - 0.9 * s / steps)

    def report():
        with torch.no_grad():
            p = net(xs[k:]).squeeze(1)
            m, r = margin[k:], result[k:]
            decided = r != 0
            mse = (p - m).pow(2).mean().item()
            win = ((p[decided] > 0) == (r[decided] > 0)).float().mean().item()
            emse = (ev[k:] - m).pow(2).mean().item()
            ewin = ((ev[k:][decided] > 0) == (r[decided] > 0)).float().mean().item()
            return mse, win, emse, ewin

    _, _, emse, ewin = report()
    print(f"hand-written eval vs final margin: MSE {emse:.2f}, picks the winner {100 * ewin:.1f}%")
    print("epoch  train loss  val MSE  winner%  time")
    t0 = time.time()
    for epoch in range(1, a.epochs + 1):
        order = torch.randperm(k, device=dev)
        total = 0.0
        for b in range(0, k, a.batch):
            i = order[b:b + a.batch]
            out = net(xs[i]).squeeze(1)
            loss = 0.5 * (out - target[i]).pow(2).mean()
            opt.zero_grad(set_to_none=True)
            loss.backward()
            opt.step()
            sched.step()
            total += loss.item() * len(i)
        mse, win, _, _ = report()
        print(f"{epoch:5d}  {total / k:10.2f}  {mse:7.2f}  {100 * win:6.1f}%  {time.time() - t0:.0f}s")

    # Go's layout: W1 is input-major (W1[i*H1+j]), W2 is output-major.
    l1, l2, l3 = net[0], net[2], net[4]
    flat = lambda t: t.detach().cpu().reshape(-1).tolist()
    model = {
        "In": len(names), "H1": a.h1, "H2": a.h2,
        "Scale": scale.tolist(),
        "W1": flat(l1.weight.t()), "B1": flat(l1.bias),
        "W2": flat(l2.weight), "B2": flat(l2.bias),
        "W3": flat(l3.weight), "B3": flat(l3.bias),
    }
    with open(a.out, "w") as f:
        json.dump(model, f)
    print("saved", a.out)


if __name__ == "__main__":
    main()
