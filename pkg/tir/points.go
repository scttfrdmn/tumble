// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

// Point is a top-level program point: the value of V just after the
// top-level statement At. A point is where a fragment is taken
// (pkg/index) and where equivalence substitution can act (pkg/ops).
type Point struct {
	At int
	V  Var
}

// Writes are the variables a statement assigns that were defined outside
// it: reassignments of accumulators, at any depth inside a loop or if.
func Writes(s Stmt) []Var {
	var out []Var
	seen := map[int]bool{}
	local := map[int]bool{}
	Walk([]Stmt{s}, func(t Stmt, _ int) bool {
		for _, d := range Defs(t) {
			local[d.ID] = true
		}
		if a, ok := t.(*Assign); ok && !a.Define && !local[a.Dst.ID] && !seen[a.Dst.ID] {
			seen[a.Dst.ID] = true
			out = append(out, a.Dst)
		}
		return true
	})
	return out
}

// Points are f's top-level program points, in order: after each top-level
// definition of a non-slice local, the point for that local; after each
// top-level loop or if, a point for each variable it reassigns. A point is
// listed only if no later top-level statement reassigns its variable, so
// every read of the variable after the point sees the point's value.
func Points(f *Func) []Point {
	lastWrite := map[int]int{}
	for i, s := range f.Body {
		for _, w := range Writes(s) {
			lastWrite[w.ID] = i
		}
	}
	var out []Point
	add := func(i int, v Var) {
		if j, ok := lastWrite[v.ID]; (!ok || j <= i) && !v.T.Slice && !f.IsParam(v) {
			out = append(out, Point{At: i, V: v})
		}
	}
	for i, s := range f.Body {
		switch s := s.(type) {
		case *Assign:
			if s.Define {
				add(i, s.Dst)
			}
		case *Loop, *If:
			for _, w := range Writes(s) {
				add(i, w)
			}
		}
	}
	return out
}

// ReadAfter reports whether a statement after top-level index at reads v.
func ReadAfter(f *Func, at int, v Var) bool {
	found := false
	Walk(f.Body[at+1:], func(s Stmt, _ int) bool {
		if _, sink := s.(*Sink); sink {
			return true
		}
		for _, u := range Uses(s) {
			if u.ID == v.ID {
				found = true
			}
		}
		return !found
	})
	return found
}
