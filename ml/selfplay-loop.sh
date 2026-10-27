#!/usr/bin/env bash
# Self-improvement loop. Each generation the current network plays itself, a
# new network is trained on the last two generations' games, and it plays its
# parent and the classic engine at equal time. It takes over only if it beats
# its parent. One row per generation goes to ml/generations.csv.
#
#   bash ml/selfplay-loop.sh [generations] [games per generation]
#
# Settings from the environment: EXPLORE (share of random moves in the games,
# default 0.05), FINETUNE=1 (start each network from its parent's weights
# instead of random ones), LR (training step size, default 1e-3, or 3e-4 when
# fine-tuning) and PREV_DATA (the games the first generation trains on next to
# its own, default ml/data.csv).
set -euo pipefail
cd "$(dirname "$0")/.."
gens=${1:-4}
games=${2:-15000}
azul=./azul.exe
[ -x "$azul" ] || azul=./azul

log=ml/generations.csv
[ -f "$log" ] || echo "gen,parent,vs_parent_elo,vs_parent_err,vs_classic_elo,vs_classic_err,kept" > "$log"
# Generation 0 is the shipped network, trained on the classic engine's games.
[ -f ml/net-gen0.json ] || cp internal/nn/default-net.json ml/net-gen0.json
parent=$(awk -F, 'NR > 1 && $7 == "yes" { p = "ml/net-gen" $1 ".json" } END { print p ? p : "ml/net-gen0.json" }' "$log")
first=$(wc -l < "$log")
explore=${EXPLORE:-0.05}
init=()
lr=${LR:-1e-3}
if [ -n "${FINETUNE:-}" ]; then lr=${LR:-3e-4}; fi
prev_data=${PREV_DATA:-ml/data.csv}
if [ "$first" -gt 1 ]; then prev_data=ml/data-gen$((first - 1)).csv; fi

# elo pulls "+64,24" out of a selfplay summary.
elo() { sed -nE 's/^final: .*elo ([+-]?[0-9]+) ±([0-9]+).*/\1,\2/p'; }

for ((g = first; g < first + gens; g++)); do
  echo "== generation $g (parent $parent)"
  data=ml/data-gen$g.csv
  "$azul" gendata -games "$games" -net "$parent" -explore "$explore" -seed $((2000000 + g * 100000)) -out "$data"
  if [ -n "${FINETUNE:-}" ]; then init=(-init "$parent"); fi
  "$azul" train -data "$prev_data,$data" -h1 64 -h2 16 -lr "$lr" "${init[@]}" -out "ml/net-gen$g.json" | tail -2
  vp=$("$azul" selfplay -a-net "ml/net-gen$g.json" -b-net "$parent" -pairs 400 -time 40ms -seed $((5000000 + g * 1000)) | elo)
  vc=$("$azul" selfplay -a-net "ml/net-gen$g.json" -b default -pairs 400 -time 40ms -seed $((6000000 + g * 1000)) | elo)
  kept=no
  if (( ${vp%%,*} > 0 )); then kept=yes; fi
  echo "$g,$parent,$vp,$vc,$kept" | tee -a "$log"
  if [ "$kept" = yes ]; then parent=ml/net-gen$g.json; fi
  prev_data=$data
done
