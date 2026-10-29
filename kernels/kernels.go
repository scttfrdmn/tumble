// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Package kernels is the registry of reference kernels: for each, the run
// grammar, the reference source and function, the test vectors, the
// reference's outputs, and where the native stage finds the package.
package kernels

import (
	"fmt"
	"slices"

	"github.com/scttfrdmn/tumble/kernels/popcount"
	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/native"
	"github.com/scttfrdmn/tumble/pkg/pop"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Kernel is one registered kernel.
type Kernel struct {
	Name    string
	Grammar func() *grammar.Grammar
	Source  []byte // the reference file
	Func    string // the reference function in Source
	Native  native.Kernel

	vectors   func() (sel, hold []eval.Input, err error)
	reference func(eval.Input) []any
}

// Vectors are the selection and holdout inputs.
func (k *Kernel) Vectors() (sel, hold []eval.Input, err error) { return k.vectors() }

// Reference computes the reference outputs for in.
func (k *Kernel) Reference(in []eval.Input) [][]any {
	out := make([][]any, len(in))
	for i, v := range in {
		out[i] = k.reference(v)
	}
	return out
}

// Problem is the kernel as a pop.Problem over its selection vectors.
func (k *Kernel) Problem() (pop.Problem, error) {
	g := k.Grammar()
	ref, err := tir.Parse(k.Source, k.Func)
	if err != nil {
		return pop.Problem{}, fmt.Errorf("kernels: %s reference: %w", k.Name, err)
	}
	sel, _, err := k.Vectors()
	if err != nil {
		return pop.Problem{}, err
	}
	return pop.Problem{Grammar: g, Reference: ref, Selection: sel, Want: k.Reference(sel)}, nil
}

var registry = map[string]*Kernel{
	"popcount": {
		Name:    "popcount",
		Grammar: grammar.Popcount,
		Source:  popcount.Source,
		Func:    "Popcount",
		Native:  native.Kernel{Name: "popcount", Dir: "kernels/popcount", Package: "popcount", Func: "Popcount", RefFile: "popcount.go"},
		vectors: func() ([]eval.Input, []eval.Input, error) {
			sel, hold, err := popcount.Vectors()
			return inputs(sel), inputs(hold), err
		},
		reference: func(in eval.Input) []any { return []any{popcount.Popcount(in.Args[0].([]uint64))} },
	},
}

func inputs(vs [][]uint64) []eval.Input {
	out := make([]eval.Input, len(vs))
	for i, v := range vs {
		out[i] = eval.Input{Args: []any{v}}
	}
	return out
}

// Get returns the named kernel.
func Get(name string) (*Kernel, error) {
	k, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("kernels: unknown kernel %q, have %v", name, Names())
	}
	return k, nil
}

// Names are the registered kernels, sorted.
func Names() []string {
	var out []string
	for n := range registry {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}
