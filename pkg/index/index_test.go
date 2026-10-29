// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"go/types"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/ops"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

var _ ops.Donor = (*Index)(nil)

var intT = types.Typ[types.Int]

func TestFingerprintDistinguishes(t *testing.T) {
	base := [][]any{{1, 2, 3}, {4}, {}}
	fp, ok := Of(intT, base)
	if !ok {
		t.Fatal("no fingerprint for ints")
	}
	if again, _ := Of(intT, [][]any{{1, 2, 3}, {4}, {}}); again != fp {
		t.Error("fingerprint is not a function of the trace")
	}
	variants := map[string][][]any{
		"value":     {{1, 2, 4}, {4}, {}},
		"length":    {{1, 2, 3, 0}, {4}, {}},
		"split":     {{1, 2}, {3, 4}, {}},
		"order":     {{4}, {1, 2, 3}, {}},
		"vectors":   {{1, 2, 3}, {4}},
		"empty-seq": {{1, 2, 3}, {4}, {0}},
	}
	for name, v := range variants {
		if other, _ := Of(intT, v); other == fp {
			t.Errorf("%s change did not change the fingerprint", name)
		}
	}
	if other, _ := Of(types.Typ[types.Int64], [][]any{{int64(1), int64(2), int64(3)}, {int64(4)}, {}}); other == fp {
		t.Error("type is not part of the fingerprint")
	}
	if _, ok := Of(types.Typ[types.Float64], [][]any{{1.5}}); ok {
		t.Error("float value fingerprinted in v0")
	}
	if _, ok := FromTrace(eval.ValueTrace{Truncated: true}); ok {
		t.Error("truncated trace fingerprinted")
	}
}

