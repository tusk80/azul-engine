# Hosting the analysis board

The whole site is one static binary: the engine, the API and the page. It
keeps no data and needs no database.

## What `-public` does

`azul serve -public` is the same server with limits suited to strangers:

| Setting | Local default | `-public` | Why |
|---|---|---|---|
| `-max-time` | 30s | 2s | Caps how long one request can hold an engine. |
| `-engines` | 1 | half the CPUs | Several visitors can search at once. |
| `-hash` | 256 MB | 32 MB per engine | Memory = engines × hash. |
| `-rate` | unlimited | 40 searches/min per visitor | Other requests get 10× that. |
| `-cors` | `*` | none | Only the site's own page may call the API. |

When every engine is busy a request waits up to 5 s, then gets `503` with
`Retry-After`. Over the rate limit it gets `429`. A search stops as soon as its
visitor disconnects. Any flag you pass yourself overrides the preset.

Rough sizing: each search uses one CPU core for up to `-max-time`. A 2-core
server with `-engines 1` and 2 s searches handles about 30 searches a minute.

## Rate limits behind a proxy: `-ip-header`

Behind a reverse proxy every connection comes from the proxy, so the engine
needs the visitor's address from a header. It reads exactly one header, the one
you name, and trusts it completely, so it must be a header **your proxy
overwrites**.

`deploy/Caddyfile` does this for you: Caddy sets `X-Client-IP` on every
request, from `CF-Connecting-IP` when the request really comes from
Cloudflare and from the connection otherwise. It works whether the DNS record
is proxied by Cloudflare or not, and connecting to the server directly cannot
forge it. The service runs with `-ip-header X-Client-IP`.

With another proxy, name the header it sets (nginx: `X-Forwarded-For`, whose
last entry is used). With no proxy, leave the flag out. Without it the server
still works, but every visitor shares one limit.

## Linux server with systemd and Caddy

Build on any machine (Go cross-compiles):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/azul-linux-amd64 ./cmd/azul
```

On the server:

```bash
sudo mkdir -p /opt/azul
sudo cp azul-linux-amd64 /opt/azul/azul && sudo chmod 755 /opt/azul/azul
sudo cp deploy/azul.service /etc/systemd/system/azul.service
sudo systemctl daemon-reload
sudo systemctl enable --now azul
curl -s http://127.0.0.1:8080/health    # {"status":"ok"}
```

Then point the reverse proxy at `127.0.0.1:8080`. With Caddy, add the block in
`deploy/Caddyfile` (with your domain) to `/etc/caddy/Caddyfile`, check it with
`caddy validate --config /etc/caddy/Caddyfile`, and
`sudo systemctl reload caddy`. Add a DNS record for the subdomain pointing at
the server.

To update: copy the new binary over `/opt/azul/azul` and
`sudo systemctl restart azul`. Logs: `journalctl -u azul -f`.

## Docker

```bash
docker build -t azul -f deploy/Dockerfile .
docker run -d --name azul --restart unless-stopped -p 127.0.0.1:8080:8080 azul
```

Append flags after the image name to override the defaults, for example
`azul serve -public -addr 0.0.0.0:8080 -ip-header X-Forwarded-For`.

## The overlay and a hosted engine

The userscript talks to `127.0.0.1` by default and that is the fast, private
option. To let it use a hosted engine instead, start the server with
`-cors https://buddyboardgames.com` and change `ENGINE_URL` and the `@connect`
line in the script.
