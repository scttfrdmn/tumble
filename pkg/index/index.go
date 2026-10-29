// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"bytes"
	"cmp"
	"reflect"
	"slices"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// HashCons is the population-wide table of fragments by structural hash
// (PROJECT.md §5.1): identical fragments from any genome and generation share
// one entry.
type HashCons struct {
	frags map[tir.Hash]*Fragment
}

// NewHashCons returns an empty table.
func NewHashCons() *HashCons { return &HashCons{frags: map[tir.Hash]*Fragment{}} }

// Intern returns the table's fragment for fr's hash, adding fr if it is new.
func (h *HashCons) Intern(fr *Fragment) (canonical *Fragment, added bool) {
	if c, ok := h.frags[fr.Hash]; ok {
		return c, false
	}
	h.frags[fr.Hash] = fr
	return fr, true
}

// Get looks a fragment up by hash.
func (h *HashCons) Get(hash tir.Hash) (*Fragment, bool) {
	fr, ok := h.frags[hash]
	return fr, ok
}

// Len is the number of distinct fragments.
func (h *HashCons) Len() int { return len(h.frags) }

// Entry is one fragment known to compute a fingerprint.
type Entry struct {
	Cost     uint64   // abstract cost of computing the fragment, summed over the test vectors
	Fragment tir.Hash // its structural hash; the fragment is in the HashCons table
	Genome   uint64   // the genome it was first seen in
}

// Index is the semantic fingerprint index (PROJECT.md §5.3): fingerprint →
// fragments computing it, kept sorted by cost. It also remembers each
// fragment's fingerprint, which is how a fragment in a child that has not
// been evaluated yet is looked up: by its structural hash.
type Index struct {
	HC     *HashCons
	byFP   map[Fingerprint][]Entry
	fpOf   map[tir.Hash]Fingerprint
	costOf map[tir.Hash]uint64
}

// New returns an empty index.
func New() *Index {
	return &Index{
		HC:     NewHashCons(),
		byFP:   map[Fingerprint][]Entry{},
		fpOf:   map[tir.Hash]Fingerprint{},
		costOf: map[tir.Hash]uint64{},
	}
}

// Len is the number of distinct fingerprints.
func (ix *Index) Len() int { return len(ix.byFP) }

// Empty reports whether the index holds no fragments.
func (ix *Index) Empty() bool { return len(ix.byFP) == 0 }

// Has reports whether fp is in the index.
func (ix *Index) Has(fp Fingerprint) bool { _, ok := ix.byFP[fp]; return ok }

// Lookup returns the entries for fp, cheapest first. The slice is shared;
// do not modify it.
func (ix *Index) Lookup(fp Fingerprint) []Entry { return ix.byFP[fp] }

// FingerprintOf returns the fingerprint recorded for a fragment hash.
func (ix *Index) FingerprintOf(h tir.Hash) (Fingerprint, bool) {
	fp, ok := ix.fpOf[h]
	return fp, ok
}

// Add records that fr computes fp at the given cost, seen first in genome.
// A fragment already recorded keeps its first fingerprint, cost, and genome:
// evaluation is deterministic, so a later sighting agrees with the first, and
// a disagreement is a collision for Collisions to find, not a reason to
// overwrite. Entries are ordered by cost, then fragment hash, so the order
// does not depend on the order fragments were added.
func (ix *Index) Add(fp Fingerprint, fr *Fragment, cost, genome uint64) {
	fr, _ = ix.HC.Intern(fr)
	if _, ok := ix.fpOf[fr.Hash]; ok {
		return
	}
	ix.fpOf[fr.Hash] = fp
	ix.costOf[fr.Hash] = cost
	es := append(ix.byFP[fp], Entry{Cost: cost, Fragment: fr.Hash, Genome: genome})
	slices.SortFunc(es, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Cost, b.Cost), bytes.Compare(a.Fragment[:], b.Fragment[:]))
	})
	ix.byFP[fp] = es
}

// Novelty is the number of fps not yet in the index (PROJECT.md §4.5),
// counting each distinct fingerprint once.
func (ix *Index) Novelty(fps []Fingerprint) int {
	seen := map[Fingerprint]bool{}
	n := 0
	for _, fp := range fps {
		if !seen[fp] && !ix.Has(fp) {
			n++
		}
		seen[fp] = true
	}
	return n
}

// Cheaper implements ops.Donor: v's backward slice in f is looked up by its
// structural hash; if the index has seen that fragment, and the cheapest
// fragment with the same fingerprint costs less, that fragment is returned.
// A slice the index has never seen has no known fingerprint and gets
// nothing.
func (ix *Index) Cheaper(f *tir.Func, v tir.Var) ([]tir.Stmt, tir.Var, bool) {
	fr, ok := Slice(f, v)
	if !ok {
		return nil, tir.Var{}, false
	}
	fp, ok := ix.fpOf[fr.Hash]
	if !ok {
		return nil, tir.Var{}, false
	}
	best := ix.byFP[fp][0]
	if best.Fragment == fr.Hash || best.Cost >= ix.costOf[fr.Hash] {
		return nil, tir.Var{}, false
	}
	donor, _ := ix.HC.Get(best.Fragment)
	if donor.Result.T != v.T {
		return nil, tir.Var{}, false
	}
	return tir.CloneStmts(donor.Stmts), donor.Result, true
}

// Collision is a fingerprint whose fragments disagree on some test vector.
type Collision struct {
	Fingerprint Fingerprint
	A, B        tir.Hash
	Vector      int
}

// Collisions checks every fingerprint's fragments against their outputs on
// the test vectors (outputs[hash][vector]); fragments with no outputs given
// are skipped. Equal fingerprints must mean equal outputs on every vector:
// any pair that disagrees is a collision. This is the collision detector of
// §5.3 as a check that can run over a whole index.
func (ix *Index) Collisions(outputs map[tir.Hash][]any) []Collision {
	var out []Collision
	fps := make([]Fingerprint, 0, len(ix.byFP))
	for fp := range ix.byFP {
		fps = append(fps, fp)
	}
	slices.SortFunc(fps, func(a, b Fingerprint) int { return bytes.Compare(a[:], b[:]) })
	for _, fp := range fps {
		es := ix.byFP[fp]
		var ref *Entry
		for i := range es {
			o, ok := outputs[es[i].Fragment]
			if !ok {
				continue
			}
			if ref == nil {
				ref = &es[i]
				continue
			}
			r := outputs[ref.Fragment]
			for t := range max(len(o), len(r)) {
				if t >= len(o) || t >= len(r) || !reflect.DeepEqual(o[t], r[t]) {
					out = append(out, Collision{Fingerprint: fp, A: ref.Fragment, B: es[i].Fragment, Vector: t})
					break
				}
			}
		}
	}
	return out
}
