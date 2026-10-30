// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import (
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"sync"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/index"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// probeTraceCap bounds the per-value trace of a probe; a probe whose trace
// is truncated gives no fingerprint.
const probeTraceCap = 1 << 15

// probeBudget multiplies the step budget for a probed clone's run. Probes
// at points inside inner loops execute on every iteration, so a genome
// within budget can exhaust it once probed; the genome's own evaluation has
// already held it to the real budget.
const probeBudget = 16

// pointData is what probing learned about one body point of a genome.
type pointData struct {
	Point   tir.BlockPoint
	FP      index.Fingerprint
	PerExec float64 // the point's slice, abstract cost per execution of the point
	Scope   []index.ScopedFP
	Execs   int
	Slice   *index.BodyFragment // nil if the point has no closed slice

	// Per slice member (Slice.Source order): its cost per execution of the
	// point, the variables it defines or reassigns, and those it reads.
	MemberCost  []float64
	MemberDefs  []map[int]bool
	MemberReads []map[int]bool
}

// removable is the cost per execution that substituting at the point with
// binding bind would remove: the slice members not needed to compute the
// variables the donor's inputs are bound to.
func (pd pointData) removable(bind map[int]tir.Var) float64 {
	keep := make([]bool, len(pd.MemberCost))
	need := map[int]bool{}
	for _, v := range bind {
		need[v.ID] = true
	}
	for changed := true; changed; {
		changed = false
		for i := range keep {
			if keep[i] {
				continue
			}
			hit := false
			for id := range pd.MemberDefs[i] {
				hit = hit || need[id]
			}
			if hit {
				keep[i], changed = true, true
				for id := range pd.MemberReads[i] {
					need[id] = true
				}
			}
		}
	}
	saved := 0.0
	for i, c := range pd.MemberCost {
		if !keep[i] {
			saved += c
		}
	}
	return saved
}

// probe is one inserted probe: P := V ^ 0 (or V != false) just after point
// k's statement.
type probe struct {
	k    int
	v, p tir.Var
	stmt tir.Stmt
}

// probeSite is a body point of the genome with what probing needs about it,
// all taken before any probe is inserted: the variables in scope, the slice
// as a donor fragment (from the genome itself, so free of probes), and the
// same slice's statements in the probed clone, for its cost.
type probeSite struct {
	point  tir.BlockPoint
	scope  []tir.Var
	frag   *index.BodyFragment
	source []tir.Stmt
}

// probed builds f's probed clone: after each body point, a probe for every
// scalar variable in scope there. It returns the clone, the sites, and the
// probes.
func probed(f *tir.Func) (*tir.Func, []probeSite, []probe) {
	pf := f.Clone()
	points := tir.BlockPoints(pf)
	if len(points) == 0 {
		return nil, nil, nil
	}
	sites := make([]probeSite, len(points))
	type at struct {
		block *[]tir.Stmt
		after tir.Stmt
	}
	ats := make([]at, len(points))
	for k, p := range points {
		sites[k].point = p
		sites[k].scope = pf.VisibleAt(p.Path, p.At)
		if fr, ok := index.BodySlice(f, p); ok {
			sites[k].frag = fr
		}
		if fr, ok := index.BodySlice(pf, p); ok {
			sites[k].source = fr.Source
		}
		b := pf.Block(p.Path)
		ats[k] = at{b, (*b)[p.At]}
	}
	var probes []probe
	for k := range points {
		var ins []tir.Stmt
		for _, v := range sites[k].scope {
			if v.T.Slice || !v.T.Valid() {
				continue
			}
			var x tir.Expr
			pv := pf.NewVar(v.T)
			if v.T.IsBool() {
				x = &tir.Binop{Op: token.NEQ, X: v, Y: tir.BoolConst(false)}
			} else {
				x = &tir.Binop{Op: token.XOR, X: v, Y: tir.IntConst(v.T, 0)}
			}
			st := &tir.Assign{Dst: pv, Define: true, X: x}
			ins = append(ins, st)
			probes = append(probes, probe{k: k, v: v, p: pv, stmt: st})
		}
		b := ats[k].block
		i := slices.Index(*b, ats[k].after)
		*b = slices.Insert(*b, i+1, ins...)
	}
	pf.Sink()
	return pf, sites, probes
}

