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
  `CheckExperiment` (#52).
- `pkg/pop`: lockstep islands with a deterministic barrier (#54), lexicase
  selection, the MAP-Elites archive, ring migration, run parameter defaults
  (#53), and per-generation readings with the trace-to-variable mapping for
  indexing (#55).
- `pkg/native`: the native benchmark stage: the genome in a copy of the
  kernel, the kernel's tests as a gate, `go test -bench -count=10`, and
  ns/op with a 95% interval from `golang.org/x/perf/benchmath`, plus the
  `benchstat` table (#56). `benchstat` is a tool dependency.
- `pkg/report` and `cmd/tumble`: `tumble run` writes the run record
  (hardware, versions, parameters, per-generation readings and archive
  snapshots, native results, best genome; #57); `tumble report` and
  `tumble inspect` read it back. `tumble run` refuses a non-instrument
  evaluator for an output directory under `experiments/`.
- `kernels`: the kernel registry; `experiments/e0/config.json`.
- `pkg/eval`: the giri evaluator on giri's `pkg/exec` (#62): outcome and
  trace mapping, the concreteness check on the grammar's calls, and the
  linked giri version in run records. Also the value cache (#63), whose
  readings (`vc_hit_rate`, `vc_hits`, `vc_pure_executed`, `vc_entries`) are
  now recorded per generation. `make determinism` also runs the giri
  evaluator.
- `timing.jsonl` records the host's 1-minute load average per generation, and
  `tumble report` prints the maximum (#72).
- `timing.jsonl` records each generation's wall time by phase (evaluate,
  build, interpret, index, breed, native), and the readings record the
  generation's interpreted steps (#21).
- `tumble report` prints the run's E0 summary: operator yields, the FP
  collision rate, the cost-model fit (Spearman, abstract cost against
  native ns/op per benchmark), and seconds and ns/instruction per
  generation. `make e0` runs E0 into `experiments/e0/<timestamp>/` (#19, #20).
- `kernels/headroom_test.go`: hand-written popcount genomes inside the v0
  grammar, unrolled by 2 and by 4, are correct and cost 5,860 and 5,375
  against the reference's 8,237. The GA has not found them (#77).
- Per-generation near-miss readings (`near_miss`, `near_miss_cheaper`,
  `near_miss_below_best`), the `seed_islands` parameter, and
  `experiments/probe77/` configs for #77's search-power probes.
- `kernels/argmax`: reference (first-max index, −1 for empty), tests,
  benchmark, fuzz target and corpus, 60 selection and 16 holdout vectors, and
  `grammar.Argmax`, registered as kernel `argmax` (#83). Kernels may import
  only the standard library, which a test now enforces.
- `ops.Duplicate`, gene duplication: a blind mutation that copies a range of
  1–4 statements to directly after itself, with fresh locals, then repairs.
  The `duplication` parameter (default 0) is its probability; at 0 a run is
  unchanged. It is for arm D (#81, #82).
- `make determinism` and a CI `determinism` job: 5 generations of `popcount`
  twice with seed 1, diffing the archives, readings, and best genome; CI
  installs giri v0.97.0 so the stub and CLI tests run (#58).

### Fixed

- The native stage ran `go test` with its default vet checks, which reject
  legal Go that evolution produces as neutral material (`x || x`): 11 of 153
  genomes in E0 run 20261029T165651Z were reported unverified for it. The
  stage now runs `go test -vet=off`.
- Run records under `experiments/` are their own Go module, so a recorded
  genome (`best.go`, which keeps unreachable code by design) is not vetted
  or linted as tumble code. CI had failed since the E0 record was committed.
- A native result whose interval is unbounded (too few samples for the
  confidence) made the run record unwritable (JSON has no infinity). It is now
  recorded with `no_interval` and left out of the cost-model fit.
- `tumble report` shows an operator yield over zero attempts as `n/m`, not
  as a rate of 0 (#59).
- Loop accumulators were fingerprinted by the loop-header phi attributed to
  their definition's line, so a fragment like `v0 := 0` was filed under the
  loop's per-iteration values. Fingerprints now come from evaluating the
  fragment (#67).

- The type checker finds the standard library in a binary built with
  `-trimpath` (as `make build` does), which does not know its GOROOT.

### Changed

- `PROJECT.md` §8: E1 runs arms A, B and C as specified, plus arm D (A with
  gene duplication, frozen on `argmax` first), labelled separately (#81).
- `PROJECT.md` §1: the design claim restated as hypotheses H1–H4 with what
  tests each and its measured status; §8 records E0's outcome (#80).
- Selection breaks abstract-cost ties on statements above `parsimony_above`
  (default 24), before novelty, in lexicase and in the rank: bounded
  parsimony (#76), replacing the full parsimony of #75. `PROJECT.md` §4.5.
- A genome is correct on a vector only if it returns the reference's values
  and leaves its slice arguments as the reference does. Both evaluators report
  post-call argument contents (`eval.Result.ArgsAfter`). `PROJECT.md` §4.3
  (#74).
- giri pinned at the v0.99.0 release (44e4c41) in `go.mod` and CI:
  `exec.Result.ArgsAfter` (scttfrdmn/giri#267). The code is the same as
  4948ac0, which was pinned briefly before the tag.
- Native elites: one genome per abstract cost not benchmarked before, the
  cheapest plus an even spread over the cost range, recorded as
  `elite_costs`. `PROJECT.md` §4.3 (#71).
- Fragment indexing runs concurrently, Islands × Workers at a time, and is
  bounded by `index_budget` (default 4096) new fragments per generation
  (#72).
- `PROJECT.md` §9: the budget model rewritten from E0's recorded timing, with
  the original estimate's errors stated (#60).
- Fragments, fingerprints, novelty, and equivalence substitution work at
  top-level program points, including an accumulator's value after its loop.
  A fragment's fingerprint is its output on each test vector when evaluated
  as a function. Genome evaluation no longer records traces. `PROJECT.md`
  §4.2 and §5.3 (#67).
- tumble depends on `github.com/scttfrdmn/giri` and builds only with
  `GOEXPERIMENT=arenas`. The `giriapi` build tag is gone (#62).
- `PROJECT.md` §5.2: the value cache as built is per instruction (#63, #64).
- giri pinned at the v0.98.0 release (04128bd), in `go.mod` and CI. Its only
  code change since 5ca7f1f is the CLI cache key (scttfrdmn/giri#265), and
  the stub runs giri with `-no-cache`. Before that, 5ca7f1f: the CLI now reads stored
  slice, array and struct values (scttfrdmn/giri#264), so `eval.stub`'s
  violations and step counts follow native control flow. The stub still takes
  outputs natively, since the CLI exposes none (#51, PROJECT.md §6.2). Before
  that, 28d4253: `math/bits` widths in the CLI
  (scttfrdmn/giri#261), and deferred named results, `recover` of run-time
  panics and interface comparison with nil (#262). Neither changes a v0
  genome. Before that, 0d1c31f (concrete-mode review fixes, #258), and before
  that:
- CI pins giri at 0e7b1ae (main: `pkg/exec`, scttfrdmn/giri#251, plus the
  value-cache GC-safepoint fix, scttfrdmn/giri#254), up from v0.97.0 (#58).
- `PROJECT.md` §6.2 and §7, `SETUP.md`: how the stub evaluates, and its
  exception to "no native execution inside selection" (#51).
- `PROJECT.md` §4.1: expressions are one level over atoms, and `convert` is an
  expression form (#39).

- `GIRI_REQUIREMENTS.md`: requirement 11, value-faithful memory
  (scttfrdmn/giri#248), and requirement 12, slice argument contents after the
  call (scttfrdmn/giri#266, #73).
- `make fuzz-seed` copies the fuzzer's finds from `$GOCACHE/fuzz` into
  `testdata/corpus/`.
