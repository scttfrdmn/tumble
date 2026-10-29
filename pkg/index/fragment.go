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

// Func is the fragment as a function over sig returning its result, sunk so
// that it type-checks: the form in which a fragment can itself be evaluated
// for its cost. It is a genome when the result has sig's result type.
func (fr *Fragment) Func(sig *tir.Signature) *tir.Func {
	f := tir.NewFunc(sig)
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

// Slice extracts the backward slice of v, a top-level definition of f. The
// slice is the smallest set of top-level statements, in order, that holds
// v's definition and, for every variable a member reads, its definition and
// every loop or if that reassigns it. It is not closed over the parameters,
// and Slice reports false, if v is not a top-level definition, if a store
// precedes v's definition (memory reads would depend on it), or if the slice
// holds a return (it could exit before computing v).
func Slice(f *tir.Func, v tir.Var) (*Fragment, bool) {
	at := -1
	for i, s := range f.Body {
		if a, ok := s.(*tir.Assign); ok && a.Define && a.Dst == v {
			at = i
		}
	}
	if at < 0 {
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

	member := make([]bool, at+1)
	member[at] = true
	need := map[int]bool{}
	for id := range infos[at].reads {
		need[id] = true
	}
	for changed := true; changed; {
		changed = false
		for i := range at {
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