// probeGeneration probes the generation's eligible genomes not probed in
// the previous generation, and returns the point data of every eligible
// genome by structural hash. Genomes are probed concurrently, Islands ×
// Workers at a time; results are by index, so the outcome is deterministic.
func (r *runner) probeGeneration(g int, all []*Individual) (map[tir.Hash][]pointData, error) {
	out := map[tir.Hash][]pointData{}
	type job struct {
		hash  tir.Hash
		f     *tir.Func
		pf    *tir.Func
		sites []probeSite
		prbs  []probe
		name  string
		data  []pointData
		seqs  map[index.Fingerprint][][]any
	}
	var jobs []*job
	queued := map[tir.Hash]bool{}
	for _, ind := range all {
		if !ind.Eligible() {
			continue
		}
		h := ind.F.StructHash()
		if d, ok := r.probedPrev[h]; ok {
			out[h] = d
			continue
		}
		if queued[h] {
			continue
		}
		queued[h] = true
		pf, sites, prbs := probed(ind.F)
		if pf == nil {
			out[h] = nil
			continue
		}
		jobs = append(jobs, &job{hash: h, f: ind.F, pf: pf, sites: sites, prbs: prbs, name: fmt.Sprintf("p_%05d", len(jobs)+1)})
	}
	if len(jobs) == 0 {
		return out, nil
	}
	named := make([]tir.Named, len(jobs))
	for i, j := range jobs {
		named[i] = tir.Named{Name: j.name, F: j.pf}
	}
	src := tir.PrintFile("g", named)
	dir := filepath.Join(r.work, "probes", fmt.Sprintf("gen%04d", g))
	if err := eval.WriteGeneration(dir, src); err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	prog, err := r.ev.Build(dir)
	if err != nil {
		return nil, err
	}
	defer r.release(prog)
	fns := prog.Funcs()
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, r.p.Islands*r.p.Workers)
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			res, err := r.ev.Run(fns[j.name], r.prob.Selection, eval.RunOptions{MaxSteps: probeBudget * r.p.MaxSteps, Trace: true, TraceCap: probeTraceCap})
			if err != nil {
				errs[i] = err
				return
			}
			if len(res.Violations) > 0 || res.Exhausted {
				return // no point data for a genome that faults
			}
			j.data, j.seqs = pointsFromTraces(src, j.name, j.pf, j.sites, j.prbs, res.Traces, r.libTypes)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for _, j := range jobs {
		out[j.hash] = j.data
		for fp, sq := range j.seqs {
			if _, ok := r.libSeqs[fp]; !ok {
				r.libSeqs[fp] = sq
			}
		}
	}
	return out, nil
}

