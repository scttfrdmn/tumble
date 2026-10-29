// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Repair makes f a legal genome again after variation, in place. Walking the
// body in order with the scope at each point:
//
//   - a use of a variable that is not visible is rebound to the nearest
//     visible variable of the same type (the latest defined); with none, the
//     statement is dropped, along with any block it holds;
//   - a reassignment whose target is not an accumulator here is rebound to
//     the nearest accumulator of the type, or dropped;
//   - the body is given a final return of the nearest variable of the result
//     type (a zero constant if there is none);
//   - every local that is never read is sunk with _ = v.
//
// It then checks the grammar's limits, Validate, and the type checker, and
// returns the first failure: a child that fails is discarded.
func Repair(g *grammar.Grammar, f *tir.Func) error {
	sc := grammar.NewScope(f)
	f.Body = repairBlock(f.Body, sc)
	if n := len(f.Body); n == 0 || !isReturn(f.Body[n-1]) {
		f.Body = append(f.Body, finalReturn(f, sc))
	}
	f.Sink()
	if err := g.Admits(f); err != nil {
		return err
	}
	if err := f.Validate(); err != nil {
		return err
	}
	return f.TypeCheck()
}

func isReturn(s tir.Stmt) bool { _, ok := s.(*tir.Return); return ok }

func finalReturn(f *tir.Func, sc *grammar.Scope) *tir.Return {
	if vs := sc.Vars(f.Sig.Result); len(vs) > 0 {
		return &tir.Return{X: vs[len(vs)-1]}
	}
	return &tir.Return{X: tir.Const{T: f.Sig.Result}}
}

// repairBlock repairs ss under scope sc, which it advances.
func repairBlock(ss []tir.Stmt, sc *grammar.Scope) []tir.Stmt {
	out := make([]tir.Stmt, 0, len(ss))
	for _, s := range ss {
		r, ok := repairStmt(s, sc)
		if !ok {
			continue
		}
		switch r := r.(type) {
		case *tir.Loop:
			r.Body = repairBlock(r.Body, sc.Enter(r))
		case *tir.If:
			r.Then = repairBlock(r.Then, sc.Child())
			r.Else = repairBlock(r.Else, sc.Child())
		}
		out = append(out, r)
		sc.Step(r)
	}
	return out
}

// repairStmt rebinds s's own operands (not its nested blocks). It reports
// false if s must be dropped.
func repairStmt(s tir.Stmt, sc *grammar.Scope) (tir.Stmt, bool) {
	ok := true
	r := tir.MapOperands(s, func(o tir.Operand) tir.Operand {
		v, isVar := o.(tir.Var)
		if !isVar || sc.Has(v) {
			return o
		}
		vs := sc.Vars(v.T)
		if len(vs) == 0 {
			ok = false
			return o
		}
		return vs[len(vs)-1]
	})
	if !ok {
		return nil, false
	}
	if a, isAssign := r.(*tir.Assign); isAssign && !a.Define {
		var accs []tir.Var
		for _, v := range sc.Accumulators() {
			if v.T == a.Dst.T {
				accs = append(accs, v)
			}
		}
		if len(accs) == 0 {
			return nil, false
		}
		if !contains(accs, a.Dst) {
			a.Dst = accs[len(accs)-1]
		}
	}
	return r, true
}

func contains(vs []tir.Var, v tir.Var) bool {
	for _, w := range vs {
		if w == v {
			return true
		}
	}
	return false
}
