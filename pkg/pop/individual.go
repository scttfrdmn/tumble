// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import (
	"bytes"
	"cmp"
	"fmt"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Origin is how an individual was made.
type Origin int

// Origins: the generation-0 random genomes, the reference seed, a migrant,
// or a child of an operator.
const (
	Random Origin = iota
	Seed
	Migrant
	FromCrossover
	FromMutation
	FromSubstitution
)

var originNames = []string{"random", "seed", "migrant", "crossover", "mutation", "substitution"}

func (o Origin) String() string { return originNames[o] }

// MarshalText writes the origin's name.
func (o Origin) MarshalText() ([]byte, error) { return []byte(o.String()), nil }

// UnmarshalText reads an origin's name.
func (o *Origin) UnmarshalText(b []byte) error {
	for i, n := range originNames {
		if n == string(b) {
			*o = Origin(i)
			return nil
		}
	}
	return fmt.Errorf("pop: unknown origin %q", b)
}

// Individual is one evaluated genome.
type Individual struct {
	ID     uint64
	F      *tir.Func
	Origin Origin
	Island int
	Gen    int

	// Discard is the fitness-cascade stage that rejected the genome ("" if
	// none): "violation" or "exhausted" at the giri stage. Type-check
	// failures never get here; they are discarded by ops.
	Discard string

	C       []bool // per selection vector: output equals the reference
	Correct int    // number of true entries in C
	Cost    uint64 // abstract cost
	Novelty int    // point fingerprints new to the index (set at the barrier)

	vcHits, vcPure uint64 // value-cache readings of the evaluation
	vcMeasured     bool
	steps          uint64 // interpreted instructions of the evaluation

	// parentBest is the best parent's rank key, for attributing
	// improvements to operators; donorC is a substitution donor's
	// correctness, for the collision reading.
	parentBest *Individual
	donorC     []bool
}

// Eligible reports whether ind passed the cascade and may be a parent.
func (ind *Individual) Eligible() bool { return ind.Discard == "" }

// FullyCorrect reports whether ind matches the reference on every vector.
func (ind *Individual) FullyCorrect() bool { return ind.Eligible() && ind.Correct == len(ind.C) }

// better orders individuals by rank: eligible first, then more vectors
// correct, then lower cost, then higher novelty, then identity, which makes
// the order total and independent of evaluation order.
func better(a, b *Individual) int {
	return cmp.Or(
		-cmp.Compare(boolInt(a.Eligible()), boolInt(b.Eligible())),
		-cmp.Compare(a.Correct, b.Correct),
		cmp.Compare(a.Cost, b.Cost),
		-cmp.Compare(a.Novelty, b.Novelty),
		compareIdentity(a.F, b.F),
	)
}

func compareIdentity(a, b *tir.Func) int {
	ia, ib := a.Identity(), b.Identity()
	return bytes.Compare(ia[:], ib[:])
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
