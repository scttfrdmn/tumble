#!/bin/bash -l
# E1 arm C on pollux: run.sh <output dir>. OpenEvolve with tumble score as
# its fitness, 4 h wall clock (SIGTERM is OpenEvolve's graceful shutdown),
# $50 Bedrock cap. Expects /tmp/tumble-e1/tumble at E1's commit (64f009b)
# with bin/tumble built, and the openevolve-bedrock venv in /tmp/tumble-e1/armc.
set -e
here=/tmp/tumble-e1/armc
cd "$here"
. .venv/bin/activate
export PATH=$HOME/sdk/go1.27.3/bin:$PATH GOEXPERIMENT=arenas GOTOOLCHAIN=local
export AWS_PROFILE=aws TUMBLE_BIN=/tmp/tumble-e1/tumble/bin/tumble TUMBLE_KERNEL=popcount
date -u +%Y-%m-%dT%H:%M:%SZ > "$1.started"
uptime > "$1.load-before"
timeout -s TERM -k 15m 4h oe-bedrock run initial_program.go evaluator.py \
  --config config.yaml --output "$1" --budget-usd 50 --pricing pricing.yaml || echo "exit $?" > "$1.exit"
date -u +%Y-%m-%dT%H:%M:%SZ > "$1.ended"
