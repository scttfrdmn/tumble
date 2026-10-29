# tumble

A strict genetic algorithm over Go kernels.

AlphaEvolve-style systems are called genetic but put an LLM prior in the variation
operator. tumble asks the complementary question: what does a GA that is strict in
Goldberg's sense — coded representation, population, blind payoff-only operators,
probabilistic transitions — achieve on the same problem class, when the language is
Go and the evaluator is [giri](https://github.com/scttfrdmn/giri), a UB-verifying
SSA interpreter?

Three hash structures over the population, fed by giri's execution traces, carry
the design: structural hash-consing of SSA, a Hashlife-style value cache inside the
interpreter, and a semantic fingerprint index that gives a blind, payoff-only
semantic crossover.

The output of the project is a number: best verified `ns/op` on a fixed kernel
under a fixed wall-clock budget, for tumble, for tumble without the semantic index,
and for OpenEvolve on the same evaluator, with `benchstat` intervals.

## Status

Pre-release (0.x). Nothing runs yet. Work is tracked on GitHub: issues,
milestones, and the project board.

## Documents

- [`PROJECT.md`](PROJECT.md) — the design
- [`SETUP.md`](SETUP.md) — repository conventions, build and measurement discipline, standing decisions
- [`GIRI_REQUIREMENTS.md`](GIRI_REQUIREMENTS.md) — what tumble needs from giri

## Build

```
make            # vet, build, unit tests
make lint       # golangci-lint with the repo config
```

The Makefile exports `GOEXPERIMENT=arenas`, which giri's build needs.

## License

Apache License 2.0. Copyright 2026 Scott Friedman.
