#!/bin/bash -l
# run.sh <variant> <round>
cd /tmp/tumble-perf92/$1
export GOEXPERIMENT=arenas GOTOOLCHAIN=go1.27.3
out=/tmp/tumble-perf92/out/$1-$2
uptime > $out.load-before
exec /usr/bin/time -v -o $out.time ./bin/tumble run --kernel popcount --config /tmp/tumble-perf92/config.json --out $out
