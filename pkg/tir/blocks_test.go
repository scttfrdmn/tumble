// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"strings"
	"testing"
)

const bitLoopSrc = `package p

func f(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		x := xs[i]
		c := 0
		for j := 0; j < 64; j++ {
			c += int(x >> uint64(j) & 1)
		}
		n += c
	}
	return n
}
`

func TestBlockPoints(t *testing.T) {
	f, err := Parse([]byte(bitLoopSrc), "f")
	if err != nil {
		t.Fatal(err)
	}
	names := f.Names()
	var got []string
	for _, p := range BlockPoints(f) {
		b := f.Block(p.Path)
		if b == nil || p.At >= len(*b) {
			t.Fatalf("point %+v does not address a statement", p)
		}
		got = append(got, names[p.V.ID])
	}
	// In the outer body: x (v1) and, after the inner loop, c (v2); c's own
	// definition is reassigned by the inner loop, so it is not a point.
	// Inner body: its definitions (the shift's temporaries).
	joined := strings.Join(got, " ")
	if !strings.HasPrefix(joined, "v1 v2") {
		t.Errorf("outer body points = %v, want v1 then v2 first", got)
	}
	for _, p := range BlockPoints(f) {
		if len(p.Path) == 1 && names[p.V.ID] == "v2" {
			if _, isLoop := (*f.Block(p.Path))[p.At].(*Loop); !isLoop {
				t.Error("c's point is not after the inner loop")
			}
			vis := map[string]bool{}
			for _, v := range f.VisibleAt(p.Path, p.At) {
				vis[names[v.ID]] = true
			}
			for _, want := range []string{"xs", "v0", "i0", "v1", "v2"} {
				if !vis[want] {
					t.Errorf("%s not visible after the inner loop: %v", want, vis)
				}
			}
			if vis["i1"] {
				t.Error("the inner loop's variable is visible after it")
			}
		}
	}
}

func TestLines(t *testing.T) {
	f, _ := Parse([]byte(bitLoopSrc), "f")
	src := PrintFile("p", []Named{{Name: "f", F: f}})
	lines := Lines(src, "f", f)
	n := 0
	Walk(f.Body, func(s Stmt, _ int) bool { n++; return true })
	if len(lines) != n {
		t.Fatalf("%d statements mapped, %d in the function", len(lines), n)
	}
	text := strings.Split(string(src), "\n")
	outer := f.Body[1].(*Loop)
	if l := lines[outer]; l[1] <= l[0] || !strings.Contains(text[l[0]-1], "for i0") {
		t.Errorf("outer loop lines %v: %q", l, text[l[0]-1])
	}
}