// pointsFromTraces reads each probe's trace (the one SSA value on its line)
// as the per-iteration values of its variable at its point, and each
// point's slice cost from the executions of the slice's traced
// instructions, probes excluded. It also returns, for probed values of the
// types in keep, their sequences by fingerprint (library seeding's input).
func pointsFromTraces(src []byte, name string, pf *tir.Func, sites []probeSite, prbs []probe, traces []eval.ValueTrace, keep map[tir.Type]bool) ([]pointData, map[index.Fingerprint][][]any) {
	lines := tir.Lines(src, name, pf)
	byLine := map[int][]eval.ValueTrace{}
	for _, tr := range traces {
		if tr.Value == nil || !tr.Value.Pos().IsValid() {
			continue
		}
		l := tr.Value.Parent().Prog.Fset.Position(tr.Value.Pos()).Line
		byLine[l] = append(byLine[l], tr)
	}
	probeLines := map[int]bool{}
	for _, pr := range prbs {
		probeLines[lines[pr.stmt][0]] = true
	}
	fps := make([]map[int]index.Fingerprint, len(sites))
	execs := make([]int, len(sites))
	for k := range sites {
		fps[k] = map[int]index.Fingerprint{}
	}
	seqs := map[index.Fingerprint][][]any{}
	for _, pr := range prbs {
		trs := byLine[lines[pr.stmt][0]]
		if len(trs) != 1 || trs[0].Truncated {
			continue
		}
		fp, ok := index.FromTrace(trs[0])
		if !ok {
			continue
		}
		fps[pr.k][pr.v.ID] = fp
		if keep[pr.v.T] {
			if _, ok := seqs[fp]; !ok {
				seqs[fp] = trs[0].Seqs
			}
		}
		if pr.v.ID == sites[pr.k].point.V.ID {
			for _, s := range trs[0].Seqs {
				execs[pr.k] += len(s)
			}
		}
	}
	var out []pointData
	for k, st := range sites {
		p := st.point
		fp, ok := fps[k][p.V.ID]
		if !ok || execs[k] == 0 {
			continue
		}
		pd := pointData{Point: p, FP: fp, Execs: execs[k]}
		for _, v := range st.scope {
			if vfp, ok := fps[k][v.ID]; ok {
				pd.Scope = append(pd.Scope, index.ScopedFP{V: v, FP: vfp})
			}
		}
		if st.frag != nil && st.source != nil {
			total := 0
			for _, s := range st.source {
				cost := 0
				span := lines[s]
				for l := span[0]; l <= span[1]; l++ {
					if probeLines[l] {
						continue
					}
					for _, tr := range byLine[l] {
						for _, seq := range tr.Seqs {
							cost += len(seq)
						}
					}
				}
				total += cost
				defs, reads := map[int]bool{}, map[int]bool{}
				local := map[int]bool{}
				tir.Walk([]tir.Stmt{s}, func(t tir.Stmt, _ int) bool {
					for _, d := range tir.Defs(t) {
						local[d.ID] = true
					}
					if a, ok := t.(*tir.Assign); ok && (a.Define || !local[a.Dst.ID]) {
						defs[a.Dst.ID] = true
					}
					for _, u := range tir.Uses(t) {
						reads[u.ID] = true
					}
					return true
				})
				for _, d := range tir.Defs(s) {
					defs[d.ID] = true
				}
				pd.MemberCost = append(pd.MemberCost, float64(cost)/float64(execs[k]))
				pd.MemberDefs = append(pd.MemberDefs, defs)
				pd.MemberReads = append(pd.MemberReads, reads)
			}
			pd.Slice = st.frag
			pd.PerExec = float64(total) / float64(execs[k])
		}
		out = append(out, pd)
	}
	return out, seqs
}

// libraryDonors evaluates, through the evaluator, each admitted single-
// parameter call of the grammar on the per-iteration values of every probed
// scope variable of the call's parameter type not seen before, and adds the
// call as a body donor for the resulting fingerprint (PROJECT.md §5.3, blind
// library discovery), at one call per execution.
func (r *runner) libraryDonors(g int, data map[tir.Hash][]pointData) error {
	type want struct {
		call grammar.Callee
		in   index.Fingerprint
		seqs [][]any
	}
	var wants []want
	seen := map[string]bool{}
	hashes := make([]tir.Hash, 0, len(data))
	for h := range data {
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool { return string(hashes[i][:]) < string(hashes[j][:]) })
	for _, h := range hashes {
		for _, pd := range data[h] {
			for _, sc := range pd.Scope {
				for _, c := range r.prob.Grammar.Calls {
					if c.Poly || c.Pkg == "" || len(c.Params) != 1 || c.Params[0] != sc.V.T {
						continue
					}
					k := c.String() + string(sc.FP[:])
					if seen[k] || r.libSeen[k] {
						continue
					}
					seen[k] = true
					seqs, ok := r.libSeqs[sc.FP]
					if !ok {
						continue
					}
					wants = append(wants, want{c, sc.FP, seqs})
				}
			}
		}
	}
	if len(wants) == 0 {
		return nil
	}
	for i, w := range wants {
		if err := r.libraryDonor(g, i, w.call, w.in, w.seqs); err != nil {
			return err
		}
		r.libSeen[w.call.String()+string(w.in[:])] = true
	}
	return nil
}

