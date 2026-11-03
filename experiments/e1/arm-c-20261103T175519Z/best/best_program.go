// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Package popcount is the reference kernel for popcount(xs []uint64) int: the
// total number of set bits in xs.
//
// The native stage (pkg/native) copies this directory and replaces this file
// with a genome printed as Popcount, so the tests and benchmarks in
// popcount_test.go run against the genome unchanged.
package popcount

import "math/bits"

// Popcount returns the number of set bits across all words of xs.
// One popcount intrinsic per word, unrolled 16/8/4/2/1 to cut loop overhead.
func Popcount(xs []uint64) int {
	l := len(xs)
	if l >= 16 {
		n := bits.OnesCount64(xs[0]) + bits.OnesCount64(xs[1]) +
			bits.OnesCount64(xs[2]) + bits.OnesCount64(xs[3]) +
			bits.OnesCount64(xs[4]) + bits.OnesCount64(xs[5]) +
			bits.OnesCount64(xs[6]) + bits.OnesCount64(xs[7]) +
			bits.OnesCount64(xs[8]) + bits.OnesCount64(xs[9]) +
			bits.OnesCount64(xs[10]) + bits.OnesCount64(xs[11]) +
			bits.OnesCount64(xs[12]) + bits.OnesCount64(xs[13]) +
			bits.OnesCount64(xs[14]) + bits.OnesCount64(xs[15])
		if l == 16 {
			return n
		}
		return n + Popcount(xs[16:])
	}
	// At most 15 words: a binary ladder with direct returns.
	if l >= 8 {
		n := bits.OnesCount64(xs[0]) + bits.OnesCount64(xs[1]) +
			bits.OnesCount64(xs[2]) + bits.OnesCount64(xs[3]) +
			bits.OnesCount64(xs[4]) + bits.OnesCount64(xs[5]) +
			bits.OnesCount64(xs[6]) + bits.OnesCount64(xs[7])
		if l >= 12 {
			n += bits.OnesCount64(xs[8]) + bits.OnesCount64(xs[9]) +
				bits.OnesCount64(xs[10]) + bits.OnesCount64(xs[11])
			if l == 12 {
				return n
			}
			if l == 13 {
				return n + bits.OnesCount64(xs[12])
			}
			n += bits.OnesCount64(xs[12]) + bits.OnesCount64(xs[13])
			if l == 14 {
				return n
			}
			return n + bits.OnesCount64(xs[14])
		}
		if l >= 10 {
			n += bits.OnesCount64(xs[8]) + bits.OnesCount64(xs[9])
			if l == 11 {
				return n + bits.OnesCount64(xs[10])
			}
			return n
		}
		if l == 9 {
			return n + bits.OnesCount64(xs[8])
		}
		return n
	}
	if l >= 4 {
		n := bits.OnesCount64(xs[0]) + bits.OnesCount64(xs[1]) +
			bits.OnesCount64(xs[2]) + bits.OnesCount64(xs[3])
		if l >= 6 {
			n += bits.OnesCount64(xs[4]) + bits.OnesCount64(xs[5])
			if l == 7 {
				return n + bits.OnesCount64(xs[6])
			}
			return n
		}
		if l == 5 {
			return n + bits.OnesCount64(xs[4])
		}
		return n
	}
	if l == 1 {
		return bits.OnesCount64(xs[0])
	}
	if l == 0 {
		return 0
	}
	n := bits.OnesCount64(xs[0]) + bits.OnesCount64(xs[1])
	if l == 3 {
		return n + bits.OnesCount64(xs[2])
	}
	return n
}
