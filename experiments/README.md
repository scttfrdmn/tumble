# experiments

Run configs and recorded results for E0–E4 (`PROJECT.md` §8). Each run writes
`experiments/<name>/<timestamp>/` with the record `SETUP.md` ("Measurement
discipline") requires; the commit that adds a result changes no code.

`tumble run` refuses an output directory under `experiments/` unless the
evaluator is an instrument, so nothing here can come from `eval.stub`.

- `e0/config.json`: E0, instrument validation on `popcount`, 50 generations,
  `PROJECT.md` §4.5 defaults, on the `giri` evaluator.
