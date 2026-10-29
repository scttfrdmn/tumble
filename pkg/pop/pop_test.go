// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/index"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// fakeEval is a fast deterministic evaluator for testing the loop: outputs,
// steps, and violations are functions of the SSA function's instructions,
// not of its semantics. It builds real SSA.
type fakeEval struct {
	want [][]any
}

func (fakeEval) Build(dir string) (eval.Program, error) { return eval.LoadSSA(dir) }

func (e fakeEval) Run(fn *ssa.Function, in []eval.Input, opt eval.RunOptions) (eval.Result, error) {
	h := fnv.New64a()
	n := uint64(0)
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			h.Write([]byte(ins.String() + ";"))
			n++
		}
	}
	sum := h.Sum64()
	res := eval.Result{Steps: n * uint64(len(in))}
	if sum%23 == 0 {
		res.Violations = []eval.Violation{{Category: "fake", Input: -1}}
	}
	for t := range in {
		w := 0
		if t < len(e.want) {
			w = e.want[t][0].(int)
		}
		if (sum>>t)%3 == 0 {
			w++
		}
		res.Outputs = append(res.Outputs, []any{w})
	}
	return res, nil
}

func problem(t *testing.T) Problem {
	t.Helper()
	src, err := os.ReadFile("../../kernels/popcount/popcount.go")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := tir.Parse(src, "Popcount")
	if err != nil {
		t.Fatal(err)
	}
	var sel []eval.Input
	var want [][]any
	for i := range 6 {
		sel = append(sel, eval.Input{Args: []any{make([]uint64, i)}})
		want = append(want, []any{i})
	}
	return Problem{Grammar: grammar.Popcount(), Reference: ref, Selection: sel, Want: want}
}

func smallParams() Params {
	p := Defaults()
	p.Islands, p.Population, p.Generations = 2, 12, 5
	p.MigrateEvery, p.Migrants = 2, 2
	p.Workers = 2
	return p
}

// snapshot serializes the archive after each generation: the
// generation-by-generation archive that the determinism check diffs.
func snapshot(buf *bytes.Buffer) Observer {
	return func(rd *Readings, a *Archive) error {
		fmt.Fprintf(buf, "gen %d evals %d discards %v yield %v archive %d best %d\n",
			rd.Gen, rd.Evaluations, rd.Discards, rd.Yield, rd.ArchiveSize, rd.BestCost)
		for _, e := range a.Elites() {
			fmt.Fprintf(buf, "%+v %d %d %d %s\n%s\n", e.Cell, e.Ind.ID, e.Ind.Correct, e.Ind.Cost, e.Ind.Origin, e.Ind.F.Print("f"))
		}
		return nil
	}
}

func run(t *testing.T, ev eval.Evaluator, p Params) (string, *Result) {
	t.Helper()
	var buf bytes.Buffer
	res, err := Run(ev, problem(t), p, t.TempDir(), snapshot(&buf))
	if err != nil {
		t.Fatal(err)
	}
	return buf.String(), res
}

func TestRunDeterministic(t *testing.T) {
	prob := problem(t)
	ev := fakeEval{want: prob.Want}
	a, _ := run(t, ev, smallParams())
	b, _ := run(t, ev, smallParams())
	if a != b {
		t.Fatalf("two runs with one seed differ:\n%s\n---\n%s", a, b)
	}
	p := smallParams()
	p.Seed = 2
	if c, _ := run(t, ev, p); c == a {
		t.Error("a different seed gave the same run")
	}
}