// libraryDonor evaluates call on seqs (one sequence of argument values per
// test vector) and indexes c := call(x) as a donor for the result.
func (r *runner) libraryDonor(g, n int, call grammar.Callee, in index.Fingerprint, seqs [][]any) error {
	pt, rt := call.Params[0], call.Result
	f := tir.NewFunc(&tir.Signature{Params: []tir.Param{{Name: "us", T: pt.SliceOf()}, {Name: "out", T: rt.SliceOf()}}, Result: tir.Int})
	us, out := f.Param(0), f.Param(1)
	i := f.NewVar(tir.Int)
	x := f.NewVar(pt)
	c := f.NewVar(rt)
	f.Body = []tir.Stmt{
		&tir.Loop{Ind: i, Lo: tir.IntConst(tir.Int, 0), Hi: &tir.Len{X: us}, Step: 1, Body: []tir.Stmt{
			&tir.Assign{Dst: x, Define: true, X: &tir.Index{X: us, I: i}},
			&tir.Assign{Dst: c, Define: true, X: &tir.Call{Pkg: call.Pkg, Name: call.Name, Args: []tir.Operand{x}}},
			&tir.Store{X: out, I: i, Val: c},
		}},
		&tir.Return{X: tir.IntConst(tir.Int, 0)},
	}
	dir := filepath.Join(r.work, "library", fmt.Sprintf("gen%04d-%d", g, n))
	if err := eval.WriteGeneration(dir, tir.PrintFile("g", []tir.Named{{Name: "lib", F: f}})); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	prog, err := r.ev.Build(dir)
	if err != nil {
		return err
	}
	defer r.release(prog)
	argT, ok1 := goSliceOf(pt)
	resT, ok2 := goSliceOf(rt)
	if !ok1 || !ok2 {
		return nil
	}
	var inputs []eval.Input
	for _, s := range seqs {
		a := reflect.MakeSlice(argT, len(s), len(s))
		for j, v := range s {
			a.Index(j).Set(reflect.ValueOf(v).Convert(argT.Elem()))
		}
		inputs = append(inputs, eval.Input{Args: []any{a.Interface(), reflect.MakeSlice(resT, len(s), len(s)).Interface()}})
	}
	res, err := r.ev.Run(prog.Funcs()["lib"], inputs, eval.RunOptions{MaxSteps: r.p.MaxSteps})
	if err != nil {
		return err
	}
	if len(res.Violations) > 0 || res.Exhausted || len(res.ArgsAfter) != len(inputs) {
		return nil
	}
	outSeqs := make([][]any, len(inputs))
	for t, after := range res.ArgsAfter {
		if after == nil {
			return nil
		}
		rv := reflect.ValueOf(after[1])
		for j := range rv.Len() {
			outSeqs[t] = append(outSeqs[t], rv.Index(j).Interface())
		}
	}
	fp, ok := index.Of(rt.GoType(), outSeqs)
	if !ok {
		return nil
	}
	in0 := tir.Var{ID: 1, T: pt}
	res0 := tir.Var{ID: 2, T: rt}
	fr := &index.BodyFragment{
		Stmts:  []tir.Stmt{&tir.Assign{Dst: res0, Define: true, X: &tir.Call{Pkg: call.Pkg, Name: call.Name, Args: []tir.Operand{in0}}}},
		Result: res0,
		Inputs: []tir.Var{in0},
	}
	fr.Hash = tir.HashStmts(len(r.prob.Grammar.Sig.Params), append(tir.CloneStmts(fr.Stmts), &tir.Return{X: res0}))
	r.body.Add(fp, index.BodyEntry{Fragment: fr, InputFP: []index.Fingerprint{in}, PerExec: 1, Library: true})
	return nil
}

