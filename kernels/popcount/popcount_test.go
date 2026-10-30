// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package popcount

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
)

// naive counts bits one at a time; it is the oracle for the fuzz target.
func naive(xs []uint64) int {
	n := 0
	for _, x := range xs {
		for ; x != 0; x >>= 1 {
			n += int(x & 1)
		}
	}
	return n
}

func TestPopcount(t *testing.T) {
	cases := []struct {
		xs   []uint64
		want int
	}{
		{nil, 0},
		{[]uint64{}, 0},
		{[]uint64{0}, 0},
		{[]uint64{1}, 1},
		{[]uint64{^uint64(0)}, 64},
		{[]uint64{0xAAAAAAAAAAAAAAAA, 0x5555555555555555}, 64},
		{[]uint64{1, 3, 7}, 6},
	}
	for _, c := range cases {
		if got := Popcount(c.xs); got != c.want {
			t.Errorf("Popcount(%#x) = %d, want %d", c.xs, got, c.want)
		}
	}
}

func TestVectors(t *testing.T) {
	sel, hold, err := Vectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, xs := range append(sel, hold...) {
		if got, want := Popcount(xs), naive(xs); got != want {
			t.Errorf("Popcount(%#x) = %d, naive = %d", xs, got, want)
		}
	}
	if len(sel) < len(EdgeCases()) {
		t.Fatalf("selection has %d vectors, fewer than the %d edge cases", len(sel), len(EdgeCases()))
	}
	inSel := map[[32]byte]bool{}
	for _, xs := range sel {
		inSel[hashWords(xs)] = true
	}
	for _, xs := range hold {
		if inSel[hashWords(xs)] {
			t.Errorf("holdout vector %#x is also a selection vector", xs)
		}
	}
	if len(hold) == 0 {
		t.Error("no holdout vectors")
	}
	sel2, hold2, _ := Vectors()
	if len(sel2) != len(sel) || len(hold2) != len(hold) {
		t.Fatal("Vectors is not deterministic")
	}
}

func BenchmarkPopcount(b *testing.B) {
	for _, n := range []int{16, 1024, 65536} {
		r := rand.New(rand.NewPCG(1, uint64(n)))
		xs := make([]uint64, n)
		for i := range xs {
			xs[i] = r.Uint64()
		}
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			for b.Loop() {
				Popcount(xs)
			}
		})
	}
}

func FuzzPopcount(f *testing.F) {
	for _, xs := range EdgeCases() {
		b := make([]byte, 8*len(xs))
		for i, x := range xs {
			for j := range 8 {
				b[8*i+j] = byte(x >> (8 * j))
			}
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		xs := Words(data)
		in := slices.Clone(xs)
		want := naive(in)
		if got := Popcount(xs); got != want {
			t.Fatalf("Popcount(%#x) = %d, naive = %d", in, got, want)
		}
		if !slices.Equal(xs, in) {
			t.Fatalf("Popcount modified its input %#x -> %#x", in, xs)
		}
	})
}
