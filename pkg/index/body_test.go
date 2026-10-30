// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"testing"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

const bitLoop2 = `func f(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		x := xs[i]
		c := 0
		for j := 0; j < 64; j++ {
			c += int(x >> uint64(j) & 1)
		}
		n += c
	}
	return n
}`

// cPoint is the point after the inner loop, for c.
func cPoint(t *testing.T, f *tir.Func) tir.BlockPoint {
	t.Helper()
	for _, p := range tir.BlockPoints(f) {
		if len(p.Path) == 1 {
			if _, ok := (*f.Block(p.Path))[p.At].(*tir.Loop); ok {
				return p
			}
		}
	}
	t.Fatal("no point after the inner loop")
	return tir.BlockPoint{}
}

func TestBodySlice(t *testing.T) {
	f := parse(t, bitLoop2)
	fr, ok := BodySlice(f, cPoint(t, f))
	if !ok {
		t.Fatal("no slice for c after the inner loop")
	}
	// c := 0 and the inner loop, which reads x: x's definition is in the
	// block, so it is in the slice, and the slice's only free scalar input
	// is the outer loop variable.
	if len(fr.Stmts) != 3 {
		t.Errorf("slice has %d statements, want 3 (x, c, inner loop)", len(fr.Stmts))
	}
	if len(fr.Inputs) != 1 || fr.Inputs[0].T != tir.Int {
		t.Errorf("inputs = %v, want the outer loop index", fr.Inputs)
	}
	// The outer accumulator n is not in the slice.
	g := parse(t, `func f(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		c := 0
		for j := 0; j < 4; j++ {
			c += j
			n += j
		}
		_ = c
	}
	return n
}`)
	var p tir.BlockPoint
	for _, q := range tir.BlockPoints(g) {
		if len(q.Path) == 1 {
			if _, ok := (*g.Block(q.Path))[q.At].(*tir.Loop); ok {
				p = q
			}
		}
	}
	if _, ok := BodySlice(g, p); ok {
		t.Error("slice whose inner loop also reassigns the outer accumulator was accepted")
	}
}

func TestBodySliceHashAcrossGenomes(t *testing.T) {
	// The same inner computation in two genomes with different preambles
	// hashes the same.
	f := parse(t, bitLoop2)
	g := parse(t, `func f(xs []uint64) int {
	pad := len(xs)
	_ = pad
	n := 0
	for i := 0; i < len(xs); i++ {
		x := xs[i]
		c := 0
		for j := 0; j < 64; j++ {
			c += int(x >> uint64(j) & 1)
		}
		n += c
	}
	return n
}`)
	a, _ := BodySlice(f, cPoint(t, f))
	b, _ := BodySlice(g, cPoint(t, g))
	if a == nil || b == nil || a.Hash != b.Hash {
		t.Error("identical body slices hash differently across genomes")
	}
}

func TestBodyIndexBinding(t *testing.T) {
	ix := NewBodyIndex()
	x := tir.Var{ID: 10, T: tir.Uint64}
	c := tir.Var{ID: 11, T: tir.Int}
	lib := &BodyFragment{
		Stmts:  []tir.Stmt{&tir.Assign{Dst: c, Define: true, X: &tir.Call{Pkg: "math/bits", Name: "OnesCount64", Args: []tir.Operand{x}}}},
		Result: c, Inputs: []tir.Var{x},
	}
	lib.Hash = tir.HashStmts(1, append(tir.CloneStmts(lib.Stmts), &tir.Return{X: c}))
	fpOut, _ := Of(intT, [][]any{{1, 2}})
	fpX, _ := Of(intT, [][]any{{9, 9}})
	fpOther, _ := Of(intT, [][]any{{8}})
	ix.Add(fpOut, BodyEntry{Fragment: lib, InputFP: []Fingerprint{fpX}, PerExec: 1, Library: true})
	recip := tir.Var{ID: 3, T: tir.Uint64}
	below := func(limit float64) func(BodyEntry, map[int]tir.Var) bool {
		return func(e BodyEntry, _ map[int]tir.Var) bool { return e.PerExec < limit }
	}
	if _, _, ok := ix.Cheaper(fpOut, []ScopedFP{{V: recip, FP: fpOther}}, below(40)); ok {
		t.Error("bound an input to a variable with a different fingerprint")
	}
	if _, _, ok := ix.Cheaper(fpOut, []ScopedFP{{V: recip, FP: fpX}}, below(1)); ok {
		t.Error("donated a fragment the acceptance test rejected")
	}
	e, bind, ok := ix.Cheaper(fpOut, []ScopedFP{{V: recip, FP: fpX}}, below(40))
	if !ok || !e.Library || bind[x.ID] != recip {
		t.Errorf("binding: ok %v, bind %v", ok, bind)
	}
}
