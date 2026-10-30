// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

func parents(t *testing.T, n int, seed uint64) (*grammar.Grammar, []*tir.Func) {
	t.Helper()
	g := grammar.Popcount()
	return g, grammar.Gen{G: g, R: rand.New(rand.NewPCG(seed, 99))}.Ramped(n)
}

func parse(t *testing.T, src string) *tir.Func {
	t.Helper()
	f, err := tir.Parse([]byte("package p\n\nimport \"math/bits\"\n\nvar _ = bits.Len\n\n"+src), "f")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// checkChild asserts a surviving child is a legal genome: inside the
// grammar, structurally valid (which includes: no unused local), and accepted
// by go/types.
func checkChild(t *testing.T, g *grammar.Grammar, op string, c *tir.Func) {
	t.Helper()
	if err := g.Admits(c); err != nil {
		t.Fatalf("%s child outside grammar: %v\n%s", op, err, c.Print("f"))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("%s child invalid: %v\n%s", op, err, c.Print("f"))
	}
	if err := tir.CheckSource(tir.PrintFile("p", []tir.Named{{Name: "f", F: c}})); err != nil {
		t.Fatalf("%s child does not type-check: %v\n%s", op, err, c.Print("f"))
	}
}

func TestOperatorsDeterministic(t *testing.T) {
	g, ps := parents(t, 40, 1)
	run := func(seed uint64) []string {
		r := rand.New(rand.NewPCG(seed, 1))
		var out []string
		for i := 0; i+1 < len(ps); i++ {
			if c, err := TwoPoint(r, g, ps[i], ps[i+1]); err == nil {
				out = append(out, string(c.Print("f")))
			} else {
				out = append(out, "discarded")
			}
			if c, err := Point(r, g, ps[i]); err == nil {
				out = append(out, string(c.Print("f")))
			} else {
				out = append(out, "discarded")
			}
			if c, ok, err := Substitute(r, g, callDonor{}, ps[i]); err == nil {
				out = append(out, string(c.Print("f")), map[bool]string{true: "applied"}[ok])
			}
		}
		return out
	}
	a, b := run(5), run(5)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("child %d differs between runs with one seed:\n%s\nvs\n%s", i, a[i], b[i])
		}
	}
	if c := run(6); strings.Join(c, "") == strings.Join(a, "") {
		t.Error("a different seed produced identical children")
	}
}

func TestChildrenTypeCheckOrDiscarded(t *testing.T) {
	g, ps := parents(t, 100, 2)
	r := rand.New(rand.NewPCG(3, 3))
	var y Yield
	for range 1500 {
		a, b := ps[r.IntN(len(ps))], ps[r.IntN(len(ps))]
		c, err := TwoPoint(r, g, a, b)
		y.Record(Crossover, err == nil)
		if err == nil {
			checkChild(t, g, "crossover", c)
		} else if d := (*Discarded)(nil); !errors.As(err, &d) {
			t.Fatalf("crossover failed without a discard report: %v", err)
		}
		c, err = Point(r, g, a)
		y.Record(Mutation, err == nil)
		if err == nil {
			checkChild(t, g, "mutation", c)
		} else if d := (*Discarded)(nil); !errors.As(err, &d) {
			t.Fatalf("mutation failed without a discard report: %v", err)
		}
	}
	for _, k := range []Kind{Crossover, Mutation} {
		if y.Survived[k] == 0 {
			t.Errorf("no %s child survived out of %d", k, y.Attempted[k])
		}
	}
	t.Logf("yield over 1500 attempts each (plumbing check, not a measurement): crossover %.2f, mutation %.2f",
		y.Rate(Crossover), y.Rate(Mutation))
}

func TestRepairRebindsAndSinks(t *testing.T) {
	g := grammar.Popcount()
	f := parse(t, `func f(xs []uint64) int {
	n := len(xs)
	return n
}`)
	// Dangle a use: point the return at an undefined int; add an unused
	// definition.
	ghost := tir.Var{ID: 77, T: tir.Int}
	f.NextID = 80
	u := f.NewVar(tir.Uint64)
	f.Body = []tir.Stmt{
		f.Body[0],
		&tir.Assign{Dst: u, Define: true, X: &tir.Index{X: f.Param(0), I: ghost}},
		&tir.Return{X: ghost},
	}
	if err := Repair(g, f); err != nil {
		t.Fatalf("repair failed: %v\n%s", err, f.Print("f"))
	}
	want := `func f(xs []uint64) int {
	v0 := len(xs)
	v1 := xs[v0]
	_ = v1
	return v0
}`
	if got := string(f.Print("f")); got != want {
		t.Errorf("repaired to\n%s\nwant\n%s", got, want)
	}
}

