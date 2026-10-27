"""Builds the dev log: one page per post in this folder plus an index.

Each post is an HTML fragment whose first lines are a comment with title,
date, summary and tags. Pages go to internal/server/ui (blog.html and
blog/<name>.html), so the engine binary and the static site both serve them.

    python blog/build.py
"""
import html
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
    posts.sort(key=lambda p: p["date"], reverse=True)

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

    # Unknown paths fall back to index.html, so search engines need the list.
    newest = posts[0]["date"]
    pages = [("", newest), ("compare.html", newest), ("blog.html", newest)]
    pages += [(f"blog/{p['name']}.html", p["date"]) for p in posts]
    urls = "".join(f"  <url><loc>{SITE}/{u}</loc><lastmod>{d}</lastmod></url>\n" for u, d in pages)
    (UI / "sitemap.xml").write_text(
        f'<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n{urls}</urlset>\n',
        encoding="utf-8", newline="\n")
    print(f"built {len(posts)} posts")


if __name__ == "__main__":
    main()
