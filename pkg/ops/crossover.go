// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"math/rand/v2"
	"slices"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// TwoPoint is two-point crossover (PROJECT.md §4.2). It picks a block depth
// present in both parents, a block at that depth in each, and a statement
// range in each (cut points are statement boundaries, so both cuts of a
// range are at that block's depth); the child is a with its range replaced
// by b's. b's locals are renumbered fresh in the child, so b's references to
// its own definitions outside the range dangle and are rebound by Repair.
// At least one of the two ranges is non-empty.
func TwoPoint(r *rand.Rand, g *grammar.Grammar, a, b *tir.Func) (*tir.Func, error) {
	child := a.Clone()
	ab, bb := blocks(child), blocks(b)

	depthsB := map[int]bool{}
	for _, p := range bb {
		depthsB[p.depth] = true
	}
	var depths []int
	for _, p := range ab {
		if depthsB[p.depth] && !slices.Contains(depths, p.depth) {
			depths = append(depths, p.depth)
		}
	}
	d := depths[r.IntN(len(depths))] // depth 0 is always shared
	at := func(ps []pos) []pos {
		var out []pos
		for _, p := range ps {
			if p.depth == d {
				out = append(out, p)
			}
		}
		return out
	}
	pa, pb := pick(r, at(ab)), pick(r, at(bb))

	var i, j, k, l int
	for {
		i, j = cut(r, len(*pa.block))
		k, l = cut(r, len(*pb.block))
		if j > i || l > k || (len(*pa.block) == 0 && len(*pb.block) == 0) {
			break
		}
	}
	seg, _ := renumber(child, len(b.Sig.Params), (*pb.block)[k:l])
	*pa.block = insert(remove(*pa.block, i, j), i, seg...)

	if err := Repair(g, child); err != nil {
		return nil, &Discarded{Op: Crossover, Reason: err}
	}
	return child, nil
}

// cut draws a range [i, j) of a block of n statements, 0 <= i <= j <= n.
func cut(r *rand.Rand, n int) (int, int) {
	i, j := r.IntN(n+1), r.IntN(n+1)
	if i > j {
		i, j = j, i
	}
	return i, j
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// renumber deep-copies ss from a donor into child's ID space: every local of
// the donor (IDs from nparams up) gets a fresh ID in child, the same donor ID
// always the same fresh ID; parameters are shared. It returns the copy and
// the mapping.
func renumber(child *tir.Func, nparams int, ss []tir.Stmt) ([]tir.Stmt, func(tir.Var) tir.Var) {
	ids := map[int]tir.Var{}
	re := func(v tir.Var) tir.Var {
		if v.ID < nparams {
			return v
		}
		w, ok := ids[v.ID]
		if !ok {
			w = child.NewVar(v.T)
			ids[v.ID] = w
		}
		return w
	}
	var copyBlock func([]tir.Stmt) []tir.Stmt
	copyBlock = func(ss []tir.Stmt) []tir.Stmt {
		out := make([]tir.Stmt, 0, len(ss))
		for _, s := range ss {
			c := tir.MapOperands(s, func(o tir.Operand) tir.Operand {
				if v, ok := o.(tir.Var); ok {
					return re(v)
				}
				return o
			})
			switch c := c.(type) {
			case *tir.Assign:
				c.Dst = re(c.Dst)
			case *tir.Loop:
				c.Ind = re(c.Ind)
				c.Body = copyBlock(c.Body)
			case *tir.If:
				c.Then = copyBlock(c.Then)
				c.Else = copyBlock(c.Else)
			}
			out = append(out, c)
		}
		return out
	}
	return copyBlock(ss), re
}
