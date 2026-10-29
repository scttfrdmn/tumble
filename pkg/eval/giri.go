// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

//go:build giriapi

// This file is the adapter to giri's library API (GIRI_REQUIREMENTS.md),
// written against the surface that document asks for. It builds only with
// -tags giriapi, once that API exists (scttfrdmn/tumble#14), with
// go.mod: replace github.com/scttfrdmn/giri => ../giri.

package eval

import (
	"cmp"
	"fmt"
	"slices"

	gexec "github.com/scttfrdmn/giri/pkg/exec"
	"golang.org/x/tools/go/ssa"
)

func init() {
	Register("giri", func(cfg Config, work string) (Named, error) { return &Giri{}, nil })
}

// Giri evaluates genomes with giri's library entry point: requirement 1
// (exec.Run), 2 (traces), 4 (counters by class), 6 (single goroutine), 7
// (LoadDir), 8 (seed), 9 (partial results on exhaustion). It is the
// instrument E0 runs on. The value cache hook (requirement 3) is supplied by
// pkg/pop through Cache.
type Giri struct {
	Cache gexec.CacheHook // nil for no value cache
	Seed  int64
}

func (*Giri) Name() string       { return "giri" }
func (*Giri) IsInstrument() bool { return true }

type giriProgram struct {
	pkg   *ssa.Package
	funcs map[string]*ssa.Function
}

func (p *giriProgram) Funcs() map[string]*ssa.Function { return p.funcs }

// Build loads the generation package with giri's batch loader (requirement 7).
func (g *Giri) Build(pkgDir string) (Program, error) {
	pkg, err := gexec.LoadDir(pkgDir)
	if err != nil {
		return nil, err
	}
	p := &giriProgram{pkg: pkg, funcs: map[string]*ssa.Function{}}
	for name, m := range pkg.Members {
		if fn, ok := m.(*ssa.Function); ok && name != "init" {
			p.funcs[name] = fn
		}
	}
	return p, nil
}

// Run interprets fn on in (requirement 1) and converts giri's result.
func (g *Giri) Run(fn *ssa.Function, in []Input, opt RunOptions) (Result, error) {
	args := make([][]any, len(in))
	for i, v := range in {
		args[i] = v.Args
	}
	max := opt.MaxSteps
	if max == 0 {
		max = DefaultMaxSteps
	}
	r, err := gexec.Run(fn, args, gexec.Options{
		MaxSteps:        max,
		Trace:           opt.Trace,
		TraceCap:        opt.TraceCap,
		Cache:           g.Cache,
		SingleGoroutine: true,
		Seed:            g.Seed,
	})
	if err != nil {
		return Result{}, err
	}
	res := Result{
		Outputs:   r.Outputs,
		Steps:     r.Steps,
		StepsBy:   map[string]uint64{},
		Exhausted: r.Exhausted,
		Partial:   r.Exhausted,
	}
	for class, n := range r.StepsByClass {
		res.StepsBy[fmt.Sprint(class)] = n
	}
	for _, v := range r.Violations {
		res.Violations = append(res.Violations, Violation{Category: v.Category, Message: v.Message, Input: v.Input})
	}
	for val, tr := range r.Traces {
		res.Traces = append(res.Traces, ValueTrace{Value: val, Seqs: tr.Seqs, Truncated: tr.Truncated})
	}
	// Map order is random; traces are ordered by source position, then name.
	slices.SortFunc(res.Traces, func(a, b ValueTrace) int {
		return cmp.Or(cmp.Compare(a.Value.Pos(), b.Value.Pos()), cmp.Compare(a.Value.Name(), b.Value.Name()))
	})
	return res, nil
}
