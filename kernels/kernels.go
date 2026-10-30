// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Package kernels is the registry of reference kernels: for each, the run
// grammar, the reference source and function, the test vectors, the
// reference's outputs, and where the native stage finds the package.
package kernels

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/scttfrdmn/tumble/kernels/argmax"
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

	vectors func() (sel, hold []eval.Input, err error)
	// reference calls the reference on in, whose slices it may modify, and
	// returns its results.
	reference func(in eval.Input) []any
}

// Vectors are the selection and holdout inputs.
func (k *Kernel) Vectors() (sel, hold []eval.Input, err error) { return k.vectors() }

// Reference computes the reference's outputs for in, and the contents of
// its slice arguments after each call (nil for a non-slice argument). Each
// call gets copies, so in is not modified.
func (k *Kernel) Reference(in []eval.Input) (outputs, after [][]any) {
	outputs, after = make([][]any, len(in)), make([][]any, len(in))
	for i, v := range in {
		cp := eval.Input{Args: make([]any, len(v.Args))}
		after[i] = make([]any, len(v.Args))
		for j, a := range v.Args {
			cp.Args[j] = copySlice(a)
		}
		outputs[i] = k.reference(cp)
		for j, a := range cp.Args {
			if reflect.ValueOf(a).Kind() == reflect.Slice {
				after[i][j] = a
			}
		}
	}
	return outputs, after
}

// copySlice copies a slice argument (keeping nil nil); other values are
// returned as they are.
func copySlice(a any) any {
	rv := reflect.ValueOf(a)
	if rv.Kind() != reflect.Slice || rv.IsNil() {
		return a
	}
	c := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
	reflect.Copy(c, rv)
	return c.Interface()
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
	want, wantAfter := k.Reference(sel)
	return pop.Problem{Grammar: g, Reference: ref, Selection: sel, Want: want, WantAfter: wantAfter}, nil
}

var registry = map[string]*Kernel{
	"argmax": {
		Name:    "argmax",
		Grammar: grammar.Argmax,
		Source:  argmax.Source,
		Func:    "Argmax",
		Native:  native.Kernel{Name: "argmax", Dir: "kernels/argmax", Package: "argmax", Func: "Argmax", RefFile: "argmax.go"},
		vectors: func() ([]eval.Input, []eval.Input, error) {
			sel, hold, err := argmax.Vectors()
			return inputs32(sel), inputs32(hold), err
		},
		reference: func(in eval.Input) []any { return []any{argmax.Argmax(in.Args[0].([]int32))} },
	},
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

func inputs32(vs [][]int32) []eval.Input {
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
