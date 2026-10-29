// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"golang.org/x/tools/go/ssa"
)

// Input is one test vector: Go values matching the kernel signature.
type Input struct{ Args []any }

// ValueTrace is the sequence of concrete values one SSA value took, one
// sequence per input vector (GIRI_REQUIREMENTS.md, requirement 2).
// Truncated is set when the evaluator's per-value cap cut a sequence short;
// a truncated value has no fingerprint.
type ValueTrace struct {
	Value     ssa.Value
	Seqs      [][]any
	Truncated bool
}

// Violation is one finding from the evaluator's detectors, or a runtime
// fault the evaluator observed.
type Violation struct {
	Category string `json:"category"`
	Message  string `json:"message"`
	Input    int    `json:"input"` // the input vector index, or -1 if unknown
}

// Result is one function's evaluation over a set of input vectors.
type Result struct {
	Outputs    [][]any           // per input vector: the return values
	Traces     []ValueTrace      // empty when RunOptions.Trace is off, and always under the stub
	Steps      uint64            // instructions executed, summed over vectors
	StepsBy    map[string]uint64 // by instruction class; nil if the evaluator cannot break steps down
	Violations []Violation
	Exhausted  bool // the step budget was hit
	Partial    bool // Outputs is missing vectors (budget exhaustion or a fault)

	// Value-cache readings: pure instructions served from the cache and
	// pure instructions executed. CacheMeasured is false when the evaluator
	// has no value cache (the stub, or the cache switched off).
	CacheHits     uint64
	PureExecuted  uint64
	CacheMeasured bool
}

// RunOptions are per-run evaluator options.
type RunOptions struct {
	MaxSteps uint64 // the step budget over all vectors; 0 for the evaluator's default
	Trace    bool   // record per-value traces
	TraceCap int    // per-value sequence cap when tracing
}

// Program is one generation's built package.
type Program interface {
	// Funcs are the package's functions by name.
	Funcs() map[string]*ssa.Function
}

// Evaluator builds a generation's package and runs its functions.
type Evaluator interface {
	Build(pkgDir string) (Program, error) // one package per generation
	Run(fn *ssa.Function, in []Input, opt RunOptions) (Result, error)
}