func TestRunReadingsAndIndex(t *testing.T) {
	prob := problem(t)
	ev := fakeEval{want: prob.Want}
	var rds []*Readings
	p := smallParams()
	res, err := Run(ev, prob, p, t.TempDir(), func(rd *Readings, a *Archive) error {
		rds = append(rds, rd)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rds) != p.Generations {
		t.Fatalf("%d readings for %d generations", len(rds), p.Generations)
	}
	for _, rd := range rds {
		if rd.Evaluations != p.Islands*p.Population {
			t.Errorf("gen %d: %d evaluations", rd.Gen, rd.Evaluations)
		}
		if rd.VCMeasured {
			t.Error("value cache reported as measured")
		}
	}
	last := rds[len(rds)-2] // the last generation does not breed
	if last.Yield.Attempted[0]+last.Yield.Attempted[1] == 0 {
		t.Error("no operator attempts recorded")
	}
	if res.Archive.Len() == 0 {
		t.Error("empty archive")
	}
	if res.Index.Empty() {
		t.Error("the index is empty")
	}
}

// notInstrument is fakeEval reporting itself as a non-instrument, as the
// stub does.
type notInstrument struct{ fakeEval }

func (notInstrument) Name() string       { return "fake-stub" }
func (notInstrument) IsInstrument() bool { return false }

func TestIndexFromFragmentOutputs(t *testing.T) {
	prob := problem(t)
	_, res := run(t, fakeEval{want: prob.Want}, smallParams())
	if res.Index.Empty() {
		t.Error("index empty under an instrument: fragments are fingerprinted by their outputs, no traces needed")
	}
	_, res = run(t, notInstrument{fakeEval{want: prob.Want}}, smallParams())
	if !res.Index.Empty() {
		t.Error("index filled under a non-instrument evaluator")
	}
}
func TestMigration(t *testing.T) {
	prob := problem(t)
	p := smallParams()
	is := &island{n: 0, r: rand.New(rand.NewPCG(1, 1))}
	src := []*Individual{{F: prob.Reference, C: []bool{true}, Correct: 1}}
	r := &runner{prob: prob, p: p, ix: index.New(), donorC: map[uint64][]bool{}}
	is.pop = src
	r.breed(is, 0, src)
	if is.pop[0].Origin != Migrant || len(is.pop) != p.Population {
		t.Errorf("breed with one migrant: first origin %s, size %d", is.pop[0].Origin, len(is.pop))
	}
}

func TestLexicase(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	f := problem(t).Reference
	a := &Individual{F: f, C: []bool{true, true, false}, Cost: 10}
	b := &Individual{F: f, C: []bool{true, true, true}, Cost: 50}
	c := &Individual{F: f, C: []bool{true, true, true}, Cost: 40}
	for range 50 {
		if got := Lexicase(r, []*Individual{a, b, c}); got != c {
			t.Fatal("lexicase did not pick the all-passing, cheaper candidate")
		}
	}
	// a and d each pass a case the other fails: both get picked.
	d := &Individual{F: f, C: []bool{false, true, true}, Cost: 10}
	seen := map[*Individual]bool{}
	for range 100 {
		seen[Lexicase(r, []*Individual{a, d})] = true
	}
	if !seen[a] || !seen[d] {
		t.Error("lexicase never selected one of two specialists")
	}
}

func TestArchive(t *testing.T) {
	f := problem(t).Reference
	a := NewArchive(4)
	x := &Individual{F: f, C: []bool{true, false}, Correct: 1, Cost: 100}
	y := &Individual{F: f, C: []bool{true, true}, Correct: 2, Cost: 100}
	z := &Individual{F: f, C: []bool{true, true}, Correct: 2, Cost: 100, Discard: "violation"}
	if !a.Offer(x) || !a.Offer(y) || a.Offer(x) || a.Offer(z) {
		t.Error("archive placement order wrong")
	}
	if a.Len() != 1 || a.Best() != y {
		t.Errorf("archive len %d best %+v", a.Len(), a.Best())
	}
}

func TestParamsJSON(t *testing.T) {
	p := Defaults()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var q Params
	if err := json.Unmarshal(b, &q); err != nil {
		t.Fatal(err)
	}
	if c, _ := json.Marshal(q); !bytes.Equal(b, c) {
		t.Errorf("params JSON round trip:\n%s\n%s", b, c)
	}
	if p.Islands != 8 || p.Population != 256 || p.Migrants != 4 || p.MigrateEvery != 10 {
		t.Error("defaults differ from PROJECT.md §4.5")
	}
}

func TestElitesSpreadAcrossCost(t *testing.T) {
	f := problem(t).Reference
	mk := func(cost uint64, correct bool) *Individual {
		c := []bool{true, correct}
		n := 1
		if correct {
			n = 2
		}
		return &Individual{F: f, C: c, Correct: n, Cost: cost}
	}
	var all []*Individual
	for _, c := range []uint64{10, 10, 10, 11, 12, 15, 20, 30, 40, 50} {
		all = append(all, mk(c, true))
	}
	all = append(all, mk(5, false)) // not fully correct: never an elite
	benched := map[uint64]bool{}
	costs := func(es []*Individual) []uint64 {
		var out []uint64
		for _, e := range es {
			out = append(out, e.Cost)
		}
		return out
	}
	// Distinct costs 10 11 12 15 20 30 40 50; three spread picks.
	if got := costs(elites(all, 3, benched)); !slices.Equal(got, []uint64{10, 20, 50}) {
		t.Errorf("first generation's elites by cost = %v, want [10 20 50]", got)
	}
	// Costs once benchmarked are not offered again.
	if got := costs(elites(all, 3, benched)); !slices.Equal(got, []uint64{11, 15, 40}) {
		t.Errorf("second generation's elites = %v, want [11 15 40]", got)
	}
	if got := costs(elites(all, 8, benched)); !slices.Equal(got, []uint64{12, 30}) {
		t.Errorf("remaining elites = %v, want [12 30]", got)
	}
	if got := elites(all, 8, benched); len(got) != 0 {
		t.Errorf("elites after every cost is benchmarked: %v", costs(got))
	}
}

func TestIndexBudget(t *testing.T) {
	prob := problem(t)
	p := smallParams()
	p.IndexBudget = 5
	prev := 0
	_, err := Run(fakeEval{want: prob.Want}, prob, p, t.TempDir(), func(rd *Readings, a *Archive) error {
		if grew := rd.Fragments - prev; grew > p.IndexBudget {
			t.Errorf("gen %d: %d new fragments, budget %d", rd.Gen, grew, p.IndexBudget)
		}
		prev = rd.Fragments
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if prev == 0 {
		t.Error("nothing indexed under a budget")
	}
}

// TestScoreArgsAfter: a vector is correct only if the genome also leaves its
// arguments as the reference does; an evaluator reporting none is not trusted.
func TestScoreArgsAfter(t *testing.T) {
	prob := Problem{Want: [][]any{{3}, {3}, {3}}, WantAfter: [][]any{{[]uint64{1, 2}}, {[]uint64{1, 2}}, {[]uint64{1, 2}}}}
	r := &runner{prob: prob}
	ind := &Individual{F: problem(t).Reference}
	r.score(ind, eval.Result{
		Outputs:   [][]any{{3}, {3}, {3}},
		ArgsAfter: [][]any{{[]uint64{1, 2}}, {[]uint64{1, 0x16}}, nil},
	})
	if !slices.Equal(ind.C, []bool{true, false, false}) {
		t.Errorf("correctness %v, want [true false false]", ind.C)
	}
}

// TestBoundedParsimony: statements above Params.ParsimonyAbove break cost
// ties; below it length is free; cost always comes first.
func TestBoundedParsimony(t *testing.T) {
	short, err := tir.Parse([]byte("package p\n\nfunc f(xs []uint64) int {\n\treturn len(xs)\n}\n"), "f")
	if err != nil {
		t.Fatal(err)
	}
	long, err := tir.Parse([]byte("package p\n\nfunc f(xs []uint64) int {\n\tn := len(xs)\n\tm := n + 1\n\tk := m + 1\n\t_ = k\n\treturn n\n}\n"), "f")
	if err != nil {
		t.Fatal(err)
	}
	score := func(f *tir.Func, cost uint64, above int) *Individual {
		r := &runner{prob: Problem{Want: [][]any{{1}}}, p: Params{ParsimonyAbove: above}}
		ind := &Individual{F: f}
		r.score(ind, eval.Result{Outputs: [][]any{{1}}, Steps: cost})
		return ind
	}
	rng := rand.New(rand.NewPCG(3, 3))

	// Both under the limit (lengths 2 and 4, limit 24): no length preference.
	a, b := score(short, 10, 24), score(long, 10, 24)
	if a.excess != 0 || b.excess != 0 {
		t.Fatalf("excess under the limit: %d, %d", a.excess, b.excess)
	}
	seen := map[*Individual]bool{}
	for range 100 {
		seen[Lexicase(rng, []*Individual{a, b})] = true
	}
	if !seen[a] || !seen[b] {
		t.Error("under the limit, lexicase preferred one length")
	}

	// Limit 2: short (2 statements) is at it, long (4) exceeds it by 2, so
	// short wins at equal cost.
	a, b = score(short, 10, 2), score(long, 10, 2)
	if a.excess != 0 || b.excess != 2 {
		t.Fatalf("excess over the limit: %d, %d", a.excess, b.excess)
	}
	for range 50 {
		if Lexicase(rng, []*Individual{b, a}) != a {
			t.Fatal("over the limit, lexicase did not prefer fewer statements")
		}
	}
	if better(a, b) >= 0 {
		t.Error("rank: fewer statements over the limit should rank first at equal cost")
	}
	// Cost first: the long genome wins if it is cheaper.
	cheapLong := score(long, 9, 2)
	for range 50 {
		if Lexicase(rng, []*Individual{a, cheapLong}) != cheapLong {
			t.Fatal("parsimony overrode cost")
		}
	}
}
