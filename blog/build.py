"""Builds the dev log: one page per post in this folder plus an index.

Each post is an HTML fragment whose first lines are a comment with title,
date, summary and tags. Pages go to internal/server/ui (blog.html and
blog/<name>.html), so the engine binary and the static site both serve them.

    python blog/build.py
"""
import html
import json
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parent.parent
UI = ROOT / "internal" / "server" / "ui"
SITE = "https://azul.yesil.cc"
MONTHS = "January February March April May June July August September October November December".split()


def page(title, description, url, body, up=""):
    """A full page; up is the path back to the site root ("" or "../")."""
    e = html.escape
    return f"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{e(title)}</title>
  <meta name="description" content="{e(description)}">
  <meta name="theme-color" content="#0f131b">
  <meta property="og:type" content="article">
  <meta property="og:title" content="{e(title)}">
  <meta property="og:description" content="{e(description)}">
  <meta property="og:url" content="{SITE}/{url}">
  <link rel="canonical" href="{SITE}/{url}">
  <meta property="og:image" content="{SITE}/og.png">
  <meta name="twitter:card" content="summary_large_image">
  <link rel="icon" href="{up}icon.svg" type="image/svg+xml">
  <link rel="stylesheet" href="{up}style.css">
  <script src="{up}theme.js"></script>
  <script src="{up}compare.js" defer></script>
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
        <span class="brand-sub">Dev log</span>
      </span>
    </a>
    <nav class="actions">
      <a class="btn" href="{up}blog.html">All posts</a>
      <a class="btn" href="{up}puzzles.html">Puzzles</a>
      <a class="btn" href="{up}compare.html">Neural vs classic</a>
      <a class="btn primary" href="/">Open the board</a>
      <button id="btn-theme" class="icon" title="Switch light / dark" aria-label="Switch light or dark theme">◐</button>
    </nav>
  </header>
  <main class="cmp-main post-main">
{body}
  </main>
  <footer class="foot">
    <span>Unofficial fan project, not affiliated with the publishers of Azul.</span>
    <a href="/">Analysis board</a>
    <a href="https://github.com/tusk80/azul-engine" rel="noopener">Source on GitHub</a>
  </footer>
