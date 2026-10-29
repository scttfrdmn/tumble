// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// TestRandomGenomesProperty checks, for 10,000 genomes from the random
// generator: the printed form type-checks under go/types; print → parse →
// print is a fixpoint; the structural hash and identity survive the round
// trip. Ten genomes share each type-checked file to keep the test fast.
func TestRandomGenomesProperty(t *testing.T) {
	n := 10000
	if testing.Short() {
		n = 1000
	}
	const shards, batch = 8, 10
	for shard := range shards {
		t.Run(fmt.Sprint(shard), func(t *testing.T) {
			t.Parallel()
			g := grammar.Popcount()
			gen := grammar.Gen{G: g, R: rand.New(rand.NewPCG(2026, uint64(shard)))}
			for start := shard * n / shards; start < (shard+1)*n/shards; start += batch {
				checkBatch(t, g, gen, start, batch)
			}
		})
	}
}

func checkBatch(t *testing.T, g *grammar.Grammar, gen grammar.Gen, start, batch int) {
	t.Helper()
	var fns []tir.Named
	for i := range batch {
		size := g.InitMinLen + (start+i)%(g.MaxLen-g.InitMinLen+1)
		fns = append(fns, tir.Named{Name: "g" + string(rune('a'+i)), F: gen.Random(size)})
	}
	src := tir.PrintFile("p", fns)
	if err := tir.CheckSource(src); err != nil {
		t.Fatalf("genomes %d..%d do not type-check: %v\n%s", start, start+batch-1, err, src)
	}
	for _, nf := range fns {
		g2, err := tir.Parse(src, nf.Name)
		if err != nil {
			t.Fatalf("reparse %s: %v\n%s", nf.Name, err, nf.F.Print(nf.Name))
		}
		if a, b := nf.F.Print(nf.Name), g2.Print(nf.Name); !bytes.Equal(a, b) {
			t.Fatalf("not a fixpoint:\n%s\nvs\n%s", a, b)
		}
		if nf.F.StructHash() != g2.StructHash() {
			t.Fatalf("structural hash changed across print-parse:\n%s", nf.F.Print(nf.Name))
		}
	}
}
