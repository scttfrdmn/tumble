// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package grammar

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

func TestGrammarsCheck(t *testing.T) {
	for _, g := range []*Grammar{Popcount(), Argmax()} {
		if err := g.Check(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGrammarJSONRoundTrip(t *testing.T) {
	g := Popcount()
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var h Grammar
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	c, _ := json.Marshal(&h)
	if !bytes.Equal(b, c) {
		t.Fatalf("grammar JSON is not a round trip:\n%s\n%s", b, c)
	}
}

func TestRandomDeterministic(t *testing.T) {
	g := Popcount()
	a := Gen{G: g, R: rand.New(rand.NewPCG(7, 7))}.Ramped(50)
	b := Gen{G: g, R: rand.New(rand.NewPCG(7, 7))}.Ramped(50)
	for i := range a {
		if a[i].Identity() != b[i].Identity() {
			t.Fatalf("genome %d differs between runs with one seed", i)
		}
	}
}

func TestRandomValidAndAdmitted(t *testing.T) {
	for name, g := range map[string]*Grammar{"popcount": Popcount(), "argmax": Argmax()} {
		t.Run(name, func(t *testing.T) { randomValidAndAdmitted(t, g) })
	}
}

func randomValidAndAdmitted(t *testing.T, g *Grammar) {
	gen := Gen{G: g, R: rand.New(rand.NewPCG(1, 2))}
	lens := map[int]int{}
	for i := range 2000 {
		size := g.InitMinLen + i%(g.MaxLen-g.InitMinLen+1)
		f := gen.Random(size)
		if err := f.Validate(); err != nil {
			t.Fatalf("genome %d does not validate: %v\n%s", i, err, f.Print("f"))
		}
		if err := g.Admits(f); err != nil {
			t.Fatalf("genome %d outside the grammar: %v\n%s", i, err, f.Print("f"))
		}
		lens[f.Len()]++
	}
	if len(lens) < 10 {
		t.Errorf("only %d distinct lengths in 2000 genomes", len(lens))
	}
}

func TestRampedLengths(t *testing.T) {
	g := Popcount()
	fs := Gen{G: g, R: rand.New(rand.NewPCG(3, 4))}.Ramped(64)
	first, last := fs[0].Len(), fs[len(fs)-1].Len()
	if first > g.InitMinLen || last <= first {
		t.Errorf("ramp from %d to %d statements", first, last)
	}
}

func TestAdmitsRejects(t *testing.T) {
	g := Popcount()
	g.Calls = g.Calls[1:] // drop OnesCount64
	src := []byte(`package p

import "math/bits"

func f(xs []uint64) int { return bits.OnesCount64(xs[0]) }
`)
	f, err := tir.Parse(src, "f")
	if err != nil {
		t.Fatal(err)
	}
	if g.Admits(f) == nil {
		t.Error("call outside the whitelist admitted")
	}
}

func TestScopeAccumulators(t *testing.T) {
	src := []byte(`package p

func f(xs []uint64) int {
	n := 0
	m := len(xs)
	for i := 0; i < m; i++ {
		k := 1
		_ = k
	}
	return n
}
`)
	f, err := tir.Parse(src, "f")
	if err != nil {
		t.Fatal(err)
	}
	var inLoop *Scope
	Walk(f, func(block []tir.Stmt, i int, sc *Scope) {
		if sc.Depth() == 1 && i == len(block) {
			inLoop = sc
		}
	})
	if inLoop == nil {
		t.Fatal("no insertion point inside the loop")
	}
	accs := inLoop.Accumulators()
	if len(accs) != 1 || accs[0].ID != 1 {
		t.Errorf("accumulators in loop = %v, want only n (the bound m and loop-local k excluded)", accs)
	}
}
