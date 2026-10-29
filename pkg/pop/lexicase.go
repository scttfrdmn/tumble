// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import "math/rand/v2"

// Lexicase selects one parent (Spector & Helmuth 2015; PROJECT.md §4.5): the
// eligible candidates are filtered through the test cases in a random order,
// keeping on each case those that pass it if any do; the survivors are then
// filtered to the lowest abstract cost, then the highest novelty, and one of
// those is drawn uniformly. cands must be non-empty and eligible.
func Lexicase(r *rand.Rand, cands []*Individual) *Individual {
	pool := append([]*Individual(nil), cands...)
	n := len(pool[0].C)
	for _, t := range r.Perm(n) {
		if len(pool) == 1 {
			break
		}
		kept := pool[:0:0]
		for _, c := range pool {
			if c.C[t] {
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 {
			pool = kept
		}
	}
	pool = keepBest(pool, func(c *Individual) int64 { return -int64(c.Cost) })
	pool = keepBest(pool, func(c *Individual) int64 { return int64(c.Novelty) })
	return pool[r.IntN(len(pool))]
}

// keepBest keeps the candidates with the highest key.
func keepBest(pool []*Individual, key func(*Individual) int64) []*Individual {
	best := key(pool[0])
	for _, c := range pool[1:] {
		best = max(best, key(c))
	}
	out := pool[:0:0]
	for _, c := range pool {
		if key(c) == best {
			out = append(out, c)
		}
	}
	return out
}