func TestRepairDropsUnbindable(t *testing.T) {
	g := grammar.Popcount()
	f := parse(t, `func f(xs []uint64) int {
	return 0
}`)
	b := tir.Var{ID: 50, T: tir.Bool}
	f.NextID = 51
	f.Body = []tir.Stmt{
		&tir.If{Cond: b, Then: []tir.Stmt{&tir.Store{X: f.Param(0), I: tir.IntConst(tir.Int, 0), Val: tir.Const{T: tir.Uint64}}}},
		&tir.Return{X: tir.IntConst(tir.Int, 0)},
	}
	if err := Repair(g, f); err != nil {
		t.Fatal(err)
	}
	if f.Len() != 1 {
		t.Errorf("if on an unbindable condition was not dropped:\n%s", f.Print("f"))
	}
}

func TestRepairAccumulatorOutsideLoop(t *testing.T) {
	g := grammar.Popcount()
	f := parse(t, `func f(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		n += 1
	}
	return n
}`)
	// Hoist the reassignment out of the loop, as a crossover can.
	loop := f.Body[1].(*tir.Loop)
	re := loop.Body[0]
	loop.Body = nil
	f.Body = []tir.Stmt{f.Body[0], loop, re, f.Body[2]}
	if err := Repair(g, f); err != nil {
		t.Fatalf("%v\n%s", err, f.Print("f"))
	}
	for _, s := range f.Body {
		if a, ok := s.(*tir.Assign); ok && !a.Define {
			t.Errorf("reassignment outside a loop survived repair:\n%s", f.Print("f"))
		}
	}
}

// callDonor offers bits.OnesCount64(xs[0]) for any variable: it stands in
// for an index that has seen that fragment compute the same fingerprint.
type callDonor struct{}

func (callDonor) Empty() bool { return false }

func (callDonor) Cheaper(f *tir.Func, p tir.Point) ([]tir.Stmt, tir.Var, bool) {
	if p.V.T != tir.Int {
		return nil, tir.Var{}, false
	}
	x := tir.Var{ID: 100, T: tir.Uint64}
	c := tir.Var{ID: 101, T: tir.Int}
	return []tir.Stmt{
		&tir.Assign{Dst: x, Define: true, X: &tir.Index{X: f.Param(0), I: tir.IntConst(tir.Int, 0)}},
		&tir.Assign{Dst: c, Define: true, X: &tir.Call{Pkg: "math/bits", Name: "OnesCount64", Args: []tir.Operand{x}}},
	}, c, true
}

type emptyDonor struct{}

func (emptyDonor) Empty() bool { return true }
func (emptyDonor) Cheaper(*tir.Func, tir.Point) ([]tir.Stmt, tir.Var, bool) {
	panic("Cheaper called on an empty index")
}

func TestSubstituteNoOpOnEmptyIndex(t *testing.T) {
	g, ps := parents(t, 5, 4)
	r := rand.New(rand.NewPCG(1, 1))
	for _, d := range []Donor{nil, emptyDonor{}} {
		c, applied, err := Substitute(r, g, d, ps[3])
		if err != nil || applied || c.Identity() != ps[3].Identity() {
			t.Errorf("substitution with an empty index changed the genome (applied=%v, err=%v)", applied, err)
		}
	}
}

func TestSubstituteReplacesSlice(t *testing.T) {
	g := grammar.Popcount()
	// n is a bit loop over xs[0]; the donor's fragment is the call.
	f := parse(t, `func f(xs []uint64) int {
	x := xs[0]
	n := 0
	for i := 0; i < 64; i++ {
		b := x >> uint64(i)
		n += int(b & 1)
	}
	m := n
	unrelated := len(xs)
	_ = unrelated
	return m
}`)
	for seed := range uint64(5) {
		r := rand.New(rand.NewPCG(seed, 0))
		c, applied, err := Substitute(r, g, copyDonor{}, f)
		if err != nil {
			t.Fatal(err)
		}
		if !applied {
			continue
		}
		src := string(c.Print("f"))
		if strings.Contains(src, "for ") {
			t.Errorf("the replaced slice's loop survived:\n%s", src)
		}
		if !strings.Contains(src, "bits.OnesCount64") || !strings.Contains(src, "len(xs)") {
			t.Errorf("fragment missing or unrelated code removed:\n%s", src)
		}
		checkChild(t, g, "substitution", c)
		return
	}
	t.Fatal("substitution never applied")
}

// copyDonor donates the call fragment only for a variable defined at top
// level as a copy of another variable: m in TestSubstituteReplacesSlice.
type copyDonor struct{}

func (copyDonor) Empty() bool { return false }

func (copyDonor) Cheaper(f *tir.Func, p tir.Point) ([]tir.Stmt, tir.Var, bool) {
	v := p.V
	for _, s := range f.Body {
		if a, ok := s.(*tir.Assign); ok && a.Dst == v {
			if at, ok := a.X.(*tir.Atom); ok {
				if _, isVar := at.X.(tir.Var); isVar {
					return callDonor{}.Cheaper(f, p)
				}
			}
		}
	}
	return nil, tir.Var{}, false
}

