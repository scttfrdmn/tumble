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
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/index"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// fakeEval is a fast deterministic evaluator for testing the loop: outputs,
// steps, violations, and traces are functions of the SSA function's
// instructions, not of its semantics. It builds real SSA.
type fakeEval struct {
	want   [][]any
	traces bool
}

func (fakeEval) Build(dir string) (eval.Program, error) { return eval.LoadSSA(dir) }

func (e fakeEval) Run(fn *ssa.Function, in []eval.Input, opt eval.RunOptions) (eval.Result, error) {
	h := fnv.New64a()
	var vals []ssa.Value
	n := uint64(0)
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			h.Write([]byte(ins.String() + ";"))
			n++
			if v, ok := ins.(ssa.Value); ok {
				vals = append(vals, v)
			}
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
	if e.traces && opt.Trace {
		for _, v := range vals {
			seqs := make([][]any, len(in))
			for t := range in {
				seqs[t] = []any{len(v.String())%4 + t}
			}
			res.Traces = append(res.Traces, eval.ValueTrace{Value: v, Seqs: seqs})
		}
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
	ev := fakeEval{want: prob.Want, traces: true}
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
	ev := fakeEval{want: prob.Want, traces: true}
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
		t.Error("traces were given but the index is empty")
	}
}

func TestRunWithoutTracesLeavesIndexEmpty(t *testing.T) {
	prob := problem(t)
	_, res := run(t, fakeEval{want: prob.Want}, smallParams())
	if !res.Index.Empty() {
		t.Error("index filled without traces")
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

func TestVarFingerprints(t *testing.T) {
	prob := problem(t)
	g := grammar.Gen{G: prob.Grammar, R: rand.New(rand.NewPCG(4, 4))}
	f := g.Random(12)
	src := tir.PrintFile("g", []tir.Named{{Name: "g_0001", F: f}})
	dir := filepath.Join(t.TempDir(), "gen")
	if err := eval.WriteGeneration(dir, src); err != nil {
		t.Fatal(err)
	}
	prog, err := eval.LoadSSA(dir)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs()["g_0001"]
	var traces []eval.ValueTrace
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if v, ok := ins.(ssa.Value); ok {
				traces = append(traces, eval.ValueTrace{Value: v, Seqs: [][]any{{len(v.Name())}}})
			}
		}
	}
	fps := VarFingerprints(src, "g_0001", f, fn, traces)
	for id := range fps {
		found := false
		for _, s := range f.Body {
			if a, ok := s.(*tir.Assign); ok && a.Define && a.Dst.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("fingerprint attributed to v%d, not a top-level definition", id)
		}
	}
	if len(fps) == 0 {
		t.Errorf("no top-level definition got a fingerprint:\n%s", src)
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
