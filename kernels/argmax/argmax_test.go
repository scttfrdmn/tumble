// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package argmax

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
)

// naive finds the first index of the maximum with two passes; it is the
// oracle for the fuzz target and the vector check.
func naive(xs []int32) int {
	if len(xs) == 0 {
		return -1
	}
	m := slices.Max(xs)
	return slices.Index(xs, m)
}

func TestArgmax(t *testing.T) {
	cases := []struct {
		xs   []int32
		want int
	}{
		{nil, -1},
		{[]int32{}, -1},
		{[]int32{5}, 0},
		{[]int32{1, 3, 3, 2}, 1},
		{[]int32{-4, -1, -1}, 1},
		{[]int32{2, 2, 2}, 0},
		{[]int32{-2147483648, -2147483648}, 0},
	}
	for _, c := range cases {
		in := slices.Clone(c.xs)
		if got := Argmax(c.xs); got != c.want {
			t.Errorf("Argmax(%v) = %d, want %d", c.xs, got, c.want)
		}
		if !slices.Equal(in, c.xs) {
			t.Errorf("Argmax modified its input %v -> %v", in, c.xs)
		}
	}
}

func TestVectors(t *testing.T) {
	sel, hold, err := Vectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, xs := range append(sel, hold...) {
		in := slices.Clone(xs)
		if got, want := Argmax(xs), naive(xs); got != want {
			t.Errorf("Argmax(%v) = %d, naive = %d", xs, got, want)
		}
		if !slices.Equal(in, xs) {
			t.Errorf("Argmax modified its input %v -> %v", in, xs)
		}
	}
	if len(hold) == 0 || len(sel) <= len(EdgeCases()) {
		t.Fatalf("%d selection, %d holdout vectors", len(sel), len(hold))
	}
	inSel := map[[32]byte]bool{}
	for _, xs := range sel {
		inSel[hashInts(xs)] = true
	}
	for _, xs := range hold {
		if inSel[hashInts(xs)] {
			t.Errorf("holdout vector %v is also a selection vector", xs)
		}
	}
}

func BenchmarkArgmax(b *testing.B) {
	for _, n := range []int{16, 1024, 65536} {
		r := rand.New(rand.NewPCG(1, uint64(n)))
		xs := make([]int32, n)
		for i := range xs {
			xs[i] = int32(r.Uint32())
		}
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			for b.Loop() {
				Argmax(xs)
			}
		})
	}
}

func FuzzArgmax(f *testing.F) {
	for _, xs := range EdgeCases() {
		b := make([]byte, 4*len(xs))
		for i, x := range xs {
			for j := range 4 {
				b[4*i+j] = byte(uint32(x) >> (8 * j))
			}
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		xs := Ints(data)
		if got, want := Argmax(xs), naive(xs); got != want {
			t.Fatalf("Argmax(%v) = %d, naive = %d", xs, got, want)
		}
	})
}