</body>
</html>
"""


def nice_date(d):
    y, m, day = d.split("-")
    return f"{int(day)} {MONTHS[int(m) - 1]} {y}"


def main():
    posts = []
    for path in sorted((ROOT / "blog").glob("*.html")):
        text = path.read_text(encoding="utf-8")
        m = re.match(r"\s*<!--(.*?)-->\s*(.*)", text, re.S)
        if not m:
            raise SystemExit(f"{path.name}: missing the header comment")
        meta = dict(line.split(":", 1) for line in m.group(1).strip().splitlines())
        meta = {k.strip(): v.strip() for k, v in meta.items()}
        for key in ("title", "date", "summary", "tags"):
            if key not in meta:
                raise SystemExit(f"{path.name}: header needs {key}")
        meta["body"] = m.group(2).strip()
        meta["name"] = path.stem
        posts.append(meta)
    posts.sort(key=lambda p: (p["date"], p["name"]), reverse=True)

    e = html.escape
    out = UI / "blog"
    out.mkdir(exist_ok=True)
    for old in out.glob("*.html"):
        old.unlink()
    for i, p in enumerate(posts):
        newer = posts[i - 1] if i > 0 else None
        older = posts[i + 1] if i + 1 < len(posts) else None
        nav = '<nav class="post-nav">'
        nav += f'<a href="{older["name"]}.html">← {e(older["title"])}</a>' if older else "<span></span>"
        nav += f'<a href="{newer["name"]}.html">{e(newer["title"])} →</a>' if newer else "<span></span>"
        nav += "</nav>"
        tags = "".join(f'<span class="tag">{e(t.strip())}</span>' for t in p["tags"].split(","))
        body = f"""    <article class="post">
      <p class="post-meta"><time datetime="{p['date']}">{nice_date(p['date'])}</time>{tags}</p>
      <h1>{e(p['title'])}</h1>
      <p class="lede">{e(p['summary'])}</p>
      {p['body']}
    </article>
    {nav}"""
        (out / f"{p['name']}.html").write_text(
            page(f"{p['title']} · Azul Engine dev log", p["summary"], f"blog/{p['name']}.html", body, "../"),
            encoding="utf-8", newline="\n")

    items = "\n".join(f"""      <a class="post-card" href="blog/{p['name']}.html">
        <span class="post-meta"><time datetime="{p['date']}">{nice_date(p['date'])}</time>{"".join(f'<span class="tag">{e(t.strip())}</span>' for t in p['tags'].split(','))}</span>
        <h2>{e(p['title'])}</h2>
        <p>{e(p['summary'])}</p>
      </a>""" for p in posts)
    body = f"""    <section class="cmp-hero">
      <p class="eyebrow">Dev log</p>
      <h1>Building a game AI, one experiment at a time.</h1>
      <p class="lede">Notes from building an Azul engine: search algorithms, a machine-learning pipeline from self-play data to a trained neural network, and the performance engineering that makes it play well in real time. Every claim comes with a measurement.</p>
    </section>
    <section class="post-list">
{items}
    </section>"""
    (UI / "blog.html").write_text(
        page("Dev log · Azul Engine", "Notes from building a game AI: search, machine learning and performance engineering in Go.", "blog.html", body),
        encoding="utf-8", newline="\n")

    # The board's home page lists the latest posts under the results teaser.
    cards = "\n".join(f"""        <a class="post-card" href="blog/{p['name']}.html">
          <span class="post-meta"><time datetime="{p['date']}">{nice_date(p['date'])}</time>{"".join(f'<span class="tag">{e(t.strip())}</span>' for t in p['tags'].split(',')[:1])}</span>
          <h3>{e(p['title'])}</h3>
          <p>{e(p['summary'])}</p>
        </a>""" for p in posts[:3])
    block = f"""<!-- devlog: written by blog/build.py -->
      <section class="devlog" aria-label="Dev log">
        <div class="devlog-head"><h2>From the dev log</h2><a class="btn" href="blog.html">All posts</a></div>
        <div class="devlog-grid">
{cards}
        </div>
      </section>
      <!-- /devlog -->"""
    index = UI / "index.html"
    text = index.read_text(encoding="utf-8")
    text, n = re.subn(r"<!-- devlog: written by blog/build.py -->.*?<!-- /devlog -->", lambda _: block, text, flags=re.S)
    if n != 1:
        raise SystemExit("index.html: devlog markers not found")
    index.write_text(text, encoding="utf-8", newline="\n")

    puzzles_page()

    # Unknown paths fall back to index.html, so search engines need the list.
    newest = posts[0]["date"]
    pages = [("", newest), ("compare.html", newest), ("blog.html", newest), ("puzzles.html", newest)]
    pages += [(f"blog/{p['name']}.html", p["date"]) for p in posts]
    urls = "".join(f"  <url><loc>{SITE}/{u}</loc><lastmod>{d}</lastmod></url>\n" for u, d in pages)
    (UI / "sitemap.xml").write_text(
        f'<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n{urls}</urlset>\n',
        encoding="utf-8", newline="\n")
    print(f"built {len(posts)} posts")


def puzzles_page():
    """puzzles.html: a crawlable page for the board's puzzle mode."""
    puzzles = json.loads((UI / "puzzles.json").read_text(encoding="utf-8"))
    n = len(puzzles)
    wins = sum(1 for p in puzzles if p.get("wins"))
    floor = sum(1 for p in puzzles if p["best"].endswith("floor"))
    cards = []
    for i, p in enumerate(puzzles[:12]):
        st = p["state"]
        me, them = st["players"][st["toMove"]]["score"], st["players"][1 - st["toMove"]]["score"]
        stake = "One move wins the game" if p.get("wins") else f"The best move is worth {p['gain']:.1f} points more"
        cards.append(f"""      <a class="post-card" href="/#puzzle={i + 1}">
        <span class="post-meta"><span class="tag">Round {st['round']}</span><span class="tag">Player {st['toMove'] + 1} to move</span></span>
        <h2>Puzzle {i + 1}</h2>
        <p>{me} to {them} on the board. {stake}. Find it.</p>
      </a>""")
    items = "\n".join(cards)
    body = f"""    <section class="cmp-hero">
      <p class="eyebrow">Puzzles</p>
      <h1>Azul puzzles: find the best move.</h1>
      <p class="lede">{n} positions from games the engine played against itself. In each one a single move beats every other by at least 3 points, and the engine checked it by searching the rest of the round. Play your answer on the board: the engine's analysis stays hidden until you move, then it shows why.</p>
      <p><a class="btn primary" href="/#puzzle">Today's puzzle</a> <a class="btn" href="/#puzzle=1">Start from puzzle 1</a></p>
      <div class="stats">
        <div class="stat"><span class="num">{n}</span><span class="lbl">puzzles, a new one each day</span></div>
        <div class="stat"><span class="num">{wins}</span><span class="lbl">where one move wins the game</span></div>
        <div class="stat"><span class="num">{floor}</span><span class="lbl">where the best move puts tiles on the floor</span></div>
      </div>
    </section>
    <section class="post-list">
{items}
    </section>
    <article class="post">
      <h2>How the puzzles are made</h2>
      <p>The engine's neural network played 200 games against itself. At every turn it searched the position with two candidate lines. A position became a puzzle when three things held: the search reached the end of the round, so nothing was cut short; the best move scored at least 3 points more than the second best; and the best move was not the one a greedy player would pick, the move that looks best for your own board right now.</p>
      <p>That last rule is what makes them puzzles. The answer is often a quiet move: leaving a colour for the opponent so they have to break a line, taking the first-player token at the right moment, or dropping tiles on the floor now to avoid a bigger penalty later. The code is <code>azul puzzles</code> in the <a href="https://github.com/tusk80/azul-engine" rel="noopener">source on GitHub</a>.</p>
    </article>"""
    (UI / "puzzles.html").write_text(
        page("Azul puzzles: find the best move · Azul Engine",
             f"{n} Azul puzzles from engine self-play: find the one move that beats every other by at least 3 points. Free, in your browser.",
             "puzzles.html", body),
        encoding="utf-8", newline="\n")


if __name__ == "__main__":
    main()
