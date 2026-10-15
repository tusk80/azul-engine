#!/usr/bin/env bash
# One screen of self-play loop status: finished generations, what is running
# now with its latest progress, and the machine's load. Refresh it with:
#
#   watch -n 10 bash ml/status.sh
cd "$(dirname "$0")/.."

echo "Azul self-play loop · $(date '+%H:%M:%S')"
echo
if [ -f ml/generations.csv ]; then
  column -s, -t ml/generations.csv
else
  echo "No generation finished yet."
fi
echo

gen=$(grep -o 'generation [0-9]*' ml/loop.log 2>/dev/null | tail -1)
job=$(pgrep -a azul | grep -v pgrep | head -1 | cut -d' ' -f2-)
case "$job" in
  *gendata*) echo "Now: $gen, playing games"; grep '^progress' ml/loop.log | tail -1 ;;
  *train*) echo "Now: $gen, training the network" ;;
  *"-b-net"*) echo "Now: $gen, match against its parent (800 games)" ;;
  *selfplay*) echo "Now: $gen, match against the classic engine (800 games)" ;;
  *) echo "Now: idle, the loop has finished or stopped" ;;
esac
echo
echo "Load $(cut -d' ' -f1-3 /proc/loadavg) on $(nproc) cores · memory $(free -h | awk '/Mem/ {print $3 " of " $2}')"