func parse(t *testing.T, src string) *tir.Func {
	t.Helper()
	f, err := tir.Parse([]byte("package p\n\nimport \"math/bits\"\n\nvar _ = bits.Len\n\n"+src), "f")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

const bitLoop = `func f(xs []uint64) int {
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
}`

// top is the point after the k-th top-level definition of f (0-based).
func top(t *testing.T, f *tir.Func, k int) tir.Point {
	t.Helper()
	for i, s := range f.Body {
		if a, ok := s.(*tir.Assign); ok && a.Define {
			if k == 0 {
				return tir.Point{At: i, V: a.Dst}
			}
			k--
		}
	}
	t.Fatal("too few top-level definitions")
	return tir.Point{}
}

func slice(f *tir.Func, p tir.Point) (*Fragment, bool) { return Slice(f, p.At, p.V) }

func TestSlice(t *testing.T) {
	f := parse(t, bitLoop)
	// Top-level definitions: 0 x, 1 n, 2 m, 3 unrelated.
	fr, ok := slice(f, top(t, f, 2))
	if !ok {
		t.Fatal("no slice for m")
	}
	g := fr.Func(f.Sig)
	src := string(g.Print("f"))
	if !strings.Contains(src, "for ") || strings.Contains(src, "len(xs)") {
		t.Errorf("slice of m should hold the loop and not the unrelated len:\n%s", src)
	}
	if err := g.Validate(); err != nil {
		t.Errorf("fragment function invalid: %v\n%s", err, src)
	}
	if err := g.TypeCheck(); err != nil {
		t.Errorf("fragment function does not type-check: %v\n%s", err, src)
	}

	// The slice of x is just its definition.
	fx, _ := slice(f, top(t, f, 0))
	if len(fx.Stmts) != 1 {
		t.Errorf("slice of x has %d statements", len(fx.Stmts))
	}
	if fx.Hash == fr.Hash {
		t.Error("different slices hash equal")
	}
}

func TestSliceNotClosed(t *testing.T) {
	f := parse(t, `func f(xs []uint64) int {
	xs[0] = 1
	n := len(xs)
	return n
}`)
	if _, ok := slice(f, top(t, f, 0)); ok {
		t.Error("slice after a store is not closed over the parameters")
	}
	g := parse(t, `func f(xs []uint64) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		k := i + 1
		n += k
	}
	return n
}`)
	// k is defined inside the loop, which does not reassign it: no point.
	loop := g.Body[1].(*tir.Loop)
	if _, ok := Slice(g, 1, loop.Body[0].(*tir.Assign).Dst); ok {
		t.Error("slice of a loop-local variable")
	}
}

// TestSliceAccumulatorAfterLoop: the value of an accumulator after the loop
// that reassigns it is a point, and its slice is the definition and the loop.
func TestSliceAccumulatorAfterLoop(t *testing.T) {
	f := parse(t, `func f(xs []uint64) int {
	n := 0
	m := len(xs)
	for i := 0; i < len(xs); i++ {
		n += bits.OnesCount64(xs[i])
	}
	_ = m
	return n
}`)
	n := top(t, f, 0).V
	var pts []tir.Point
	for _, p := range tir.Points(f) {
		if p.V == n {
			pts = append(pts, p)
		}
	}
	// n := 0 is reassigned by the loop, so only the point after the loop
	// sees every later read.
	if len(pts) != 1 || pts[0].At != 2 {
		t.Fatalf("points for n = %v, want one after the loop (statement 2)", pts)
	}
	fr, ok := Slice(f, 2, n)
	if !ok || len(fr.Stmts) != 2 {
		t.Fatalf("slice of n after the loop: ok %v, %d statements", ok, len(fr.Stmts))
	}
	if _, isLoop := fr.Stmts[1].(*tir.Loop); !isLoop {
		t.Error("slice does not hold the loop")
	}
	src := string(fr.Func(f.Sig).Print("f"))
	if strings.Contains(src, "v1 := len") {
		t.Errorf("unrelated definition in the slice:\n%s", src)
	}
	if err := fr.Func(f.Sig).TypeCheck(); err != nil {
		t.Errorf("fragment function: %v\n%s", err, src)
	}
}

func TestSliceHashAlphaInvariant(t *testing.T) {
	f := parse(t, bitLoop)
	// The same computation with a different preamble: IDs differ, the
	// slice of m does not.
	g := parse(t, strings.Replace(bitLoop, "x := xs[0]", "pad := len(xs)\n\t_ = pad\n\tx := xs[0]", 1))
	a, _ := slice(f, top(t, f, 2))
	b, _ := slice(g, top(t, g, 3))
	if a.Hash != b.Hash {
		t.Error("identical slices from different genomes hash differently")
	}
}

func TestIndexOrderAndNovelty(t *testing.T) {
	ix := New()
	f := parse(t, bitLoop)
	fa, _ := slice(f, top(t, f, 2))
	fb, _ := slice(f, top(t, f, 0))
	fc, _ := slice(f, top(t, f, 3))
	fp1, _ := Of(intT, [][]any{{3}})
	fp2, _ := Of(intT, [][]any{{4}})
	ix.Add(fp1, fa, 900, 1)
	ix.Add(fp1, fb, 10, 2)
	ix.Add(fp1, fb, 5, 3) // already recorded: ignored
	ix.Add(fp2, fc, 7, 4)
	es := ix.Lookup(fp1)
	if len(es) != 2 || es[0].Cost != 10 || es[1].Cost != 900 {
		t.Errorf("entries for fp1 = %+v", es)
	}
	if ix.Len() != 2 || ix.HC.Len() != 3 {
		t.Errorf("Len %d, HC.Len %d", ix.Len(), ix.HC.Len())
	}
	fp3, _ := Of(intT, [][]any{{5}})
	if n := ix.Novelty([]Fingerprint{fp1, fp3, fp3, fp2}); n != 1 {
		t.Errorf("novelty = %d, want 1", n)
	}
}

func TestCheaperDrivesSubstitution(t *testing.T) {
	g := grammar.Popcount()
	f := parse(t, bitLoop)
	loopFrag, _ := slice(f, top(t, f, 2))
	callSrc := parse(t, `func f(xs []uint64) int {
	x := xs[0]
	c := bits.OnesCount64(x)
	return c
}`)
	callFrag, _ := slice(callSrc, top(t, callSrc, 1))
	fp, _ := Of(intT, [][]any{{1}, {7}})

	ix := New()
	if _, _, ok := ix.Cheaper(f, top(t, f, 2)); ok {
		t.Fatal("empty index donated")
	}
	ix.Add(fp, loopFrag, 1000, 1)
	ix.Add(fp, callFrag, 3, 2)
	if _, _, ok := ix.Cheaper(callSrc, top(t, callSrc, 1)); ok {
		t.Error("index donated to the cheapest fragment itself")
	}
	for seed := range uint64(20) {
		c, applied, err := ops.Substitute(rand.New(rand.NewPCG(seed, 0)), g, ix, f)
		if err != nil {
			t.Fatal(err)
		}
		if applied {
			src := string(c.Print("f"))
			if strings.Contains(src, "for ") || !strings.Contains(src, "bits.OnesCount64") {
				t.Errorf("substitution did not swap the loop for the call:\n%s", src)
			}
			return
		}
	}
	t.Fatal("index-driven substitution never applied")
}

// TestCollisionDetectorProperty: two fragments with equal fingerprints
// produce equal outputs on every test vector. Fragments are the top-level
// slices of random genomes; each is given an output trace from a pool built
// to contain near-misses (traces differing in one value, one length, or one
// vector boundary), fingerprinted from that trace, and indexed. Every
// fingerprint bucket must then agree on outputs. Under eval.stub there are
// no real traces; once giri's traces land, the same check runs on them.
func TestCollisionDetectorProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 11))
	var pool [][][]any
	for range 200 {
		base := make([][]any, 1+r.IntN(4))
		for i := range base {
			for range r.IntN(5) {
				base[i] = append(base[i], r.IntN(3))
			}
		}
		pool = append(pool, base)
		near := clone(base)
		if len(near[0]) > 0 {
			near[0][0] = near[0][0].(int) + 1
		} else {
			near[0] = append(near[0], 0)
		}
		pool = append(pool, near)
		longer := clone(base)
		longer[len(longer)-1] = append(longer[len(longer)-1], 0)
		pool = append(pool, longer)
	}

	g := grammar.Popcount()
	gen := grammar.Gen{G: g, R: r}
	ix := New()
	outputs := map[tir.Hash][]any{}
	var id uint64
	for range 1500 {
		f := gen.Random(4 + r.IntN(12))
		for _, p := range tir.Points(f) {
			fr, ok := Slice(f, p.At, p.V)
			if !ok {
				continue
			}
			if _, seen := outputs[fr.Hash]; seen {
				continue
			}
			trace := pool[r.IntN(len(pool))]
			fp, _ := Of(intT, trace)
			ix.Add(fp, fr, uint64(r.IntN(100)), id)
			outputs[fr.Hash] = flatten(trace)
			id++
		}
	}
	if ix.HC.Len() < 500 {
		t.Fatalf("only %d fragments; the property is not exercised", ix.HC.Len())
	}
	if cs := ix.Collisions(outputs); len(cs) > 0 {
		t.Fatalf("%d collisions, first %+v", len(cs), cs[0])
	}

	// The detector itself: a fragment filed under another fragment's
	// fingerprint, with different outputs, is found.
	var victim tir.Hash
	var victimFP Fingerprint
	for h, fp := range ix.fpOf {
		victim, victimFP = h, fp
		break
	}
	forged := parse(t, `func f(xs []uint64) int {
	q := len(xs)
	return q
}`)
	ffr, _ := slice(forged, top(t, forged, 0))
	if _, dup := outputs[ffr.Hash]; dup {
		t.Skip("forged fragment already indexed")
	}
	ix.Add(victimFP, ffr, 1, 999)
	outputs[ffr.Hash] = append(append([]any(nil), outputs[victim]...), "different")
	if cs := ix.Collisions(outputs); len(cs) != 1 {
		t.Fatalf("forged collision: found %d, want 1", len(cs))
	}
}

func clone(x [][]any) [][]any {
	out := make([][]any, len(x))
	for i := range x {
		out[i] = append([]any(nil), x[i]...)
	}
	return out
}

// flatten is a trace as a per-vector output list: each vector's sequence
// as one comparable value.
func flatten(x [][]any) []any {
	out := make([]any, len(x))
	for i, seq := range x {
		var b strings.Builder
		for _, v := range seq {
			b.WriteString(string(rune('0' + v.(int))))
		}
		out[i] = b.String()
	}
	return out
}
