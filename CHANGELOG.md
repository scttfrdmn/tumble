# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Repository scaffold: module `github.com/scttfrdmn/tumble`, Apache 2.0 licence,
  Makefile exporting `GOEXPERIMENT=arenas`, golangci-lint config, CI, package
  layout per `PROJECT.md` §12, and a `tumble` CLI stub.
- `kernels/popcount`: reference `Popcount`, correctness test, benchmark at
  16/1024/65536 words, fuzz target against a naive bit loop, committed fuzz
  corpus, and `Vectors()` returning 50 selection and 15 holdout test vectors
  (#38).
- `pkg/tir`: the typed linear IR (statements assign/store/loop/if/return
  plus the repair sink, one-level expressions over atoms), a gofmt-stable
  printer with canonical names, a parser that normalizes plain Go seeds, the
  structural pre-check `Validate`, the go/types check, phenotype identity, and
  the α-renaming structural hash (#39, #40, #41, #42).
- `pkg/grammar`: the run grammar (forms, limits, types, operators, call
  whitelist, constant ranges) with the v0 popcount grammar (#43), scope
  tracking, and the random genome generator with ramped initialization (#44).
- `pkg/tir`: `Sink` pass for unused locals; 10,000-genome property test
  (type-check, print-parse-print fixpoint, hash stability).
- `pkg/ops`: repair (#45), two-point crossover (#46), point mutation (#47),
  equivalence substitution against a `Donor` index, a no-op while the index
  is empty (#48), and per-operator yield counters.
- `pkg/eval`: the `Evaluator` interface and its input, trace, and result
  types (`SETUP.md`, "Evaluator interface").
- `pkg/index`: hash-cons table, fingerprints from traces (#49), backward-slice
  fragments of top-level definitions (#50), the fingerprint index sorted by
  cost, novelty, the `ops.Donor` lookup, and a collision checker.
- `pkg/eval`: `eval.stub` (violations and steps from the giri CLI, outputs
  from a native fuel-limited harness; #51), the evaluator registry and
  `CheckExperiment` (#52), and the giri adapter skeleton behind
  `//go:build giriapi`.
- `pkg/pop`: lockstep islands with a deterministic barrier (#54), lexicase
  selection, the MAP-Elites archive, ring migration, run parameter defaults
  (#53), and per-generation readings with the trace-to-variable mapping for
  indexing (#55).
- `pkg/native`: the native benchmark stage: the genome in a copy of the
  kernel, the kernel's tests as a gate, `go test -bench -count=10`, and
  ns/op with a 95% interval from `golang.org/x/perf/benchmath`, plus the
  `benchstat` table (#56). `benchstat` is a tool dependency.

### Changed

- `PROJECT.md` §6.2 and §7, `SETUP.md`: how the stub evaluates, and its
  exception to "no native execution inside selection" (#51).
- `PROJECT.md` §4.1: expressions are one level over atoms, and `convert` is an
  expression form (#39).

- `GIRI_REQUIREMENTS.md`: requirement 11, value-faithful memory
  (scttfrdmn/giri#248).
- `make fuzz-seed` copies the fuzzer's finds from `$GOCACHE/fuzz` into
  `testdata/corpus/`.
