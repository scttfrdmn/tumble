// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Package native runs the native benchmark stage on elites: go test -bench in
// a subprocess, parsed with benchstat into ns/op with an interval
// (PROJECT.md §4.3, stage 4).
package native
