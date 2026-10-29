// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"bytes"
	"go/format"
	"go/token"
	"os"
	"strings"
	"testing"
)

func reference(t *testing.T) *Func {
	t.Helper()
	src, err := os.ReadFile("../../kernels/popcount/popcount.go")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(src, "Popcount")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// seeds are hand-written genomes covering every statement and expression
// form; the generator-driven property tests live with pkg/grammar.
const seeds = `package s

import "math/bits"

func a(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		x := xs[i]
		for x != 0 {
			break
		}
		_ = x
	}
	return n
}

func b(xs []uint64) int {
	var n int
	for i := range xs {
		x := xs[i]
		for x := x; x != 0; x >>= 1 {
		}
		n += int(x & 1)
	}
	return n
}

func c(xs []uint64) int {
	n := 0
	m := len(xs) / 2
	for i := 1; i < m+1; i += 2 {
		y := xs[i] ^ 0xffffffffffffffff
		if i > 3 && y != 0 {
			n -= bits.OnesCount64(y)
		} else if !(y == 0) {
			n = max(n, -7)
		} else {
			xs[i] = y << 3
		}
		n = min(n, i, 100)
	}
	return -n
}

func d(xs []uint64) int {
	ok := true
	k := uint64(77)
	for _, x := range xs {
		if x > k {
			ok = !ok
		}
	}
	if ok {
		return 1
	}
	return ^len(xs)
}
`

// fixpointSeeds parse cleanly (a and b use forms the parser rejects on
// purpose, and are checked in TestParseRejects).
var fixpointSeeds = []string{"c", "d"}

func TestReference(t *testing.T) {
	f := reference(t)
	if err := f.Validate(); err != nil {
		t.Fatalf("reference does not validate: %v", err)
	}
	if err := f.TypeCheck(); err != nil {
		t.Fatalf("reference does not type-check: %v", err)
	}
	got := string(f.Print("Popcount"))
	want := `func Popcount(xs []uint64) int {
	v0 := 0
	for i0 := 0; i0 < len(xs); i0++ {
		v1 := xs[i0]
		v2 := bits.OnesCount64(v1)
		v0 = v0 + v2
	}
	return v0
}`
	if got != want {
		t.Errorf("reference prints as\n%s\nwant\n%s", got, want)
	}
}

func TestPrintParseFixpoint(t *testing.T) {
	for _, name := range fixpointSeeds {
		f, err := Parse([]byte(seeds), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fixpoint(t, f)
	}
	fixpoint(t, reference(t))
}

func fixpoint(t *testing.T, f *Func) {
	t.Helper()
	if err := f.Validate(); err != nil {
		t.Fatalf("does not validate: %v\n%s", err, f.Print("f"))
	}
	src := PrintFile("p", []Named{{"f", f}})
	if err := CheckSource(src); err != nil {
		t.Fatalf("printed form does not type-check: %v\n%s", err, src)
	}
	if fmted, err := format.Source(src); err != nil || !bytes.Equal(fmted, src) {
		t.Fatalf("printed form is not gofmt-stable:\n%s", src)
	}
	g, err := Parse(src, "f")
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, src)
	}
	if again := PrintFile("p", []Named{{"f", g}}); !bytes.Equal(again, src) {
		t.Fatalf("print-parse-print is not a fixpoint:\n%s\nvs\n%s", src, again)
	}
	if f.StructHash() != g.StructHash() || f.Identity() != g.Identity() {
		t.Fatal("hash or identity changed across print-parse")
	}
}

func TestParseRejects(t *testing.T) {
	for _, name := range []string{"a", "b"} {
		if _, err := Parse([]byte(seeds), name); err == nil {
			t.Errorf("%s: parse accepted a form outside the subset", name)
		}
	}
}

// renumber gives every local a new ID, preserving structure.
func renumber(f *Func, offset int) *Func {
	g := f.Clone()
	n := len(f.Sig.Params)
	shift := func(v Var) Var {
		if v.ID >= n {
			v.ID += offset
		}
		return v
	}
	var fix func([]Stmt) []Stmt
	fix = func(ss []Stmt) []Stmt {
		for i, s := range ss {
			s = MapOperands(s, func(o Operand) Operand {
				if v, ok := o.(Var); ok {
					return shift(v)
				}
				return o
			})
			switch s := s.(type) {
			case *Assign:
				s.Dst = shift(s.Dst)
			case *Loop:
				s.Ind = shift(s.Ind)
				s.Body = fix(s.Body)
			case *If:
				s.Then = fix(s.Then)
				s.Else = fix(s.Else)
			}
			ss[i] = s
		}
		return ss
	}
	g.Body = fix(g.Body)
	g.NextID += offset
	return g
}

