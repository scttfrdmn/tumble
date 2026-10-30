// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"math/rand/v2"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// BodyDonor is what substitution inside nested blocks needs (#66): for a
// point of a genome, a cheaper fragment computing the same per-iteration
// values, and a binding of the fragment's free inputs to variables in scope
// at the point.
type BodyDonor interface {
	Empty() bool
	// CheaperAt returns the fragment's statements and result variable, in
	// the donor's ID space, and bind, which maps each of its free input
	// variables' IDs to the recipient variable it reads in their place.
	CheaperAt(f *tir.Func, p tir.BlockPoint) (frag []tir.Stmt, result tir.Var, bind map[int]tir.Var, ok bool)
}

// SubstituteBody is equivalence substitution at a point inside a loop body
// or if branch. For the first point (random order) whose variable is read
// later in its block and for which the donor has a cheaper fragment, it
// inserts the fragment, with its inputs bound to recipient variables and its
// own locals fresh, then w := <fragment result>. Later reads of the variable
// in the block read w, and the block's statements that were live before
// and are dead after (the old slice) are removed. applied is false, and the
// child an unchanged copy, if nothing applies.
func SubstituteBody(r *rand.Rand, g *grammar.Grammar, d BodyDonor, a *tir.Func) (child *tir.Func, applied bool, err error) {
	child = a.Clone()
	if d == nil || d.Empty() {
		return child, false, nil
	}
	var cands []tir.BlockPoint
	for _, p := range tir.BlockPoints(child) {
		b := child.Block(p.Path)
		if readsLater(*b, p.At, p.V) {
			cands = append(cands, p)
		}
	}
	for _, ci := range r.Perm(len(cands)) {
		p := cands[ci]
		frag, res, bind, ok := d.CheaperAt(child, p)
		if !ok {
			continue
		}
		b := child.Block(p.Path)
		before := live(*b)
		seg, re := renumberBound(child, len(child.Sig.Params), frag, bind)
		w := child.NewVar(p.V.T)
		seg = append(seg, &tir.Assign{Dst: w, Define: true, X: &tir.Atom{X: re(res)}})
		rest := renameReads((*b)[p.At+1:], p.V, w)
		nb := append(append(append([]tir.Stmt(nil), (*b)[:p.At+1]...), seg...), rest...)
		after := live(nb)
		kept := nb[:0:0]
		for _, s := range nb {
			if before[s] && !after[s] {
				continue
			}
			kept = append(kept, s)
		}
		*b = kept
		if err := Repair(g, child); err != nil {
			return nil, true, &Discarded{Op: Substitution, Reason: err}
		}
		return child, true, nil
	}
	return child, false, nil
}

// readsLater reports whether a statement after index at of ss reads v.
func readsLater(ss []tir.Stmt, at int, v tir.Var) bool {
	for _, s := range ss[at+1:] {
		if _, sink := s.(*tir.Sink); !sink && readsVar(s, v) {
			return true
		}
	}
	return false
}

// renumberBound copies ss into child's ID space: parameters are shared,
// variables in bind read the recipient variable they are bound to, and every
// other variable gets a fresh ID (the same donor ID, the same fresh ID).
func renumberBound(child *tir.Func, nparams int, ss []tir.Stmt, bind map[int]tir.Var) ([]tir.Stmt, func(tir.Var) tir.Var) {
	ids := map[int]tir.Var{}
	re := func(v tir.Var) tir.Var {
		if v.ID < nparams {
			return v
		}
		if b, ok := bind[v.ID]; ok {
			return b
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
