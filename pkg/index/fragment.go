// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"slices"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Fragment is a variable's backward slice: the statements that compute it
// from the parameters and constants, closed over the parameters, and the
// variable itself. Hash is its structural hash (§5.1), which covers which
// variable is the result.
type Fragment struct {
	Stmts  []tir.Stmt
	Result tir.Var
	Hash   tir.Hash
}

// program is the fragment's statements followed by return of its result: the
// form its structural hash is taken over.
func (fr *Fragment) program() []tir.Stmt {
	return append(tir.CloneStmts(fr.Stmts), &tir.Return{X: fr.Result})
}

// Func is the fragment as a function over sig's parameters returning its
// result (so its result type is the fragment's, not necessarily sig's),
// sunk so that it type-checks: the form in which a fragment is evaluated for
// its cost.
func (fr *Fragment) Func(sig *tir.Signature) *tir.Func {
	f := tir.NewFunc(&tir.Signature{Params: sig.Params, Result: fr.Result.T})
	f.Body = fr.program()
	tir.Walk(f.Body, func(s tir.Stmt, _ int) bool {
		for _, d := range tir.Defs(s) {
			f.NextID = max(f.NextID, d.ID+1)
		}
		return true
	})
	f.Sink()
	return f
}

// Slice extracts the backward slice of v at the top-level point after
// statement at: the smallest set of top-level statements, in order, up to
// and including at, that holds every statement defining or reassigning v
// and, transitively, every statement defining or reassigning a variable a
// member reads. The fragment computes v's value at that point from the
// parameters. Slice reports false if statement at neither defines nor
// reassigns v, if a store occurs at or before at (memory reads would depend
// on it), or if the slice holds a return (it could exit before computing v).
func Slice(f *tir.Func, at int, v tir.Var) (*Fragment, bool) {
	if at < 0 || at >= len(f.Body) {
		return nil, false
	}
	type info struct {
		reads, writes map[int]bool
		store, ret    bool
		def           int // the top-level variable defined, or -1
	}
	infos := make([]info, at+1)
	for i, s := range f.Body[:at+1] {
		in := info{reads: map[int]bool{}, writes: map[int]bool{}, def: -1}
		if a, ok := s.(*tir.Assign); ok && a.Define {
			in.def = a.Dst.ID
		}
		local := map[int]bool{}
		tir.Walk([]tir.Stmt{s}, func(t tir.Stmt, _ int) bool {
			switch t := t.(type) {
			case *tir.Store:
				in.store = true
			case *tir.Return:
				in.ret = true
			case *tir.Sink:
				return true
			case *tir.Assign:
				if !t.Define && !local[t.Dst.ID] {
					in.writes[t.Dst.ID] = true
				}
			}
			for _, d := range tir.Defs(t) {
				local[d.ID] = true
			}
			for _, u := range tir.Uses(t) {
				if !local[u.ID] && !f.IsParam(u) {
					in.reads[u.ID] = true
				}
			}
			return true
		})
		if in.store {
			return nil, false
		}
		infos[i] = in
	}

	if infos[at].def != v.ID && !infos[at].writes[v.ID] {
		return nil, false
	}
	member := make([]bool, at+1)
	need := map[int]bool{v.ID: true}
	for changed := true; changed; {
		changed = false
		for i := range at + 1 {
			if member[i] {
				continue
			}
			in := infos[i]
			hit := in.def >= 0 && need[in.def]
			for id := range in.writes {
				hit = hit || need[id]
			}
			if hit {
				member[i] = true
				changed = true
				for id := range in.reads {
					need[id] = true
				}
				for id := range in.writes {
					need[id] = true
				}
			}
		}
	}
	fr := &Fragment{Result: v}
	for i, s := range f.Body[:at+1] {
		if member[i] {
			if infos[i].ret {
				return nil, false
			}
			fr.Stmts = append(fr.Stmts, tir.CloneStmt(s))
		}
	}
	fr.Stmts = stripSinks(fr.Stmts)
	fr.Hash = tir.HashStmts(len(f.Sig.Params), fr.program())
	return fr, true
}

// stripSinks removes repair sinks, which are not part of a computation.
func stripSinks(ss []tir.Stmt) []tir.Stmt {
	return slices.DeleteFunc(ss, func(s tir.Stmt) bool {
		switch s := s.(type) {
		case *tir.Sink:
			return true
		case *tir.Loop:
			s.Body = stripSinks(s.Body)
		case *tir.If:
			s.Then = stripSinks(s.Then)
			s.Else = stripSinks(s.Else)
		}
		return false
	})
}
