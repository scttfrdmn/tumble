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

### Changed

- `PROJECT.md` §4.1: expressions are one level over atoms, and `convert` is an
  expression form (#39).

- `GIRI_REQUIREMENTS.md`: requirement 11, value-faithful memory
  (scttfrdmn/giri#248).
- `make fuzz-seed` copies the fuzzer's finds from `$GOCACHE/fuzz` into
  `testdata/corpus/`.
