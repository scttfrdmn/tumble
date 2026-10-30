// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"math/rand/v2"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Donor is what equivalence substitution needs from the fingerprint index
// (pkg/index): for a top-level point of a genome, a cheaper fragment with the
// same fingerprint, closed over the parameters.
type Donor interface {
	// Empty reports whether the index holds no fragments.
	Empty() bool
	// Cheaper returns a fragment cheaper than the backward slice of p.V at
	// point p in f that computes the same value on every test vector:
	// statements over the signature's parameters, in the donor's ID space,
	// and the variable holding the value.
	Cheaper(f *tir.Func, p tir.Point) (frag []tir.Stmt, result tir.Var, ok bool)
}

// Substitute is equivalence substitution (PROJECT.md §4.2). It tries the
// child's top-level points (tir.Points) whose variable is read later, in
// random order. For the first point p the donor has a cheaper fragment for,
// it inserts the fragment and w := <fragment result> after p's statement,
// makes every later read of p.V read w, and removes the top-level
// statements that were live before and are dead after: the old backward
// slice, which now has no reader. Code that was already dead stays.
//
// A point is the value of a variable after a top-level definition or after
// a top-level loop or if that reassigns it, so an accumulator's value after
// its loop is a candidate. The fragment replaces whole top-level statements
// and never cuts through a loop.
//
// applied is false, and the child is an unchanged copy of a, when the index
// is empty or holds nothing cheaper for any candidate.
func Substitute(r *rand.Rand, g *grammar.Grammar, d Donor, a *tir.Func) (child *tir.Func, applied bool, err error) {
	child = a.Clone()
	if d == nil || d.Empty() {
		return child, false, nil
	}
	var cands []tir.Point
	for _, p := range tir.Points(child) {
		if tir.ReadAfter(child, p.At, p.V) {
			cands = append(cands, p)
		}
	}
	for _, ci := range r.Perm(len(cands)) {
		p := cands[ci]
		frag, res, ok := d.Cheaper(child, p)
		if !ok {
			continue
		}
		before := live(child.Body)
		seg, re := renumber(child, len(child.Sig.Params), frag)
		w := child.NewVar(p.V.T)
		seg = append(seg, &tir.Assign{Dst: w, Define: true, X: &tir.Atom{X: re(res)}})
		rest := renameReads(child.Body[p.At+1:], p.V, w)
		body := append(append(append([]tir.Stmt(nil), child.Body[:p.At+1]...), seg...), rest...)
		child.Body = body
		after := live(child.Body)
		kept := child.Body[:0:0]
		for _, s := range child.Body {
			if before[s] && !after[s] {
				continue
			}
			kept = append(kept, s)
		}
		child.Body = kept
		if err := Repair(g, child); err != nil {
			return nil, true, &Discarded{Op: Substitution, Reason: err}
		}
		return child, true, nil
	}
	return child, false, nil
}

// renameReads makes every read of v in ss, nested blocks included, read w.
// Statements without such a read keep their identity (live compares
// statements by pointer); the others are replaced by renamed copies.
func renameReads(ss []tir.Stmt, v, w tir.Var) []tir.Stmt {
	out := make([]tir.Stmt, len(ss))
	for i, s := range ss {
		if !readsVar(s, v) {
			out[i] = s
			continue
		}
		c := tir.MapOperands(s, func(o tir.Operand) tir.Operand {
			if x, ok := o.(tir.Var); ok && x.ID == v.ID {
				return w
			}
			return o
		})
		switch c := c.(type) {
		case *tir.Loop:
			c.Body = renameReads(c.Body, v, w)
		case *tir.If:
			c.Then = renameReads(c.Then, v, w)
			c.Else = renameReads(c.Else, v, w)
		}
		out[i] = c
	}
	return out
}

func readsVar(s tir.Stmt, v tir.Var) bool {
	found := false
	tir.Walk([]tir.Stmt{s}, func(t tir.Stmt, _ int) bool {
		for _, u := range tir.Uses(t) {
			if u.ID == v.ID {
				found = true
			}
		}
		return !found
	})
	return found
}

// live is the set of a block's statements whose effects are observable: the
// returns and stores, the reassignments of variables defined outside the
// block (an enclosing loop's accumulators), and transitively whatever they
// read. A definition is live if a live statement reads it; a loop or if is
// live if it holds an effect, or reassigns a variable a live statement other
// than itself reads. Sinks are never live; Repair re-adds the ones needed.
// For the function body every reassigned variable is defined in the body,
// so only stores and returns are effects there.
func live(body []tir.Stmt) map[tir.Stmt]bool {
	own := map[int]bool{}
	for _, s := range body {
		for _, d := range tir.Defs(s) {
			own[d.ID] = true
		}
	}
	reads := make([]map[int]bool, len(body))
	effects := make([]bool, len(body))
	writes := make([]map[int]bool, len(body))
	for i, s := range body {
		reads[i], writes[i] = map[int]bool{}, map[int]bool{}
		local := map[int]bool{}
		tir.Walk([]tir.Stmt{s}, func(t tir.Stmt, _ int) bool {
			switch t := t.(type) {
			case *tir.Sink:
				return true
			case *tir.Store, *tir.Return:
				effects[i] = true
			case *tir.Assign:
				if !t.Define && !local[t.Dst.ID] {
					writes[i][t.Dst.ID] = true
					if !own[t.Dst.ID] {
						effects[i] = true
					}
				}
			}
			for _, d := range tir.Defs(t) {
				local[d.ID] = true
			}
			for _, u := range tir.Uses(t) {
				reads[i][u.ID] = true
			}
			return true
		})
		if _, ok := s.(*tir.Sink); ok {
			reads[i] = nil
		}
	}
	isLive := make([]bool, len(body))
	for changed := true; changed; {
		changed = false
		for i, s := range body {
			if isLive[i] {
				continue
			}
			l := effects[i]
			if !l {
				var defs []int
				if as, ok := s.(*tir.Assign); ok && as.Define {
					defs = append(defs, as.Dst.ID)
				}
				for id := range writes[i] {
					defs = append(defs, id)
				}
				for _, id := range defs {
					for j := range body {
						if j != i && isLive[j] && reads[j][id] {
							l = true
						}
					}
				}
			}
			if l {
				isLive[i] = true
				changed = true
			}
		}
	}
	out := map[tir.Stmt]bool{}
	for i, s := range body {
		if isLive[i] {
			out[s] = true
		}
	}
	return out
}
