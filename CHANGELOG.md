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

### Changed

- `make fuzz-seed` copies the fuzzer's finds from `$GOCACHE/fuzz` into
  `testdata/corpus/`.