func TestDiscardsAreReported(t *testing.T) {
	_, ps := parents(t, 30, 8)
	tight := grammar.Popcount()
	tight.MaxLen = 6
	r := rand.New(rand.NewPCG(8, 8))
	var y Yield
	for range 200 {
		_, err := TwoPoint(r, tight, ps[r.IntN(len(ps))], ps[r.IntN(len(ps))])
		y.Record(Crossover, err == nil)
		if err != nil {
			var d *Discarded
			if !errors.As(err, &d) || d.Op != Crossover {
				t.Fatalf("unexpected error %v", err)
			}
		}
	}
	if y.Survived[Crossover] == y.Attempted[Crossover] {
		t.Error("no child discarded under a 6-statement limit")
	}
}

// loopDonor donates the reference popcount loop for an int accumulator's
// value after a top-level loop.
type loopDonor struct{ ref *tir.Func }

func (loopDonor) Empty() bool { return false }

func (d loopDonor) Cheaper(f *tir.Func, p tir.Point) ([]tir.Stmt, tir.Var, bool) {
	if _, isLoop := f.Body[p.At].(*tir.Loop); !isLoop || p.V.T != tir.Int {
		return nil, tir.Var{}, false
	}
	// The reference body minus its return; its accumulator is v0.
	body := d.ref.Body[:len(d.ref.Body)-1]
	return tir.CloneStmts(body), body[0].(*tir.Assign).Dst, true
}

// TestSubstituteAccumulatorAfterLoop: the point after a top-level loop is a
// substitution site; the bit loop that computed the accumulator is replaced
// by the donor's loop and removed as dead.
func TestSubstituteAccumulatorAfterLoop(t *testing.T) {
	g := grammar.Popcount()
	ref := parse(t, `func f(xs []uint64) int {
	n := 0
	for _, x := range xs {
		n += bits.OnesCount64(x)
	}
	return n
}`)
	f := parse(t, `func f(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		x := xs[i]
		for j := 0; j < 64; j++ {
			n += int(x >> uint64(j) & 1)
		}
	}
	return n
}`)
	c, applied, err := Substitute(rand.New(rand.NewPCG(1, 1)), g, loopDonor{ref}, f)
	if err != nil || !applied {
		t.Fatalf("applied %v, err %v", applied, err)
	}
	src := string(c.Print("f"))
	if !strings.Contains(src, "bits.OnesCount64") || strings.Contains(src, ">>") {
		t.Errorf("bit loop not replaced by the donor's loop:\n%s", src)
	}
	checkChild(t, g, "substitution", c)
}

func TestDuplicate(t *testing.T) {
	g, ps := parents(t, 60, 9)
	run := func(seed uint64) []string {
		r := rand.New(rand.NewPCG(seed, 2))
		var out []string
		for _, p := range ps {
			if c, err := Duplicate(r, g, p); err == nil {
				out = append(out, string(c.Print("f")))
			} else {
				out = append(out, "discarded")
			}
		}
		return out
	}
	if a, b := run(4), run(4); strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatal("Duplicate is not deterministic in its seed")
	}
	r := rand.New(rand.NewPCG(5, 5))
	var y Yield
	grew := 0
	for range 1000 {
		p := ps[r.IntN(len(ps))]
		c, err := Duplicate(r, g, p)
		y.Record(Duplication, err == nil)
		if err != nil {
			var d *Discarded
			if !errors.As(err, &d) || d.Op != Duplication {
				t.Fatalf("unexpected error %v", err)
			}
			continue
		}
		checkChild(t, g, "duplication", c)
		if c.Len() > p.Len() {
			grew++
		}
	}
	if y.Survived[Duplication] == 0 || grew == 0 {
		t.Errorf("duplication: %d of %d survived, %d grew", y.Survived[Duplication], y.Attempted[Duplication], grew)
	}
}

// TestDuplicateCopiesRange: duplicating a loop body's statements gives the
// copy fresh locals that read the same outer variables.
func TestDuplicateCopiesRange(t *testing.T) {
	g := grammar.Popcount()
	f := parse(t, `func f(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		x := xs[i]
		n += bits.OnesCount64(x)
	}
	return n
}`)
	for seed := range uint64(200) {
		c, err := Duplicate(rand.New(rand.NewPCG(seed, 0)), g, f)
		if err != nil {
			continue
		}
		src := string(c.Print("f"))
		if strings.Count(src, "bits.OnesCount64") == 2 && strings.Count(src, ":= xs[i0]") == 2 {
			checkChild(t, g, "duplication", c)
			return
		}
	}
	t.Fatal("no seed duplicated the loop body's two statements")
}