func goSliceOf(t tir.Type) (reflect.Type, bool) {
	m := map[tir.Type]reflect.Type{
		tir.Int: reflect.TypeFor[int](), tir.Int32: reflect.TypeFor[int32](),
		tir.Uint64: reflect.TypeFor[uint64](), tir.Bool: reflect.TypeFor[bool](),
	}
	e, ok := m[t]
	if !ok {
		return nil, false
	}
	return reflect.SliceOf(e), true
}

// bodyGeneration probes the generation (when substitution is on), adds its
// body fragments and the library donors to the body index, and keeps the
// point data for the next generation's substitutions. With substitution off
// (arm B) nothing is probed: that arm pays none of the cost.
func (r *runner) bodyGeneration(g int, all []*Individual) error {
	if r.p.Substitution == 0 {
		return nil
	}
	if n, ok := r.ev.(eval.Named); ok && !n.IsInstrument() {
		return nil
	}
	data, err := r.probeGeneration(g, all)
	if err != nil {
		return err
	}
	added := map[tir.Hash]bool{}
	for _, ind := range all {
		if !ind.Eligible() {
			continue
		}
		h := ind.F.StructHash()
		if added[h] {
			continue
		}
		added[h] = true
		for _, pd := range data[h] {
			if pd.Slice == nil {
				continue
			}
			in := make([]index.Fingerprint, len(pd.Slice.Inputs))
			ok := true
			for i, v := range pd.Slice.Inputs {
				j := slices.IndexFunc(pd.Scope, func(s index.ScopedFP) bool { return s.V.ID == v.ID })
				if j < 0 {
					ok = false
					break
				}
				in[i] = pd.Scope[j].FP
			}
			if !ok {
				continue
			}
			r.body.Add(pd.FP, index.BodyEntry{Fragment: pd.Slice, InputFP: in, PerExec: pd.PerExec, Genome: ind.ID})
			r.donorC[ind.ID] = ind.C
		}
	}
	if err := r.libraryDonors(g, data); err != nil {
		return err
	}
	clear(r.libSeqs)
	r.probedPrev = data
	r.points = data
	return nil
}

// bodyDonor serves ops.SubstituteBody from the body index and the parent's
// point data, and remembers which entry donated.
type bodyDonor struct {
	r     *runner
	entry index.BodyEntry
	used  bool
}

func (d *bodyDonor) Empty() bool { return d.r.body.Empty() }

func (d *bodyDonor) CheaperAt(f *tir.Func, p tir.BlockPoint) ([]tir.Stmt, tir.Var, map[int]tir.Var, bool) {
	for _, pd := range d.r.points[f.StructHash()] {
		if pd.Point.At != p.At || pd.Point.V.ID != p.V.ID || !slices.Equal(pd.Point.Path, p.Path) {
			continue
		}
		if pd.Slice == nil {
			return nil, tir.Var{}, nil, false
		}
		// A donor is cheaper only if it costs less than what substituting
		// it removes: the slice minus what its bound inputs keep alive.
		e, bind, ok := d.r.body.Cheaper(pd.FP, pd.Scope, func(e index.BodyEntry, bind map[int]tir.Var) bool {
			return e.PerExec < pd.removable(bind)
		})
		if !ok {
			return nil, tir.Var{}, nil, false
		}
		d.entry, d.used = e, true
		return tir.CloneStmts(e.Fragment.Stmts), e.Fragment.Result, bind, true
	}
	return nil, tir.Var{}, nil, false
}
