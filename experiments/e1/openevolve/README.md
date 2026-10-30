# E1 arm C: OpenEvolve on tumble's evaluator

`evaluator.py` is OpenEvolve's evaluator for arm C (#24). It calls
`tumble score`, so a candidate is scored by the same giri evaluator, selection
vectors, correctness rule (outputs and post-call arguments) and abstract cost
as arms A, B and D. Imports are limited to the kernel grammar's call
whitelist, which is `math/bits` for popcount.

- Initial program: `kernels/popcount/popcount.go`, the reference.
- Run it on the E1 host (castor) through pueue's `measured` group, with the
  same wall clock as the other arms (#26), using the openevolve-bedrock
  session's `oe-bedrock run`. Its ledger supplies the model id, the token
  spend and the timestamps that the record needs (SETUP.md).
- The best program's native ns/op comes from tumble's native stage
  (`native.RunSource` with the program's source), the same as for the other
  arms.
