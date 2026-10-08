#!/usr/bin/env bash
# Builds the static site: the analysis board plus the engine compiled to
# WebAssembly. The result needs no server, only a static host.
#
#   bash scripts/build-site.sh [output-dir]     (default: site)
set -euo pipefail
cd "$(dirname "$0")/.."

out=${1:-site}
rm -rf "$out"
mkdir -p "$out"

cp server/ui/* "$out"/
# Tell the page its engine is the WebAssembly one, so it does not look for a server.
sed -i 's/<html lang="en">/<html lang="en" data-engine="wasm">/' "$out/index.html"
grep -q 'data-engine="wasm"' "$out/index.html"
cp pages/_headers "$out"/
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$out/engine.wasm" ./cmd/wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out"/

echo "Built $out:"
ls -l "$out" | awk 'NR>1 {printf "  %8d  %s\n", $5, $NF}'
