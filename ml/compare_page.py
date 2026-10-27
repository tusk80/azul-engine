"""Writes the site's "Neural vs classic" page with its charts drawn as static
SVG. The site's CSP forbids inline styles, so every size is an SVG attribute.
The numbers are the match results in docs/ML.md.

    python ml/compare_page.py internal/server/ui/compare.html
"""
import sys

OUT = sys.argv[1]
W = 640  # chart width in viewBox units
LABEL = 150  # left column for row labels


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def hbars(rows, lo, hi, unit="Elo", row_h=34, bar_h=16, series=None):
    """rows: [(label, [(value, err, cls, name)])]. One bar per entry, grouped per row."""
    per = max(len(r[1]) for r in rows)
    group_h = per * (bar_h + 4) + 14
    height = len(rows) * group_h + 28
    plot_w = W - LABEL - 70
    x = lambda v: LABEL + (v - lo) / (hi - lo) * plot_w
    out = [f'<svg class="chart" viewBox="0 0 {W} {height}" role="img" aria-label="bar chart">']
    # grid and zero line
    step = 50
    t = (lo // step) * step
    while t <= hi:
        if t >= lo:
            cls = "zero" if t == 0 else "grid"
            out.append(f'<line class="{cls}" x1="{x(t):.1f}" x2="{x(t):.1f}" y1="0" y2="{height - 22}"/>')
            out.append(f'<text class="tick" x="{x(t):.1f}" y="{height - 6}" text-anchor="middle">{t:+d}</text>' if t else
                       f'<text class="tick" x="{x(t):.1f}" y="{height - 6}" text-anchor="middle">0</text>')
        t += step
    y = 8
    for label, bars in rows:
        mid = y + (len(bars) * (bar_h + 4) - 4) / 2
        out.append(f'<text class="rowlabel" x="{LABEL - 12}" y="{mid + 5:.1f}" text-anchor="end">{esc(label)}</text>')
        for v, err, cls, name in bars:
            x0, x1 = sorted((x(0), x(v)))
            # 4px rounded data end, square at the baseline: draw rounded rect + square patch at the zero side
            neg = " neg" if v < 0 else ""
            out.append(f'<g class="bar {cls}{neg}"><title>{esc(label)}, {esc(name)}: {v:+d} ± {err} {unit}</title>')
            out.append(f'<rect x="{x0:.1f}" y="{y}" width="{max(x1 - x0, 1):.1f}" height="{bar_h}" rx="4"/>')
            if x1 - x0 > 8:
                px = x0 if v >= 0 else x1 - 6
                out.append(f'<rect x="{px:.1f}" y="{y}" width="6" height="{bar_h}"/>')
            if err:
                e0, e1 = x(max(v - err, lo)), x(min(v + err, hi))  # clipped to the plot
                cy = y + bar_h / 2
                out.append(f'<line class="err" x1="{e0:.1f}" x2="{e1:.1f}" y1="{cy}" y2="{cy}"/>'
                           f'<line class="err" x1="{e0:.1f}" x2="{e0:.1f}" y1="{cy - 5}" y2="{cy + 5}"/>'
                           f'<line class="err" x1="{e1:.1f}" x2="{e1:.1f}" y1="{cy - 5}" y2="{cy + 5}"/>')
            tx = max(x(v + err), x(v)) + 8 if v >= 0 else min(x(v - err), x(v)) - 8
            anchor = "start" if v >= 0 else "end"
            out.append(f'<text class="val" x="{tx:.1f}" y="{y + bar_h - 3}" text-anchor="{anchor}">{v:+d}</text></g>')
            y += bar_h + 4
        y += 14
    out.append("</svg>")
    return "\n".join(out)


def stacked(parts):
    """parts: [(count, cls, name)] -> one 100% bar with 2px gaps."""
    total = sum(p[0] for p in parts)
    gap = 2
    usable = W - gap * (len(parts) - 1)
    out = [f'<svg class="chart" viewBox="0 0 {W} 28" role="img" aria-label="share of games">']
    x = 0.0
    for i, (n, cls, name) in enumerate(parts):
        w = usable * n / total
        rx = 4 if i in (0, len(parts) - 1) else 0
        out.append(f'<g class="bar {cls}"><title>{esc(name)}: {n:,} of {total:,} games ({100 * n / total:.1f}%)</title>'
                   f'<rect x="{x:.1f}" y="0" width="{max(w, 1):.1f}" height="28" rx="{rx}"/></g>')
        x += w + gap
    out.append("</svg>")
    return "\n".join(out)


result_bar = stacked([(2088, "neural", "Neural network wins"), (33, "draw", "Shared wins"), (879, "classic", "Classic wins")])

# Each generation against the classic engine, 40 ms per move. Generation 0 is
# the network trained on the classic engine's games.
gen_chart = hbars([
    ("Generation 0", [(112, 13, "know", "trained on the classic engine's games, 3,000 games")]),
    ("Generation 1", [(148, 14, "neural", "trained on its own games, 3,000 games")]),
    ("Generation 2", [(107, 25, "know", "800 games, did not beat its parent")]),
    ("Generation 3", [(99, 25, "know", "800 games, did not beat its parent")]),
    ("Generation 4", [(120, 25, "know", "800 games, tied its parent over 3,000")]),
], 0, 200)

speed_chart = hbars([
    ("First version", [(64, 13, "know", "3,000 games")]),
    ("3× faster", [(112, 13, "neural", "3,000 games")]),
], 0, 150)

size_chart = hbars([
    ("64 × 16", [(114, 25, "know", "equal positions"), (112, 13, "neural", "equal time")]),
    ("128 × 32", [(141, 13, "know", "equal positions"), (135, 26, "neural", "equal time")]),
], 0, 200)

feature_chart = hbars([
    ("Board only", [(-7, 34, "know", "equal positions")]),
    ("Board + scored now", [(141, 13, "neural", "equal positions")]),
], -50, 200)

target_chart = hbars([
    ("Search value only", [(-127, 36, "know", "")]),
    ("Outcome only", [(-84, 35, "know", "")]),
    ("¼ value, ¾ outcome", [(-63, 34, "know", "")]),
    ("Half and half", [(-7, 34, "neural", "")]),
], -200, 50)

html = f"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Neural vs Classic — Azul Engine</title>
  <meta name="description" content="A neural network trained on 1.5 million self-play positions against a hand-tuned evaluation: 3,000 games, +148 Elo at equal time.">
  <meta name="theme-color" content="#0f131b">
  <meta property="og:type" content="article">
  <meta property="og:title" content="Neural network vs classic: which Azul engine is stronger?">
  <meta property="og:description" content="A small network trained on self-play beats the hand-tuned engine: 70% of the points over 3,000 games at equal thinking time.">
  <meta property="og:url" content="https://azul.yesil.cc/compare.html">
  <meta property="og:image" content="https://azul.yesil.cc/og.png">
  <link rel="icon" href="icon.svg" type="image/svg+xml">
  <link rel="stylesheet" href="style.css">
  <script src="theme.js"></script>
  <script src="compare.js" defer></script>
</head>
<body class="cmp">
  <header class="topbar">
    <a class="brand" href="/" aria-label="Azul Engine home">
      <svg class="logo" viewBox="0 0 32 32" aria-hidden="true">
        <rect x="2" y="2" width="13" height="13" rx="3" class="l-blue"/>
        <rect x="17" y="2" width="13" height="13" rx="3" class="l-yellow"/>
        <rect x="2" y="17" width="13" height="13" rx="3" class="l-red"/>
        <rect x="17" y="17" width="13" height="13" rx="3" class="l-white"/>
      </svg>
      <span class="brand-text">
        <span class="brand-name">Azul Engine</span>
        <span class="brand-sub">Neural vs classic</span>
      </span>
    </a>
    <nav class="actions">
      <a class="btn" href="blog.html">Dev log</a>
      <a class="btn primary" href="/">Open the board</a>
      <button id="btn-theme" class="icon" title="Switch light / dark" aria-label="Switch light or dark theme">◐</button>
    </nav>
  </header>

  <main class="cmp-main">
    <section class="cmp-hero">
      <p class="eyebrow">Machine learning benchmark · two brains, one search</p>
      <h1>The neural network wins.</h1>
      <p class="lede">The engine can judge positions two ways: with <strong>classic</strong> rules written and tuned by hand,
        or with a <strong>neural network</strong> that learned from 1.5 million positions the engine played against itself,
        then from its own games. We had them play each other 3,000 times, with the same thinking time.</p>
      <div class="stats">
        <div class="stat"><span class="num">+148</span><span class="lbl">Elo at equal thinking time<br><small>± 14, 3,000 games</small></span></div>
        <div class="stat"><span class="num">70%</span><span class="lbl">of the points won<br><small>2,088 wins, 879 losses</small></span></div>
        <div class="stat"><span class="num">+8.8</span><span class="lbl">points per game<br><small>average final margin</small></span></div>
        <div class="stat"><span class="num">3.1×</span><span class="lbl">faster search<br><small>than the network's first version</small></span></div>
      </div>
    </section>

    <section class="cmp-card">
      <h2>3,000 games, same clock</h2>
      <p>Every deal was played twice with the seats swapped, so neither side got the luckier tiles. 40 ms per move.</p>
      {result_bar}
      <ul class="legend">
        <li><span class="sw neural"></span>Neural network won <b>2,088</b></li>
        <li><span class="sw draw"></span>Shared <b>33</b></li>
        <li><span class="sw classic"></span>Classic won <b>879</b></li>
      </ul>
    </section>

    <section class="cmp-card">
      <h2>Speed is strength</h2>
      <p>A network judges better than hand-written rules but costs more per position, so in the same time it looks at fewer of them. Its first version took 2,161 ns per position against the classic engine's 309. Running its first layer the way chess engines do, updating only the inputs a move changes, in exact integer arithmetic, brought that to 704 ns: the same network gained almost 50 Elo. Elo over the classic engine at 40 ms per move; lines show the 95% range.</p>
      {speed_chart}
    </section>

    <section class="cmp-card">
      <h2>It taught itself</h2>
      <p>The first network learned from games the classic engine played. Then the network played 10,000 games against itself, a new network learned from them, and it had to beat its parent to take over. One generation of self-play was worth +82 ± 13 Elo over its parent. After that it levelled off: the next three generations could not beat it, so the board uses generation 1. Elo over the classic engine at 40 ms per move.</p>
      {gen_chart}
      <ul class="legend">
        <li><span class="sw neural"></span>The board's brain</li>
        <li><span class="sw know"></span>Other generations</li>
      </ul>
    </section>

    <section class="cmp-grid">
      <div class="cmp-card">
        <h2>Fast enough to use what it knows</h2>
        <p>Before the speed-up the bigger network was too slow to use its knowledge: +6 at equal time. Now each network plays about as well at equal time as at equal positions. Head to head, 128 × 32 and 64 × 16 tie (−10 ± 12 over 3,000 games), so the board keeps the smaller, lighter one.</p>
        {size_chart}
        <ul class="legend">
          <li><span class="sw know"></span>Equal positions: what it knows</li>
          <li><span class="sw neural"></span>Equal time: how it plays</li>
        </ul>
      </div>
      <div class="cmp-card">
        <h2>What it sees matters most</h2>
        <p>Given only the board, the network merely tied. Showing it the board <em>as it would look if the round were scored now</em> was worth 150 Elo.</p>
        {feature_chart}
        <h2 class="sub">What it learns from</h2>
        <p>Learning half from the search's opinion and half from who actually won beat either alone.</p>
        {target_chart}
      </div>
    </section>

    <section class="cmp-card">
      <h2>How it was built</h2>
      <ol class="steps">
        <li><b>Play.</b> The classic engine played 30,000 games against itself; every position became a row of 170 numbers with what happened next.</li>
        <li><b>Learn.</b> A network with two small hidden layers learned to predict the final score margin. It was trained twice: from scratch in plain Go (no libraries), and with PyTorch on a GPU.</li>
        <li><b>Plug in.</b> The network replaces the hand-written judgement inside the same search, so the only difference between the two engines is the brain.</li>
        <li><b>Teach itself.</b> The network played itself; a new network learned from those games and took over only after beating its parent.</li>
        <li><b>Speed up.</b> Consecutive positions in a search differ by a move or two, so the network's first layer is updated rather than recomputed, in integers so it never drifts. Three times faster, exactly the same answers.</li>
        <li><b>Prove it.</b> Thousands of games at equal positions and at equal time, every deal played from both seats.</li>
      </ol>
      <table class="cmp-table">
        <caption>Go from scratch vs PyTorch, same data and network</caption>
        <thead><tr><th></th><th>Go, CPU</th><th>PyTorch, RTX 5070</th></tr></thead>
        <tbody>
          <tr><td>Training time, 64 × 16</td><td>19 s</td><td>24 s</td></tr>
          <tr><td>Training time, 128 × 32</td><td>42 s</td><td>22 s</td></tr>
          <tr><td>Validation error (points²)</td><td>153.0</td><td>153.2</td></tr>
          <tr><td>Elo at equal time, 64 × 16 (first version)</td><td>+66 ± 24</td><td>+64 ± 24</td></tr>
        </tbody>
      </table>
      <p class="note">The two agree to the decimal, which checks the hand-written training. A network this small is too little work per step for the GPU to pay off; it only pulls ahead on the bigger one.</p>
    </section>

    <section class="cmp-card cmp-choose">
      <h2>Which should you use?</h2>
      <div class="choose">
        <div><h3><span class="sw neural"></span>Neural network</h3><p>The stronger player, and the default. Use it when you want the best move.</p></div>
        <div><h3><span class="sw classic"></span>Classic</h3><p>Every term is a readable rule you can look up. Use it to see how a hand-built judgement thinks, or to compare the two on a position.</p></div>
      </div>
      <p><a class="btn primary" href="/">Try both on the board</a> <a class="btn" href="blog.html">Read the dev log</a> <a class="btn" href="https://github.com/tusk80/azul-engine/blob/main/docs/ML.md" rel="noopener">Technical write-up and code</a></p>
    </section>

    <details class="cmp-card">
      <summary>All the numbers</summary>
      <table class="cmp-table">
        <thead><tr><th>Experiment</th><th>Games</th><th>Elo vs classic</th></tr></thead>
        <tbody>
          <tr><td>Generation 1 (the board's brain), 40 ms per move</td><td>3,000</td><td>+148 ± 14</td></tr>
          <tr><td>Generation 1 against generation 0, 40 ms per move</td><td>3,000</td><td>+82 ± 13</td></tr>
          <tr><td>Generation 4 against generation 1, 40 ms per move</td><td>3,000</td><td>−9 ± 12</td></tr>
          <tr><td>Generation 0, 40 ms / 200 ms per move</td><td>3,000 / 400</td><td>+112 ± 13 / +111 ± 36</td></tr>
          <tr><td>128 × 32, 40 ms per move</td><td>800</td><td>+135 ± 26</td></tr>
          <tr><td>128 × 32 against 64 × 16, 40 ms per move</td><td>3,000</td><td>−10 ± 12</td></tr>
          <tr><td>First version, 64 × 16, 40 ms / 200 ms per move</td><td>3,000 / 400</td><td>+64 ± 13 / +110 ± 35</td></tr>
          <tr><td>First version, 32 × 16, equal positions / equal time</td><td>800 / 800</td><td>+96 ± 25 / +62 ± 24</td></tr>
          <tr><td>First version, 64 × 16, equal positions / equal time</td><td>800 / 800</td><td>+114 ± 25 / +66 ± 24</td></tr>
          <tr><td>First version, 128 × 32, equal positions / equal time</td><td>3,000 / 800</td><td>+141 ± 13 / +6 ± 24</td></tr>
          <tr><td>Board only, 128 × 32, equal positions</td><td>400</td><td>−7 ± 34</td></tr>
          <tr><td>Target: search value only</td><td>400</td><td>−127 ± 36</td></tr>
          <tr><td>Target: outcome only</td><td>400</td><td>−84 ± 35</td></tr>
          <tr><td>Target: 25% value, 75% outcome</td><td>400</td><td>−63 ± 34</td></tr>
          <tr><td>Target: half and half</td><td>400</td><td>−7 ± 34</td></tr>
        </tbody>
      </table>
      <p class="note">Equal positions: both sides search 20,000 positions per move. Ranges are 95% confidence. Every result is reproducible with the commands in the write-up.</p>
    </details>
  </main>

  <footer class="foot">
    <span>Unofficial fan project, not affiliated with the publishers of Azul.</span>
    <a href="/">Analysis board</a>
    <a href="https://github.com/tusk80/azul-engine" rel="noopener">Source on GitHub</a>
  </footer>
</body>
</html>
"""
open(OUT, "w", encoding="utf-8", newline="\n").write(html)
print("wrote", OUT)
