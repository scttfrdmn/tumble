# tumble — a strict genetic algorithm over Go

*Project document, v0.1 — October 2026*
*Copyright 2026 Scott Friedman. Apache License 2.0.*

---

## 0. Name

**tumble.** A rock tumbler takes rough stones, runs a cheap blind abrasion for a long time, and produces polished stones. Nothing in the drum knows what a good stone looks like; selection does the work. The name sits with lith, strata, varve, and agate — and agate is what comes out of a tumbler.

Module path: `github.com/scttfrdmn/tumble`. Alternates if the name is taken or doesn't wear well: `knap` (flintknapping — blind percussive shaping), `scree`.

## 1. Thesis

AlphaEvolve, OpenEvolve, and their descendants are called genetic but fail three of Goldberg's four defining criteria (1989, ch. 1): they do not search over a coding, their operators are not blind, and they are not payoff-only. The variation operator is an LLM prior; the population is a restart policy for it. The published ablations show the population contributes measurably but modestly; the prior does most of the work.

tumble asks the complementary question: what does a *strict* GA — coded representation, population, blind payoff-only operators, probabilistic transitions — achieve on the same problem class, when the language is Go and the evaluator is a UB-verifying SSA interpreter?

The design claim is that three hash structures over the population, fed by execution traces from [giri](https://github.com/scttfrdmn/giri), close the gaps that historically kept blind program search small:

1. **Structural hash-consing of SSA** across the population removes redundant evaluation.
2. **A Hashlife-style value cache** inside the interpreter makes per-generation cost proportional to *novel* code, not total code.
3. **A semantic fingerprint index** (hash of each SSA value's trace across the test vectors → fragments computing it, with their instruction cost) gives a blind, payoff-only *semantic* crossover: substitute a fragment with any cheaper fragment of identical fingerprint. Linkage is observed from execution rather than learned statistically (BOA) or imported (LLM).

Each piece has prior art (Krawiec's behavioral GP, Bansal–Aiken fingerprints, Keijzer/Handley subtree caching, STOKE's cost model, Moraglio's semantic operators). The combination on a real language, with a verifying interpreter, a native benchmark as the terminal fitness, and `unsafe` admitted to the grammar because the interpreter checks pointer rules, does not exist.

Read from the other side, tumble is a population-based superoptimizer for Go kernels — the post-SSA high-effort mode, approached from the GA side.

The output of the project is a number: best verified `ns/op` on a fixed kernel under a fixed wall-clock budget, for tumble, for tumble with the semantic index disabled, and for OpenEvolve on the same evaluator, with `benchstat` intervals.

## 2. Constraints (Goldberg-strict)

| Criterion | tumble |
|---|---|
| Works on a coding of the parameters | Genome is a typed linear IR, not the phenotype (native Go) |
| Population-based search | Yes; islands + MAP-Elites archive |
| Payoff-only, no auxiliary knowledge in the operators | Operators see genomes, fingerprints, and costs — all derived from execution on the test vectors. No model, no heuristics about Go. |
| Probabilistic transition rules | Yes |

Design knowledge is allowed in exactly one place: the **grammar** (which statement forms, which stdlib calls, which types). That is the designer's job in Goldberg's framework. The search never sees why those were chosen.

What is explicitly *not* allowed: an LLM anywhere in the loop; hand-written rewrite rules; any operator that inspects Go semantics beyond the type checker and the fingerprint index.

## 3. Problem class and scope

**v0 (this document):** single-function kernels over `[]int`, `[]uint64`, `[]int32`, scalars. Fixed signature per run. Reference implementation supplied. Target: reduce executed-instruction cost (giri) and native `ns/op` (gc-compiled, `testing.B`) while matching the reference on all test vectors and passing giri's UB detectors.

Candidate kernels, in order:
- `popcount(xs []uint64) int`
- `prefixsum(xs []int32, out []int32)`
- `argmax(xs []int32) int`
- `sdot(x, y []float32) float32` — *v1, after float fingerprints exist*

**v1:** float32/float64 with bucketed fingerprints (§6.3).
**v2:** `unsafe` admitted to the grammar; giri's unsafe-pointer rules and shadow memory become a fitness filter.
**v3:** concurrency (goroutines over slice ranges) with giri PCT sweeps as the race filter.

**Out of scope:** whole-codebase edits, multi-function programs, anything where evaluation requires I/O or non-determinism, generics, interfaces, maps as a target data structure.

## 4. Architecture

```
            ┌──────────────┐
            │   grammar    │   designer input: statement forms, type set, call whitelist
            └──────┬───────┘
                   │
   ┌───────────────▼────────────────┐
   │        population (islands)     │   genomes = typed linear IR
   │        MAP-Elites archive       │
   └───────┬───────────────▲────────┘
           │ parents        │ children
   ┌───────▼───────┐  ┌─────┴─────────────────────┐
   │   selection   │  │  operators                 │
   │  lexicase +   │  │  • two-point crossover     │   blind, type-checked, repaired
   │  novelty      │  │  • point mutation          │
   └───────────────┘  │  • equivalence substitution│ ← semantic fingerprint index
                      └──────────┬─────────────────┘
                                 │
                      ┌──────────▼──────────┐
                      │  print IR → Go src  │   one package per generation
                      │  go/types check     │
                      │  go/ssa build       │
                      └──────────┬──────────┘
                                 │
                      ┌──────────▼──────────────────┐
                      │  giri (library mode)         │
                      │  • run each fn on N inputs   │
                      │  • per-value traces          │ → fingerprints
                      │  • instruction count         │ → abstract cost
                      │  • UB violations             │ → reject
                      │  • value cache (hashlife)    │
                      └──────────┬──────────────────┘
                                 │ verified, cheap-cost ranked
                      ┌──────────▼──────────┐
                      │  native stage        │   elites only
                      │  go test -bench      │
                      │  -count=N, benchstat │ → ns/op with interval
                      └─────────────────────┘
```

### 4.1 Genome: typed linear IR (TIR)

A genome is one function with the run's fixed signature, represented as a bounded-length list of statements over a small set of forms:

```
assign    v := expr               (expr: binop, unop, const, index, len, call, convert)
store     xs[i] = v
loop      for i := lo; i < hi; i += step { body }     (structured, bounded nesting)
if        if cond { body } [else { body }]
return    return v
```

Expressions are one level deep: every operand is a variable or a constant, so each statement defines one value from named inputs, and nested Go is flattened into fresh definitions. `convert` is `T(x)` between integer types; without it no genome can combine a `uint64` element with an `int` accumulator (decision #39).

Types: the run's parameter and return types plus `int`, `bool`, and the element types involved. Variables are SSA-like (single definition) except loop-carried accumulators, which are declared before the loop and assigned inside.

Max length, max nesting, and the statement-form set are run parameters. The grammar also lists admitted calls (v0: `math/bits.OnesCount64`, `len`, `min`, `max`; nothing else).

The IR prints deterministically to Go source via `go/printer`; the printed form is canonical (`gofmt`-stable), so `sha256(printed)` is the phenotype identity used for deduplication.

**Why not Go AST directly:** the IR's variables and statement boundaries are the units crossover and the fingerprint index need to address. A general Go AST has too many node kinds with non-local meaning; the IR keeps every statement's effect local, which is what gives a contiguous statement range a real defining length.

**Why not SSA directly:** go/ssa is one-way; nothing prints it back to Go. The IR is near-SSA by construction, so the mapping IR variable ↔ SSA value is recoverable from positions (`ssa.Value.Pos()`), which is how fingerprints get attached to IR variables.

### 4.2 Operators

All operators run against the type checker and a repair pass. A child that cannot be repaired is discarded (counted; see §8 metrics).

- **Two-point crossover** on the statement list, cut points restricted to statement boundaries at equal nesting depth, followed by variable-renaming repair (dangling uses are rebound to the nearest type-compatible definition in scope or the statement is dropped). This is the material-transfer operator that keeps tumble genetic in the inheritance sense.
- **Point mutation:** change an operator, operand, constant (small integer perturbation or re-draw), loop bound, or call; insert or delete a statement. Per-statement rate ≈ 1/L.
- **Equivalence substitution** (the semantic operator): pick a top-level program point in the child, meaning the value of a variable `v` after a top-level definition, or after a top-level loop or `if` that reassigns it. Look up the fingerprint of `v`'s backward slice at that point in the index. If a fragment with the same fingerprint and lower cost exists (from any individual), insert it, make later reads of `v` read its result, and drop the old slice as dead. Fragments at top-level points are closed over the function's parameters, which are identical across the population, so no free-variable mapping is needed. Values inside loop bodies would need one, and are not substitution sites yet (#66). Probability of applying this operator is a run parameter; the ablation arm sets it to zero. (Decision #67.)
- **Dead-code elimination** is *not* an operator — Go rejects unused variables, so the repair pass sinks them (`_ = v`), and selection on cost removes them. Keeping dead code as neutral material is deliberate (Cartesian GP's result on neutral drift).

### 4.3 Fitness cascade

Each stage is a filter; a genome stops at the first failure and its cost is recorded.

1. **Type check** (`go/types`, in-process, microseconds). Failure → discard.
2. **SSA build** — all genomes of the generation printed as functions `g_0001 … g_NNNN` in one package, one `go/ssa` build.
3. **giri interpretation** on the N test vectors under a step budget. Produces: outputs, per-value traces, instruction count by class, violations.
   - Violation or budget exhaustion → discard.
   - Per-test-case correctness vector `c ∈ {0,1}^N` from output equality with the reference.
   - Abstract cost `k` = instructions executed, summed over test vectors, weighted by class (load/store/call/branch weights are run parameters; v0 uses 1 for everything).
4. **Native stage**, elites only (up to E per generation, a run parameter, default 8: among fully correct genomes, one per abstract cost not benchmarked before, the cheapest plus the rest spread evenly over the generation's cost range, so the cost-model fit sees a spread; decision #71): printed to a temp package with the reference's benchmark harness, `go test -bench -count=10`, `benchstat` interval. Native `ns/op` is the terminal fitness and the reported number. It is *not* used for selection within the population, only for archiving and reporting, because its noise would swamp per-generation selection.

Selection uses `c` (lexicase) first, then `k`, then novelty (§4.5). A genome that is correct on all N vectors but not yet natively benchmarked is still eligible as a parent.

### 4.4 Verification beyond the test vectors

Fingerprint equality and `c = 1^N` are statements about the test set, not about equivalence. Three measures:

- **Fuzz corpus as test vectors:** the N inputs are drawn from a `go test -fuzz` corpus seeded against the reference, so they include edge cases (empty, length 1, max values) rather than random fills.
- **Co-evolved inputs (v1):** a second, small population of inputs whose fitness is "produces a divergence between an elite and the reference" (Hillis 1990). When one is found it is promoted into the test vector set; every elite is re-evaluated.
- **Holdout vectors:** M additional inputs never used in selection; reported alongside `ns/op`. A reported kernel is one that passes selection vectors, holdout vectors, and a final 60-second fuzz run against the reference.

giri's UB detectors catch the class of false positives differential testing cannot: out-of-bounds reads that land on valid memory, uninitialized reads that happen to be zero.

### 4.5 Population structure

- **Islands:** I islands (default 8), population P each (default 256), migration of the top m every G generations (defaults 4, 10). Islands run in parallel goroutines; each holds its own `go/ssa` build per generation.
- **MAP-Elites archive**, global, descriptors: `(abstract cost bucket, statement count bucket, max loop depth)`. v1 adds `allocs/op` from the native stage.
- **Novelty:** number of fingerprints a genome produces that are not yet in the index. Used as the third selection key and as a MAP-Elites tiebreaker; gives selection something to grip when correctness is zero across the population (the plateau problem).
- **Lexicase selection** (Spector & Helmuth 2015) over the N test-case correctness bits, then abstract cost, then novelty.

### 4.6 Sandbox

giri's `max_steps` is fuel; `max_goroutines` = 1 in v0–v2. No process boundary is needed for interpretation. The native stage runs in a subprocess with a wall-clock timeout and `GOMEMLIMIT`; only genomes already verified under giri reach it.

The grammar excludes `unsafe` (until v2), `reflect`, `runtime`, `os`, `net`, `syscall`, cgo, `//go:` directives, and any stdlib function giri models as opaque rather than concrete (§7, giri requirement 5).

## 5. The three hash structures

### 5.1 Structural hash-cons (HC)

Every IR fragment (a statement's backward slice to parameters/constants) has a canonical structural hash: hash of the slice's statements in topological order with variables α-renamed by first use. Identical fragments across individuals and generations share one entry. This is global value numbering applied across the population.

Use: deduplication of fragments in the fingerprint index; key for the value cache.

### 5.2 Value cache (VC) — the Hashlife move

A table keyed by `(HC(fragment), hash(input vector))` → fragment's output sequence and instruction count. When giri reaches a value whose fragment and inputs are in the table, it takes the cached result and cost instead of executing.

Since most of every child is inherited from already-evaluated parents, the fraction of a generation's values served from cache should rise quickly. **The cache hit rate per generation is a primary instrument reading** (§8). If it does not exceed ~0.9 after the first few generations, the per-generation cost model in §9 is wrong and the budget has to be re-estimated.

Where it lives: inside giri, as a hook (§7, requirement 3). giri consults the hook before executing each pure instruction. tumble's side (`eval.ValueCache`, decision #63) keys an entry on the instruction's shape (operator, types, callee) and its operand values, and commits new entries at each generation barrier, so the readings are deterministic with islands in parallel.

**As built, the cache is per instruction:** a hit skips that instruction, not the computation of its operands. The Hashlife saving described above, skipping whole inherited fragments, needs a giri hook that can skip a fragment's instructions, which does not exist. Until it does, the hit rate measures reuse of pure operations, and a high reading does not mean the §9 cost model holds. Whether to build fragment-level skipping is open (#64).

### 5.3 Semantic fingerprint index (FP)

Fingerprints are taken at **top-level program points** of each evaluated genome: the value of a variable after a top-level definition, or after a top-level loop or `if` that reassigns it (so an accumulator's value after its loop is one). The fragment at a point is the variable's backward slice there, closed over the function parameters. It is evaluated as a function returning the variable, and its output on each test vector `t` is the value. The fingerprint is

```
fp(v) = hash( type(v) || for t in vectors: hash(value_t(v)) )
```

Equal fingerprints therefore mean equal values on every test vector by construction. The same evaluation gives the fragment's cost. The index maps `fp → [(fragment HC, cost_k, source genome id)]`, kept sorted by cost. (Decision #67. The original definition took every SSA value's per-iteration trace. Values inside loop bodies need free-variable binding to be fragments, and are #66.)

Uses:
- **Equivalence substitution** (§4.2): same `fp`, lower cost.
- **Blind library discovery:** admitted stdlib calls are evaluated on the vectors at run start and their fingerprints seeded into the index with their (interpreted) cost. An evolved loop that computes `bits.OnesCount64`'s fingerprint becomes substitutable by the call. *Not built:* in v0 kernels such a loop sits inside the loop over the input, at a point that needs free-variable binding (#66).
- **Novelty** (§4.5).
- **Execution-defined schemata:** a schema is the set of genomes containing a value with fingerprint F. Frequency of F in generation t+1 is driven by the fitness of genomes containing it and the operators' preservation probability — which equivalence substitution preserves exactly and two-point crossover preserves when the fragment lies inside one segment. This is the schema theorem restated over fingerprints; §8 includes the measurement that tests whether the restated theorem predicts anything.

Collision handling: fingerprints are 128-bit; collisions are detected at substitution time by re-executing the substituted child (which happens anyway). A collision that survives is a correctness failure caught by lexicase.

## 6. Decisions taken (override if you disagree)

1. **Genome is the TIR, not Go AST and not SSA.** Reasons in §4.1.
2. **giri is the only evaluator in the loop.** No separate tumble interpreter; no native execution inside selection. The native stage is reporting and archiving only. *Exception, scaffolding only:* `eval.stub`, which never runs an experiment, takes genome outputs from native execution, because giri does not yet carry element values through memory (scttfrdmn/giri#248) and its CLI exposes no program output (decision #51).
3. **v0 is integer-only.** Float fingerprints need bucketing (round to k ulps before hashing); do that after the integer pipeline produces its first number, not before.
4. **Abstract cost weights are all 1 in v0.** Calibrate against native `ns/op` on the elites once there are enough pairs to fit (the fit is itself a reported result: how well does giri's instruction count predict gc-compiled ns/op on Graviton and on x86?).
5. **No elitism across generations except through the MAP-Elites archive.** Island populations are fully replaced each generation; the archive is where good genomes persist.
6. **Stdlib call surface in v0 is `math/bits` only.** Expanding it is a grammar change, logged as such.
7. **Two baselines, both required before any tumble number is reported:** the hand-written reference compiled by gc, and OpenEvolve on the same reference, same evaluator, same wall clock.
8. **Hardware is recorded with every number:** instance type or machine, Go version, giri version, tumble commit, GOAMD64/GOARM64 level.

## 7. What giri needs

See `GIRI_REQUIREMENTS.md` for the prose and the implementation prompt. Summary: a library entry point that runs one SSA function on supplied Go values and returns outputs, per-value traces, instruction counts by class, and violations; a batch loader for one package per generation; a pre-execution value-cache hook; per-class cost counters; a query for whether a stdlib function is interpreted concretely; a fast path that skips scheduling when `max_goroutines == 1`.

Until those land, tumble builds against an `Evaluator` interface with a stub that shells out to the `giri` CLI. giri's CLI has no instruction dump and no program output, so the stub takes violations from giri's findings, the step count from a search over `-max-steps`, and outputs from a fuel-limited native harness (decision #51). The stub is for scaffolding and tests only; no measurement is taken through it.

## 8. Experiments and instrument readings

Every experiment records: kernel, arm, wall-clock budget, hardware, versions, random seed, and the full run parameter set.

**E0 — instrument validation** (no comparisons yet)
- VC hit rate per generation, over 50 generations, on `popcount`. Expectation: >0.9 by generation 5.
- FP collision rate: fraction of substitutions that produce a child failing a test vector the fragment's donor passed. Expectation: <1e-4.
- Operator yield: fraction of children per operator that survive type check + repair. Expectation: two-point crossover >0.7, mutation >0.8, substitution ≈1.0.
- Cost-model fit: Spearman correlation between giri instruction count and native `ns/op` across all natively benchmarked genomes.

**E1 — first number**, `popcount`, three arms, equal wall clock (default 4 hours on one machine):
- A: tumble, full.
- B: tumble, substitution rate 0 (no semantic index; the structural hash-cons and value cache stay on).
- C: OpenEvolve, same reference, same test vectors, same giri evaluator as its fitness function, same wall clock, recorded model and token spend.
- Report: best verified `ns/op` with `benchstat` 95% interval vs the gc-compiled reference; generations completed; evaluations completed; for A, fraction of fitness improvements attributable to substitution vs crossover vs mutation.

**E2 — `prefixsum`, `argmax`,** same arms.

**E3 — schema measurement.** For the top 20 fingerprints by final-elite frequency: track their population frequency per generation and compare against the restated schema theorem's prediction from their mean fitness and preservation probability. The reading is whether the prediction tracks the observed curve at all; this is the result that says whether "execution-defined linkage" is a theory or a slogan.

**E4 (v1) — `sdot`** with bucketed float fingerprints; the same three arms.

Nothing in this list is declared done by the document; it is the list of numbers the project exists to produce.

## 9. Budget model

A generation is one barrier-synchronised round: all islands evaluate in parallel, then the barrier indexes new fragments, then all islands breed, and the native stage benchmarks that generation's elites. Its wall time is

```
T_gen = T_evaluate + T_index + T_breed + T_native
T_evaluate ≈ max over islands of (build + interpret)
interpret  ≈ (P × instructions per genome × ns per instruction) / workers per island
T_index    ≈ new fragments this generation × (one evaluation each, sequential)
T_native   ≈ newly benchmarked elites × (copy, test, go test -bench -count=10)
```

**Measured (E0 run `experiments/e0/20261029T042951Z`, #20).** popcount; I = 8, P = 256, N = 50 vectors of 1–32 words; 4 workers per island; value cache on; Apple M4 Pro, 12 cores. These are the record's means over 50 generations:

| Term | Mean s/gen | Notes |
|---|---:|---|
| `T_evaluate` | 2.30 | 2,048 genomes, about 16–25 M interpreted instructions per generation; 806 ns per instruction as island wall time (giri's single-threaded figures are in its `docs/perf.md`) |
| `T_index` | 36.1 | grows with the fragment count: 0.9 s at generation 0, 99 s at generation 49 (#69) |
| `T_breed` | 1.40 | |
| `T_native` | 39.1 | E = 8 elites per generation, all tied at the minimum cost (#68) |
| **`T_gen`** | **81.0** | 4,050 s over 50 generations |

At that mean, a 4-hour budget is about 178 generations, or about 364k genome evaluations. The mean is not stationary: `T_index` rises through the run, so later generations cost more. Evaluation and breeding alone are about 3.7 s per generation. That is the floor if indexing (#69) and the native stage (#68) are bounded or moved off the critical path.

**What changed from the original model.** The original estimate (P = 256, N = 64, n = 1024, 5k instructions per vector, 30 ns per instruction, value-cache hit rate 0.9) had three problems:

- *Arithmetic.* It omitted its own ×0.1 hit-rate factor (≈0.25 s, not ≈2.5 s), and it multiplied by eight islands that run concurrently.
- *Inputs.* Vectors are at most 32 words (#38), not 1,024. giri's per-instruction cost is above 20–50 ns with detectors on (giri `docs/perf.md`).
- *The value-cache term.* The `(1 − hit rate)` factor assumed hits skip whole inherited fragments. The cache as built saves one instruction per hit (§5.2, #63, #64), so the hit rate (0.997–1.000 from generation 2 in E0) doesn't scale interpretation cost.

The terms that bound a run are the barrier's fragment indexing and the native stage, not interpretation.

## 10. Risks

- **giri per-instruction cost** may be higher than 50 ns with shadow memory on. Mitigation: requirement 6 (skip shadow memory for a genome already verified once; re-enable for any genome that reaches the native stage).
- **Fingerprints over the test set are not equivalence.** Mitigated by holdout, fuzz, co-evolved inputs; residual risk is reported, not hidden.
- **Ripple:** two-point crossover at equal depth still renames downstream variables. Operator-yield readings in E0 will show whether the repair pass is good enough; if yield <0.5, switch cut points to loop-body boundaries only.
- **Bloat.** Abstract cost as a selection key is the standard counter-pressure; statement-count MAP-Elites descriptor keeps short genomes alive.
- **The grammar is too small to contain the winning kernel.** This is a real outcome, not a failure of the method: it is the measurement of how much of the LLM's contribution is "knowing the search space." Report it as such.

## 11. Prior art

Goldberg 1989; Holland 1975; Koza 1992; Moscato 1989 (memetic); Hillis 1990 (co-evolution); Handley 1994 and Keijzer 2004 (subtree caching); Montana 1995 (strongly typed GP); O'Neill & Ryan 2001 (GE); Brameier & Banzhaf 2007 (linear GP); Miller (CGP); Bansal & Aiken 2006 (fingerprints); Schkufza, Sharif & Aiken 2013 (STOKE); Moraglio, Krawiec & Johnson 2012 (geometric semantic GP); Krawiec 2016 (behavioral program synthesis); Lehman & Stanley 2011 (novelty); Spector & Helmuth 2015 (lexicase); Mouret & Clune 2015 (MAP-Elites); Le Goues et al. 2012 (GenProg); Gosper 1984 (Hashlife); Novikov et al. 2025 (AlphaEvolve); Sharma 2025 (OpenEvolve).

## 12. Repository layout

```
tumble/
├── cmd/tumble/            CLI: run, report, inspect
├── pkg/
│   ├── tir/               typed linear IR, printer, parser (for seeds), canonical hash
│   ├── grammar/           run grammar: forms, types, call whitelist
│   ├── ops/               crossover, mutation, substitution, repair
│   ├── eval/              Evaluator interface; giri adapter; stub
│   ├── index/             hash-cons, fingerprint index
│   ├── pop/               islands, MAP-Elites, lexicase, migration
│   ├── native/            native benchmark stage, benchstat parsing
│   └── report/            run records, metrics, JSON/CSV out
├── kernels/               reference implementations + fuzz corpora + bench harness
│   ├── popcount/
│   ├── prefixsum/
│   └── argmax/
├── experiments/           E0–E4 run configs and recorded results
├── docs/
├── PROJECT.md
├── SETUP.md
├── CLAUDE.md
└── LICENSE                Apache 2.0, Copyright 2026 Scott Friedman
```
