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
func Popcount(xs []uint64) int {
	n := 0
	for _, x := range xs {
		n += bits.OnesCount64(x)
	}
	return n
}
