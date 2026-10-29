// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"cmp"
	"fmt"
	"slices"

	gexec "github.com/scttfrdmn/giri/pkg/exec"
	"golang.org/x/tools/go/ssa"
)

func init() {
	Register("giri", func(cfg Config, work string) (Named, error) { return NewGiri(cfg), nil })
}

// Giri evaluates genomes with giri's library API (github.com/scttfrdmn/giri/
// pkg/exec; GIRI_REQUIREMENTS.md). It is the instrument E0 runs on.
//
// Every genome runs with giri's detectors on and one goroutine. The value
// cache (requirement 3) is on unless Config.NoCache is set.
type Giri struct {
	seed  int64
	cache *ValueCache // nil when off

	concrete map[string]bool
}

// NewGiri returns the giri evaluator for cfg.
func NewGiri(cfg Config) *Giri {
	g := &Giri{seed: cfg.Seed, concrete: map[string]bool{}}
	if !cfg.NoCache {
		g.cache = NewValueCache(cfg.CacheEntries)
	}
	for _, f := range gexec.ConcreteFuncs() {
		g.concrete[f] = true
	}
	return g
}

func (*Giri) Name() string       { return "giri" }
func (*Giri) IsInstrument() bool { return true }

// IsConcrete reports whether giri models pkg.name concretely (requirement 5).
// A grammar may only admit concrete calls.
func (g *Giri) IsConcrete(pkg, name string) bool { return g.concrete[pkg+"."+name] }

// Cache is the evaluator's value cache, or nil.
func (g *Giri) Cache() *ValueCache { return g.cache }

// EndGeneration commits the value cache's new entries (see ValueCache).
func (g *Giri) EndGeneration() {
	if g.cache != nil {
		g.cache.EndGeneration()
	}
}

type giriProgram struct {
	funcs map[string]*ssa.Function
}

func (p *giriProgram) Funcs() map[string]*ssa.Function { return p.funcs }

// Build loads the generation package with giri's batch loader (requirement 7).
func (g *Giri) Build(pkgDir string) (Program, error) {
	pkg, err := gexec.LoadDir(pkgDir)
	if err != nil {
		return nil, err
	}
	p := &giriProgram{funcs: map[string]*ssa.Function{}}
	for name, m := range pkg.Members {
		if fn, ok := m.(*ssa.Function); ok && name != "init" {
			p.funcs[name] = fn
		}
	}
	return p, nil
}

// Run interprets fn on in (requirement 1) and converts giri's result.
//
// Per input vector: a returned call gives its outputs; a panic (giri-raised
// out-of-range, nil dereference, divide by zero) is a violation if no
// detector finding already covers that vector; an opaque result (a value
// no concrete model produced) is a violation, since nothing can be compared
// against it; exhaustion sets Exhausted, and the vectors after it have no
// outputs.
func (g *Giri) Run(fn *ssa.Function, in []Input, opt RunOptions) (Result, error) {
	args := make([][]any, len(in))
	for i, v := range in {
		args[i] = v.Args
	}
	maxSteps := opt.MaxSteps
	if maxSteps == 0 {
		maxSteps = DefaultMaxSteps
	}
	o := gexec.Options{
		MaxSteps:        maxSteps,
		Trace:           opt.Trace,
		TraceCap:        opt.TraceCap,
		SingleGoroutine: true,
		Seed:            g.seed,
	}
	var hook *runCache
	if g.cache != nil {
		hook = g.cache.hook()
		o.Cache = hook
	}
	r, err := gexec.Run(fn, args, o)
	if err != nil {
		return Result{}, fmt.Errorf("eval: giri: %s: %w", fn.Name(), err)
	}
	if hook != nil {
		hook.flush()
	}

	res := Result{
		Outputs:       make([][]any, len(in)),
		Steps:         r.Steps,
		StepsBy:       map[string]uint64{},
		Exhausted:     r.Exhausted,
		Partial:       len(r.Outcomes) < len(in),
		CacheHits:     r.CacheHits,
		PureExecuted:  r.PureExecuted,
		CacheMeasured: g.cache != nil,
	}
	for c, n := range r.StepsByClass {
		res.StepsBy[c.String()] = n
	}
	covered := map[int]bool{}
	for _, v := range r.Violations {
		res.Violations = append(res.Violations, Violation{Category: v.Category, Message: v.Message, Input: v.Set})
		covered[v.Set] = true
	}
	for i, oc := range r.Outcomes {
		switch oc {
		case gexec.Returned:
			res.Outputs[i] = r.Outputs[i]
		case gexec.Exhausted:
			res.Exhausted, res.Partial = true, true
		case gexec.Opaque:
			res.Partial = true
			res.Violations = append(res.Violations, Violation{Category: "opaque", Message: "result has no concrete value", Input: i})
		default: // Panicked, and any outcome added later
			res.Partial = true
			if !covered[i] {
				res.Violations = append(res.Violations, Violation{Category: "panic", Message: fmt.Sprintf("outcome %v", oc), Input: i})
			}
		}
	}
	if opt.Trace {
		res.Traces = traces(r.Traces, len(in))
	}
	return res, nil
}

// traces turns giri's per-set traces into one ValueTrace per value with a
// sequence per input vector. A value not executed on a vector has an empty
// sequence there. A value that is truncated or opaque on any vector is
// marked Truncated (no fingerprint); values giri traces only by count
// (pointers, slices, tuples) are left out. Traces are ordered by source
// position, then name, since giri returns them in maps.
func traces(sets []gexec.Traces, nvec int) []ValueTrace {
	byVal := map[ssa.Value]*ValueTrace{}
	var order []ssa.Value
	for t, set := range sets {
		for v, tr := range set {
			if tr.Kind == gexec.TraceOther || tr.Kind == gexec.TraceFloat || tr.Kind == gexec.TraceString {
				continue
			}
			vt, ok := byVal[v]
			if !ok {
				vt = &ValueTrace{Value: v, Seqs: make([][]any, nvec)}
				byVal[v] = vt
				order = append(order, v)
			}
			if tr.Truncated || tr.Opaque {
				vt.Truncated = true
			}
			seq := make([]any, 0, tr.Len())
			switch tr.Kind {
			case gexec.TraceInt:
				for _, x := range tr.Ints {
					seq = append(seq, x)
				}
			case gexec.TraceUint:
				for _, x := range tr.Uints {
					seq = append(seq, x)
				}
			case gexec.TraceBool:
				for _, x := range tr.Bools {
					seq = append(seq, x)
				}
			}
			vt.Seqs[t] = seq
		}
	}
	// An exhausted run has no traces for the vectors it never reached; the
	// genome is discarded, but its values still get a fingerprint only if
	// every vector ran.
	if len(sets) < nvec {
		for _, vt := range byVal {
			vt.Truncated = true
		}
	}
	slices.SortFunc(order, func(a, b ssa.Value) int {
		return cmp.Or(cmp.Compare(a.Pos(), b.Pos()), cmp.Compare(a.Name(), b.Name()))
	})
	out := make([]ValueTrace, len(order))
	for i, v := range order {
		out[i] = *byVal[v]
	}
	return out
}
