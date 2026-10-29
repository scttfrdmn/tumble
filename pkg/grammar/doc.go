// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Package grammar defines a run grammar — allowed statement forms, length and
// nesting bounds, type set, call whitelist, constant ranges — and a random genome
// generator driven by it. The grammar is the only place design knowledge enters
// a run (PROJECT.md §2).
package grammar
