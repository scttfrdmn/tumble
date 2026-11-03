# E1 arm C: OpenEvolve on tumble's evaluator

`evaluator.py` is OpenEvolve's evaluator for arm C (#24). It calls
`tumble score`, so a candidate is scored by the same giri evaluator, selection
vectors, correctness rule (outputs and post-call arguments) and abstract cost
as arms A, B and D. Imports are limited to the kernel grammar's call
whitelist, which is `math/bits` for popcount.

- Initial program: `kernels/popcount/popcount.go`, the reference.
- Run it on the E1 host (pollux) through pueue's `measured` group, with the
  same wall clock as the other arms (#26), using the openevolve-bedrock
  session's `oe-bedrock run`. Its ledger supplies the model id, the token
  spend and the timestamps that the record needs (SETUP.md).
- The best program's native ns/op comes from tumble's native stage
  (`native.RunSource` with the program's source), the same as for the other
  arms.

Files for the run:
- `config.yaml`: OpenEvolve 0.4.1 settings. The models, us. Claude Haiku 5.5
  (weight 0.8) and Sonnet 5.5 (0.2), and the $50 cap are Scott's choice
  (2026-11-03). Settings not listed are OpenEvolve's defaults.
- `pricing.yaml`: us-west-2 on-demand prices from the AWS Price List API,
  for the ledger and the cap.
- `run.sh`: the pueue task. It runs `oe-bedrock run` (openevolve-bedrock
  7315503) under a 4 h `timeout`, whose SIGTERM is OpenEvolve's graceful
  shutdown, scored by tumble 64f009b (giri v0.99.1), E1's commit.

The initial program is the reference as written, whose `range` loop costs
7,570. tumble's arms start from the same reference parsed into the IR, which
has no `range` form, so their seed costs 8,237. Costs are compared as absolute
abstract cost.
