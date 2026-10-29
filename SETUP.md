# tumble — project setup for Claude Code

This file is read by Claude Code at the start of every session. It states the repository conventions, the build and measurement discipline, and the standing decisions. `PROJECT.md` is the design; this is how work on it is done.

## Identity

- Module: `github.com/scttfrdmn/tumble`
- License: Apache License 2.0. Every source file carries `// Copyright 2026 Scott Friedman` and the SPDX line `// SPDX-License-Identifier: Apache-2.0`.
- Language: Go, latest stable at repo creation; pin in `go.mod` to whatever `github.com/scttfrdmn/giri` requires or newer. giri's own build needs `GOEXPERIMENT=arenas`; the `Makefile` exports it for every target so this is never a surprise.
- Dependencies, v0: `golang.org/x/tools` (go/ssa, go/packages, go/analysis), `github.com/scttfrdmn/giri` (once its library API exists — see `GIRI_REQUIREMENTS.md`), `golang.org/x/perf/cmd/benchstat` (tool dependency). Nothing else without a recorded reason (a `type: decision` issue, see below).

## Standing decisions

Decisions in `PROJECT.md` §6 are in force. New decisions are recorded on GitHub, not in local files: each is an issue labelled `type: decision` (title: the decision; body: what was decided, why, what it replaces; linked from the issue whose work prompted it). A decision that contradicts `PROJECT.md` updates `PROJECT.md` in the same commit, and the commit references the decision issue.

Scope, version numbers, releases, and what the project is for are Scott's. Everything else — representation details, parameter defaults, operator mechanics, test design — is decided from the project's state and recorded; it is not escalated as an open question. When a path is blocked by giri, the Go toolchain, or tumble's own tooling, fix the tool and keep going; record what was fixed.

Nobody but Scott declares a milestone, an experiment, or the project done. Reports state what ran, what was measured, and what the instrument readings were.

## Tracking

Project status lives on GitHub only: the `tumble` project board, milestones for phases (M0, E0–E3, then the v1–v3 scope phases of `PROJECT.md` §3 — design phases, not release versions), issues for each unit of work, and labels for type (`type: *`), priority (`P0`–`P2`) and component (`area: *`). Every new piece of work gets an issue with labels, a milestone, and a place on the board. The repository holds no plan, status, progress, or TODO files; decisions and session reports are issue comments.

Versions stay on 0.x until Scott says otherwise.

## Layout

See `PROJECT.md` §12. Packages under `pkg/` have no dependencies on `cmd/`. `kernels/` are ordinary Go packages with a reference implementation, a `_test.go` with the benchmark harness and fuzz target, and a `testdata/corpus/` seeded by running the fuzzer against the reference for 60 s.

## Build and test

```
make            # vet, build, unit tests
make lint       # golangci-lint with the repo config
make fuzz-seed  # regenerate kernels/*/testdata/corpus (60 s per kernel)
make e0 ...     # experiments; each writes experiments/<name>/<timestamp>/
```

- `go vet ./...` and `golangci-lint` clean before any commit.
- Unit tests for every operator: given a genome and a seed, the child is deterministic; the child type-checks or is reported as discarded; the repair pass never produces an unused variable.
- Property test for `tir`: print → parse → print is a fixpoint; canonical hash is invariant under α-renaming.
- Property test for the fingerprint index: two fragments with equal fingerprints produce equal outputs on every test vector (this is the collision detector running as a test).
- Determinism: a run with a fixed seed and fixed giri version reproduces its generation-by-generation archive byte for byte. CI runs 5 generations of `popcount` twice and diffs.

## Measurement discipline

Every number that leaves the repository has a record in `experiments/` containing:

- kernel, arm, wall-clock budget, seed
- hardware: `uname -m`, CPU model string, instance type if on AWS, `GOAMD64`/`GOARM64`
- versions: Go, giri commit, tumble commit, OpenEvolve commit and model id (arm C)
- the full run parameter set as JSON
- raw `go test -bench` output and the `benchstat` table
- per-generation instrument readings: VC hit rate, FP collision count, operator yields, archive size, best abstract cost, best `ns/op`

`ns/op` is reported with the `benchstat` interval and `-count=10` minimum. A result without an interval is not a result.

No AWS account IDs in any committed file or any public issue.

## Evaluator interface

`pkg/eval` defines:

```go
type Input struct{ Args []any }          // Go values matching the kernel signature
type ValueTrace struct {
    Value ssa.Value
    Seqs  [][]any                         // one sequence per input vector
}
type Result struct {
    Outputs    [][]any                    // per input vector
    Traces     []ValueTrace
    Steps      uint64
    StepsBy    map[string]uint64          // by instruction class
    Violations []Violation
    Exhausted  bool                       // step budget hit
}
type Evaluator interface {
    Build(pkgDir string) (Program, error) // one package per generation
    Run(fn *ssa.Function, in []Input, opt RunOptions) (Result, error)
}
```

Two implementations: `giri` (the real one, against the API in `GIRI_REQUIREMENTS.md`) and `stub` (shells out to `giri -v`, parses the instruction dump, no traces). The stub exists so tumble's population and operator code can be developed and tested before the giri API lands. **No experiment runs against the stub.** `experiments/` tooling refuses to start with `eval.stub`.

## Working with giri

giri is a separate repository with its own Claude Code. Requirements tumble has of giri are stated in `GIRI_REQUIREMENTS.md` and nowhere else. If tumble needs something new from giri, add it there as a numbered requirement, open an issue on `scttfrdmn/giri` that references the requirement number, and build against a local replace directive (`go.mod`: `replace github.com/scttfrdmn/giri => ../giri`) until it is released. Do not fork giri into tumble.

## Commit conventions

- One concern per commit. Subject line ≤ 72 chars, imperative.
- Commits that change a run parameter default, the grammar, or the cost weights say so in the subject (`grammar:`, `params:`, `cost:`).
- Experiment results are committed as data under `experiments/`; the commit that adds a result does not change code.

## What a session report looks like

At the end of a session, the active milestone's tracking issue gets a dated comment: what was built, what tests cover it, what instrument readings were taken (with the numbers), which `type: decision` issues were opened, and what is blocked on what. State, not verdicts.