func TestHashAlphaInvariant(t *testing.T) {
	for _, name := range fixpointSeeds {
		f, _ := Parse([]byte(seeds), name)
		g := renumber(f, 1000)
		if f.StructHash() != g.StructHash() {
			t.Errorf("%s: structural hash changed under renaming", name)
		}
		if !bytes.Equal(f.Print("f"), g.Print("f")) {
			t.Errorf("%s: printed form changed under renaming", name)
		}
	}
	// Different structure, different hash.
	c, _ := Parse([]byte(seeds), "c")
	d, _ := Parse([]byte(seeds), "d")
	if c.StructHash() == d.StructHash() {
		t.Error("distinct genomes hash equal")
	}
}

func TestHashSeesConstantsAndOps(t *testing.T) {
	f := reference(t)
	g := f.Clone()
	loop := g.Body[1].(*Loop)
	loop.Body[2].(*Assign).X.(*Binop).Op = token.SUB
	if f.StructHash() == g.StructHash() {
		t.Error("changing an operator did not change the hash")
	}
	h := f.Clone()
	h.Body[0].(*Assign).X = &Atom{X: IntConst(Int, 1)}
	if f.StructHash() == h.StructHash() {
		t.Error("changing a constant did not change the hash")
	}
	if f.StructHash() != reference(t).StructHash() {
		t.Error("Clone mutated the original")
	}
}

func TestValidateRejects(t *testing.T) {
	sig := &Signature{Params: []Param{{"xs", Uint64s}}, Result: Int}
	cases := map[string]func(f *Func){
		"unused": func(f *Func) {
			v := f.NewVar(Int)
			f.Body = []Stmt{&Assign{Dst: v, Define: true, X: &Atom{X: IntConst(Int, 1)}}, &Return{X: IntConst(Int, 0)}}
		},
		"undefined": func(f *Func) {
			f.Body = []Stmt{&Return{X: Var{ID: 5, T: Int}}}
		},
		"no return": func(f *Func) {
			f.Body = []Stmt{&Store{X: f.Param(0), I: IntConst(Int, 0), Val: Const{T: Uint64}}}
		},
		"reassign outside loop": func(f *Func) {
			v := f.NewVar(Int)
			f.Body = []Stmt{
				&Assign{Dst: v, Define: true, X: &Atom{X: IntConst(Int, 1)}},
				&Assign{Dst: v, X: &Atom{X: IntConst(Int, 2)}},
				&Return{X: v},
			}
		},
		"type mismatch": func(f *Func) {
			v := f.NewVar(Int)
			f.Body = []Stmt{&Assign{Dst: v, Define: true, X: &Index{X: f.Param(0), I: IntConst(Int, 0)}}, &Return{X: v}}
		},
		"division by zero": func(f *Func) {
			v := f.NewVar(Int)
			w := f.NewVar(Int)
			f.Body = []Stmt{
				&Assign{Dst: v, Define: true, X: &Len{X: f.Param(0)}},
				&Assign{Dst: w, Define: true, X: &Binop{Op: token.QUO, X: v, Y: IntConst(Int, 0)}},
				&Return{X: w},
			}
		},
		"bound reassigned": func(f *Func) {
			n := f.NewVar(Int)
			i := f.NewVar(Int)
			f.Body = []Stmt{
				&Assign{Dst: n, Define: true, X: &Len{X: f.Param(0)}},
				&Loop{Ind: i, Lo: IntConst(Int, 0), Hi: &Atom{X: n}, Step: 1, Body: []Stmt{
					&Assign{Dst: n, X: &Binop{Op: token.ADD, X: n, Y: IntConst(Int, 1)}},
				}},
				&Return{X: n},
			}
		},
		"overflow": func(f *Func) {
			v := f.NewVar(Int32)
			w := f.NewVar(Int)
			f.Body = []Stmt{
				&Assign{Dst: v, Define: true, X: &Atom{X: IntConst(Int32, 1<<40)}},
				&Assign{Dst: w, Define: true, X: &Convert{X: v}},
				&Return{X: w},
			}
		},
	}
	for name, build := range cases {
		f := NewFunc(sig)
		build(f)
		if err := f.Validate(); err == nil {
			t.Errorf("%s: validated\n%s", name, f.Print("f"))
		} else if err := f.TypeCheck(); err == nil && !strings.Contains(name, "reassign") && !strings.Contains(name, "bound") {
			// Validate's rules that are not Go's own are tumble's; the rest
			// should also be caught by the type checker.
			t.Errorf("%s: Validate rejects but go/types accepts\n%s", name, f.Print("f"))
		}
	}
}

func TestTypeText(t *testing.T) {
	for _, s := range []string{"int", "bool", "uint64", "[]uint64", "[]int32"} {
		ty, err := ParseType(s)
		if err != nil || ty.String() != s {
			t.Errorf("ParseType(%q) = %v, %v", s, ty, err)
		}
	}
	if _, err := ParseType("float64"); err == nil {
		t.Error("float64 accepted in v0")
	}
}
