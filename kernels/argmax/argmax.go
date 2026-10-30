// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Package argmax is the reference kernel for argmax(xs []int32) int: the
// index of the first largest element of xs, or -1 if xs is empty.
//
// The native stage (pkg/native) copies this directory and replaces this file
// with a genome printed as Argmax, so the tests and benchmarks in
// argmax_test.go run against the genome unchanged.
package argmax

// Argmax returns the index of the first largest element of xs, or -1 if xs
// is empty.
func Argmax(xs []int32) int {
	idx := -1
	var best int32
	for i, x := range xs {
		if idx < 0 || x > best {
			best = x
			idx = i
		}
	}
	return idx
}
