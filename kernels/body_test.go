// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package kernels

import (
	"testing"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/pop"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

const bitLoopSeed = `package p

func Popcount(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		x := xs[i]
		c := 0
		for j := 0; j < 64; j++ {
			c += int(x >> uint64(j) & 1)
		}
		n += c
	}
	return n
}
`

// TestBodySubstitutionEndToEnd: seeded with a correct genome that counts
// bits one at a time in an inner loop, the giri pipeline probes the inner
// loop's per-iteration values, seeds bits.OnesCount64 as a library donor
// for them (§5.3), and body substitution (#66) replaces the loop, so the
// best cost falls far below the seed's.
func TestBodySubstitutionEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the giri evaluator")
	}
	k, _ := Get("popcount")
	prob, err := k.Problem()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := tir.Parse([]byte(bitLoopSeed), "Popcount")
	if err != nil {
		t.Fatal(err)
	}
	prob.Reference = seed
	p := pop.Defaults()
	p.Islands, p.Population, p.Generations, p.Migrants = 1, 16, 4, 0
	p.Substitution = 1
	var first, last uint64
	bodySubs, bodyIdx := 0, 0
	_, err = pop.Run(eval.NewGiri(eval.Config{}), prob, p, t.TempDir(), func(rd *pop.Readings, a *pop.Archive) error {
		if rd.Gen == 0 {
			first = rd.BestCost
		}
		last = rd.BestCost
		bodySubs += rd.BodySubstitutions
		bodyIdx = rd.BodyIndexSize
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("best cost %d -> %d; body index %d fingerprints; %d body substitutions", first, last, bodyIdx, bodySubs)
	if bodyIdx == 0 || bodySubs == 0 {
		t.Fatalf("body index %d, body substitutions %d", bodyIdx, bodySubs)
	}
	if last*2 > first {
		t.Errorf("best cost %d -> %d: the inner loop was not replaced", first, last)
	}
}
