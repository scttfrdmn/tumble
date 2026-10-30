# Copyright 2026 Scott Friedman
# SPDX-License-Identifier: Apache-2.0
"""OpenEvolve evaluator for tumble's E1 arm C (scttfrdmn/tumble#24).

Scores a candidate with `tumble score`, which uses the same giri evaluator,
selection vectors, correctness rule and abstract cost as tumble's own arms.
OpenEvolve maximises combined_score: the fraction of selection vectors
correct while any is wrong, and 1 + reference_cost/cost once all are correct.

Environment:
  TUMBLE_BIN     the tumble binary (default: tumble on PATH)
  TUMBLE_KERNEL  the kernel (default: popcount)
  TUMBLE_TIMEOUT seconds per candidate (default: 300)
"""

import json
import os
import subprocess

TUMBLE = os.environ.get("TUMBLE_BIN", "tumble")
KERNEL = os.environ.get("TUMBLE_KERNEL", "popcount")
TIMEOUT = float(os.environ.get("TUMBLE_TIMEOUT", "300"))


def evaluate(program_path):
    env = dict(os.environ, GOEXPERIMENT="arenas")
    try:
        out = subprocess.run(
            [TUMBLE, "score", "--kernel", KERNEL, "--program", program_path],
            capture_output=True, text=True, timeout=TIMEOUT, env=env,
        )
    except subprocess.TimeoutExpired:
        return {"combined_score": 0.0, "scored": 0.0}
    if out.returncode != 0:
        return {"combined_score": 0.0, "scored": 0.0}
    s = json.loads(out.stdout)
    metrics = {k: float(v) for k, v in s.items() if isinstance(v, (int, float)) and not isinstance(v, bool)}
    metrics["scored"] = 0.0 if s.get("error") else 1.0
    return metrics


if __name__ == "__main__":
    import sys
    print(json.dumps(evaluate(sys.argv[1])))
