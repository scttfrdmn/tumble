// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// BodyFragment is the backward slice of a variable at a point inside a
// nested block (#66): the statements of that block, up to the point, that
// compute the variable from values defined outside the block. Those values
// are its Inputs, the free scalar variables it reads, in first-occurrence
// order; slice parameters are read directly, since every genome shares them.
// Hash is the structural hash of the statements followed by return Result,
// which α-renames the inputs by first occurrence.
type BodyFragment struct {
	Stmts  []tir.Stmt
	Result tir.Var
	Inputs []tir.Var
	Hash   tir.Hash
	// Source are the statements of f the slice was taken from (not
	// copies), for mapping the slice back to f's printed lines.
	Source []tir.Stmt
}

// BodySlice extracts the slice of p.V at p. It reports false if the
// statement at p neither defines nor reassigns p.V, if f stores into memory
// anywhere (element reads inside a loop could depend on it), if the slice
// holds a return, or if it reassigns a variable it does not define (an
// effect beyond computing p.V).
func BodySlice(f *tir.Func, p tir.BlockPoint) (*BodyFragment, bool) {
	b := f.Block(p.Path)
	if b == nil || p.At < 0 || p.At >= len(*b) {
		return nil, false
	}
	stores := false
	tir.Walk(f.Body, func(s tir.Stmt, _ int) bool {
		if _, ok := s.(*tir.Store); ok {
			stores = true
		}
		return true
	})
	if stores {
		return nil, false
	}
	block := (*b)[:p.At+1]
	type info struct {
		reads, writes map[int]bool
		def           int
		ret           bool
	}
	infos := make([]info, len(block))
	for i, s := range block {
		in := info{reads: map[int]bool{}, writes: map[int]bool{}, def: -1}
		if a, ok := s.(*tir.Assign); ok && a.Define {
			in.def = a.Dst.ID
		}
		local := map[int]bool{}
		tir.Walk([]tir.Stmt{s}, func(t tir.Stmt, _ int) bool {
			switch t := t.(type) {
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
		infos[i] = in
	}
	if infos[p.At].def != p.V.ID && !infos[p.At].writes[p.V.ID] {
		return nil, false
	}
	member := make([]bool, len(block))
	need := map[int]bool{p.V.ID: true}
	for changed := true; changed; {
		changed = false
		for i, in := range infos {
			if member[i] {
				continue
			}
			hit := in.def >= 0 && need[in.def]
			for id := range in.writes {
				hit = hit || need[id]
			}
			if !hit {
				continue
			}
			member[i], changed = true, true
			for id := range in.reads {
				need[id] = true
			}
			for id := range in.writes {
				need[id] = true
			}
		}
	}
	fr := &BodyFragment{Result: p.V}
	defined := map[int]bool{}
	for i, s := range block {
		if !member[i] {
			continue
		}
		if infos[i].ret {
			return nil, false
		}
		fr.Stmts = append(fr.Stmts, tir.CloneStmt(s))
		fr.Source = append(fr.Source, s)
		tir.Walk([]tir.Stmt{s}, func(t tir.Stmt, _ int) bool {
			for _, d := range tir.Defs(t) {
				defined[d.ID] = true
			}
			return true
		})
	}
	fr.Stmts = stripSinks(fr.Stmts)
	// Inputs: variables the members read but do not define, in order of
	// first occurrence, which is the order HashStmts α-renames them in.
	seen := map[int]bool{}
	tir.Walk(fr.Stmts, func(t tir.Stmt, _ int) bool {
		for _, u := range tir.Uses(t) {
			if !defined[u.ID] && !f.IsParam(u) && !seen[u.ID] {
				seen[u.ID] = true
				fr.Inputs = append(fr.Inputs, u)
			}
		}
		return true
	})
	// A member that reassigns a variable from outside the slice has an
	// effect beyond computing Result (an outer accumulator, say); copying
	// it elsewhere would repeat that effect.
	escapes := false
	tir.Walk(fr.Stmts, func(t tir.Stmt, _ int) bool {
		if a, ok := t.(*tir.Assign); ok && !a.Define && !defined[a.Dst.ID] {
			escapes = true
		}
		return true
	})
	if escapes {
		return nil, false
	}
	if slices.ContainsFunc(fr.Inputs, func(v tir.Var) bool { return v.T.Slice }) {
		return nil, false
	}
	fr.Hash = tir.HashStmts(len(f.Sig.Params), append(tir.CloneStmts(fr.Stmts), &tir.Return{X: fr.Result}))
	return fr, true
}

// BodyEntry is a fragment known to compute a body-point fingerprint, given
// inputs with the fingerprints InputFP. PerExec is its abstract cost per
// execution of the point, where it was measured.
type BodyEntry struct {
	Fragment *BodyFragment
	InputFP  []Fingerprint
	PerExec  float64
	Genome   uint64
	Library  bool // seeded from an admitted call (§5.3), not from a genome
}

// BodyIndex maps a body point's fingerprint (the per-iteration values its
// variable takes, per test vector) to the fragments known to compute it,
// cheapest first.
type BodyIndex struct {
	byFP map[Fingerprint][]BodyEntry
	seen map[string]bool // fragment hash + input fingerprints
}

// NewBodyIndex returns an empty index.
func NewBodyIndex() *BodyIndex {
	return &BodyIndex{byFP: map[Fingerprint][]BodyEntry{}, seen: map[string]bool{}}
}

// Len is the number of distinct fingerprints.
func (ix *BodyIndex) Len() int { return len(ix.byFP) }

// Empty reports whether the index holds nothing.
func (ix *BodyIndex) Empty() bool { return len(ix.byFP) == 0 }

func entryKey(h tir.Hash, in []Fingerprint) string {
	var b bytes.Buffer
	b.Write(h[:])
	for _, fp := range in {
		b.Write(fp[:])
	}
	return b.String()
}

// Add records e under fp unless the same fragment with the same input
// fingerprints is already recorded. Entries are ordered by PerExec, then
// fragment hash, independent of insertion order.
func (ix *BodyIndex) Add(fp Fingerprint, e BodyEntry) {
	k := entryKey(e.Fragment.Hash, e.InputFP)
	if ix.seen[k] {
		return
	}
	ix.seen[k] = true
	es := append(ix.byFP[fp], e)
	slices.SortFunc(es, func(a, b BodyEntry) int {
		return cmp.Or(cmp.Compare(a.PerExec, b.PerExec), bytes.Compare(a.Fragment.Hash[:], b.Fragment.Hash[:]))
	})
	ix.byFP[fp] = es
}

// Has reports whether fp is in the index.
func (ix *BodyIndex) Has(fp Fingerprint) bool { _, ok := ix.byFP[fp]; return ok }

// Cheaper finds the cheapest entry for fp whose every input binds to a scope
// variable of the same type and fingerprint, and that accept approves given
// the binding. scope lists the recipient's in-scope variables with their
// fingerprints at the point, latest-defined last; an input binds to the
// latest-defined match. It returns the entry and the binding from each
// input's ID to the scope variable it reads.
func (ix *BodyIndex) Cheaper(fp Fingerprint, scope []ScopedFP, accept func(BodyEntry, map[int]tir.Var) bool) (BodyEntry, map[int]tir.Var, bool) {
	for _, e := range ix.byFP[fp] {
		bind := map[int]tir.Var{}
		ok := true
		for i, in := range e.Fragment.Inputs {
			found := false
			for j := len(scope) - 1; j >= 0; j-- {
				if scope[j].V.T == in.T && scope[j].FP == e.InputFP[i] {
					bind[in.ID] = scope[j].V
					found = true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if ok && accept(e, bind) {
			return e, bind, true
		}
	}
	return BodyEntry{}, nil, false
}

// ScopedFP is a variable in scope at a point with its fingerprint there.
type ScopedFP struct {
	V  tir.Var
	FP Fingerprint
}
