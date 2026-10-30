// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"math/rand/v2"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// MaxDuplicate is the longest statement range Duplicate copies.
const MaxDuplicate = 4

// Duplicate is gene duplication, a blind structural mutation (arm D,
// decision #81): it picks a block of the parent, at any depth, and a
// contiguous range of 1..MaxDuplicate statements in it (sinks excluded from
// the count), and inserts a copy of the range directly after it. Locals the
// range defines get fresh IDs in the copy, so the copy is legal; reads of
// variables defined before the range keep reading them. Repair then runs as
// for every child. It inspects only the statement list's structure, never
// what the statements compute.
func Duplicate(r *rand.Rand, g *grammar.Grammar, a *tir.Func) (*tir.Func, error) {
	child := a.Clone()
	var nonEmpty []pos
	for _, b := range blocks(child) {
		if len(*b.block) > 0 {
			nonEmpty = append(nonEmpty, b)
		}
	}
	if len(nonEmpty) == 0 {
		return nil, &Discarded{Op: Duplication, Reason: errEmpty}
	}
	b := pick(r, nonEmpty)
	n := len(*b.block)
	i := r.IntN(n)
	j := min(n, i+1+r.IntN(MaxDuplicate))
	cp, _ := renumberDefs(child, (*b.block)[i:j])
	*b.block = insert(*b.block, j, cp...)
	if err := Repair(g, child); err != nil {
		return nil, &Discarded{Op: Duplication, Reason: err}
	}
	return child, nil
}

type emptyErr struct{}

func (emptyErr) Error() string { return "no statements to duplicate" }

var errEmpty = emptyErr{}

// renumberDefs deep-copies ss inside child, giving each local defined in ss
// (assign definitions and loop variables, at any depth) a fresh ID, and
// rewriting reads of those locals to match. Variables defined outside ss
// keep their IDs.
func renumberDefs(child *tir.Func, ss []tir.Stmt) ([]tir.Stmt, map[int]tir.Var) {
	fresh := map[int]tir.Var{}
	tir.Walk(ss, func(s tir.Stmt, _ int) bool {
		for _, d := range tir.Defs(s) {
			if _, ok := fresh[d.ID]; !ok {
				fresh[d.ID] = child.NewVar(d.T)
			}
		}
		return true
	})
	re := func(v tir.Var) tir.Var {
		if w, ok := fresh[v.ID]; ok {
			return w
		}
		return v
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
	return copyBlock(ss), fresh
}
