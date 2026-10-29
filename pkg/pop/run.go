// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/index"
	"github.com/scttfrdmn/tumble/pkg/ops"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Problem is what a run searches: the grammar (with the kernel signature),
// the selection vectors and the reference's outputs on them, and the parsed
// reference for seeding.
type Problem struct {
	Grammar   *grammar.Grammar
	Reference *tir.Func
	Selection []eval.Input
	Want      [][]any
}

// Readings are one generation's instrument readings (PROJECT.md §8). Under
// eval.stub none of them is a measurement.
type Readings struct {
	Gen         int    `json:"gen"`
	Evaluations int    `json:"evaluations"`
	Evaluator   string `json:"evaluator"`

	// Discards by fitness-cascade stage: "typecheck" (children ops could
	// not repair), "violation", "exhausted".
	Discards map[string]int `json:"discards"`

	// Yield is the operator yield of breeding the next generation from
	// this one.
	Yield ops.Yield `json:"yield"`

	// VCHitRate is the value-cache hit rate; VCMeasured is false when the
	// evaluator has no value cache (eval.stub), and the rate is then 0.
	VCHitRate  float64 `json:"vc_hit_rate"`
	VCMeasured bool    `json:"vc_measured"`

	Substitutions int    `json:"substitutions"` // substitution children evaluated
	FPCollisions  int    `json:"fp_collisions"` // of those, failing a vector their donor passed
	IndexSize     int    `json:"index_size"`    // distinct fingerprints
	Fragments     int    `json:"fragments"`     // distinct fragments in the hash-cons table
	ArchiveSize   int    `json:"archive_size"`  // occupied MAP-Elites cells
	FullyCorrect  int    `json:"fully_correct"` // this generation's genomes correct on every vector
	BestCost      uint64 `json:"best_cost"`     // lowest abstract cost of a fully correct genome so far; 0 if none
	BestCorrect   int    `json:"best_correct"`  // vectors correct of the archive's best

	// Improvements counts children that beat their best parent (more
	// vectors correct, or as many at lower cost), by operator.
	Improvements map[string]int `json:"improvements"`

	// Elites are this generation's native-stage candidates: the top E
	// fully correct genomes by abstract cost.
	Elites []*Individual `json:"-"`
}

// Observer receives each generation's readings and the archive after it.
// Returning ErrStop ends the run after that generation, without error.
type Observer func(r *Readings, a *Archive) error

// ErrStop is an Observer's request to end the run (a wall-clock budget).
var ErrStop = errors.New("pop: stop")

// Result is a finished run.
type Result struct {
	Archive *Archive
	Index   *index.Index
	Gens    int
}

type island struct {
	n   int
	r   *rand.Rand
	pop []*Individual // the generation being evaluated (F, Origin, lineage set)
}

// Run evolves prob under params with ev, writing generation packages under
// work. Islands evaluate each generation in parallel; the archive, index,
// and migration are then updated at a barrier in island order, so the run
// is a deterministic function of params.Seed, the problem, and the
// evaluator.
func Run(ev eval.Evaluator, prob Problem, p Params, work string, obs Observer) (*Result, error) {
	if err := p.Check(); err != nil {
		return nil, err
	}
	if p.SeedReference && prob.Reference == nil {
		return nil, errors.New("pop: SeedReference without a reference")
	}
	evName := "unknown"
	if n, ok := ev.(eval.Named); ok {
		evName = n.Name()
	}
	r := &runner{ev: ev, prob: prob, p: p, work: work, ix: index.New(), archive: NewArchive(p.LenBucket), donorC: map[uint64][]bool{}}
	islands := make([]*island, p.Islands)
	for i := range islands {
		is := &island{n: i, r: rand.New(rand.NewPCG(p.Seed, uint64(i)))}
		gen := grammar.Gen{G: prob.Grammar, R: is.r}
		n := p.Population
		if p.SeedReference {
			is.pop = append(is.pop, &Individual{F: prob.Reference.Clone(), Origin: Seed})
			n--
		}
		for _, f := range gen.Ramped(n) {
			is.pop = append(is.pop, &Individual{F: f, Origin: Random})
		}
		islands[i] = is
	}

	for g := range p.Generations {
		rd := &Readings{Gen: g, Evaluator: evName, Discards: map[string]int{}, Improvements: map[string]int{}}
		errs := make([]error, len(islands))
		var wg sync.WaitGroup
		for i, is := range islands {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = r.evaluate(is, g)
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			return nil, err
		}

		// Barrier: merge in island order.
		var all []*Individual
		for _, is := range islands {
			all = append(all, is.pop...)
		}
		for _, ind := range all {
			rd.Evaluations++
			if ind.Discard != "" {
				rd.Discards[ind.Discard]++
			}
			if ind.FullyCorrect() {
				rd.FullyCorrect++
			}
			if ind.Origin == FromSubstitution && ind.Eligible() {
				rd.Substitutions++
				if collides(ind) {
					rd.FPCollisions++
				}
			}
			if pb := ind.parentBest; pb != nil && ind.Eligible() &&
				(ind.Correct > pb.Correct || (ind.Correct == pb.Correct && ind.Cost < pb.Cost)) {
				rd.Improvements[ind.Origin.String()]++
			}
			// Lineage is only needed for this generation's readings; keeping
			// it would chain every individual to all its ancestors.
			ind.parentBest, ind.donorC = nil, nil
			r.archive.Offer(ind)
		}
		if err := r.indexGeneration(g, all); err != nil {
			return nil, err
		}
		for _, is := range islands {
			if err := os.RemoveAll(filepath.Join(r.work, fmt.Sprintf("island%02d", is.n), fmt.Sprintf("gen%04d", g))); err != nil {
				return nil, err
			}
		}
		rd.IndexSize, rd.Fragments, rd.ArchiveSize = r.ix.Len(), r.ix.HC.Len(), r.archive.Len()
		if best := r.archive.Best(); best != nil {
			rd.BestCorrect = best.Correct
			if best.FullyCorrect() {
				rd.BestCost = best.Cost
			}
		}
		rd.Elites = elites(all, p.Elites)

		if g+1 < p.Generations {
			var migrants [][]*Individual
			if p.Islands > 1 && p.Migrants > 0 && (g+1)%p.MigrateEvery == 0 {
				for _, is := range islands {
					migrants = append(migrants, top(is.pop, p.Migrants))
				}
			}
			ys := make([]ops.Yield, len(islands))
			discards := make([]int, len(islands))
			for i, is := range islands {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var in []*Individual
					if migrants != nil {
						in = migrants[(i+len(islands)-1)%len(islands)]
					}
					ys[i], discards[i] = r.breed(is, g, in)
				}()
			}
			wg.Wait()
			for i := range islands {
				rd.Yield.Add(ys[i])
				rd.Discards["typecheck"] += discards[i]
			}
		}
		if obs != nil {
			if err := obs(rd, r.archive); errors.Is(err, ErrStop) {
				return &Result{Archive: r.archive, Index: r.ix, Gens: g + 1}, nil
			} else if err != nil {
				return nil, err
			}
		}
	}
	return &Result{Archive: r.archive, Index: r.ix, Gens: p.Generations}, nil
}

type runner struct {
	ev      eval.Evaluator
	prob    Problem
	p       Params
	work    string
	ix      *index.Index
	archive *Archive
	donorC  map[uint64][]bool // correctness of genomes that donated fragments

	// evaluated keeps each generation's source and functions for the
	// index update at the barrier, per island.
	mu        sync.Mutex
	evaluated map[int]*evalRecord
}

type evalRecord struct {
	src    []byte
	names  []string
	prog   eval.Program
	traces [][]eval.ValueTrace
}

// evaluate runs island is's generation g through the cascade's giri stage.
func (r *runner) evaluate(is *island, g int) error {
	dir := filepath.Join(r.work, fmt.Sprintf("island%02d", is.n), fmt.Sprintf("gen%04d", g))
	named := make([]tir.Named, len(is.pop))
	names := make([]string, len(is.pop))
	for i, ind := range is.pop {
		ind.ID = uint64(g)*uint64(r.p.Islands)*uint64(r.p.Population) + uint64(is.n)*uint64(r.p.Population) + uint64(i)
		ind.Island, ind.Gen = is.n, g
		names[i] = fmt.Sprintf("g_%04d", i+1)
		named[i] = tir.Named{Name: names[i], F: ind.F}
	}
	src := tir.PrintFile("g", named)
	if err := eval.WriteGeneration(dir, src); err != nil {
		return err
	}
	prog, err := r.ev.Build(dir)
	if err != nil {
		return fmt.Errorf("pop: island %d generation %d: %w", is.n, g, err)
	}
	fns := prog.Funcs()
	results := make([]eval.Result, len(is.pop))
	errs := make([]error, len(is.pop))
	var wg sync.WaitGroup
	sem := make(chan struct{}, r.p.Workers)
	for i := range is.pop {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			results[i], errs[i] = r.ev.Run(fns[names[i]], r.prob.Selection, eval.RunOptions{MaxSteps: r.p.MaxSteps, Trace: true, TraceCap: 4096})
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	rec := &evalRecord{src: src, names: names, prog: prog, traces: make([][]eval.ValueTrace, len(is.pop))}
	for i, ind := range is.pop {
		r.score(ind, results[i])
		rec.traces[i] = results[i].Traces
	}
	r.mu.Lock()
	if r.evaluated == nil {
		r.evaluated = map[int]*evalRecord{}
	}
	r.evaluated[is.n] = rec
	r.mu.Unlock()
	return nil
}

func (r *runner) score(ind *Individual, res eval.Result) {
	switch {
	case len(res.Violations) > 0:
		ind.Discard = "violation"
	case res.Exhausted:
		ind.Discard = "exhausted"
	}
	ind.C = make([]bool, len(r.prob.Want))
	ind.Correct = 0
	for t, w := range r.prob.Want {
		if t < len(res.Outputs) && reflect.DeepEqual(res.Outputs[t], w) {
			ind.C[t] = true
			ind.Correct++
		}
	}
	ind.Cost = r.cost(res)
	ind.FPs = nil
	for _, tr := range res.Traces {
		if fp, ok := index.FromTrace(tr); ok {
			ind.FPs = append(ind.FPs, fp)
		}
	}
	ind.Novelty = r.ix.Novelty(ind.FPs) // the index is read-only while islands evaluate
}

// cost is the abstract cost: per-class counts weighted by CostWeights
// (default 1), or the step count when the evaluator gives no breakdown.
func (r *runner) cost(res eval.Result) uint64 {
	if res.StepsBy == nil {
		return res.Steps
	}
	classes := make([]string, 0, len(res.StepsBy))
	for c := range res.StepsBy {
		classes = append(classes, c)
	}
	slices.Sort(classes)
	total := 0.0
	for _, c := range classes {
		w, ok := r.p.CostWeights[c]
		if !ok {
			w = 1
		}
		total += w * float64(res.StepsBy[c])
	}
	return uint64(total + 0.5)
}

func collides(ind *Individual) bool {
	for t, ok := range ind.donorC {
		if ok && !ind.C[t] {
			return true
		}
	}
	return false
}

// indexGeneration adds the generation's new fragments to the index, in
// island and genome order: each eligible genome's top-level definitions
// that carry a fingerprint (only when the evaluator traces), evaluated as
// fragment functions for their cost. Under eval.stub there are no traces,
// and nothing is added.
func (r *runner) indexGeneration(g int, all []*Individual) error {
	type pending struct {
		fp     index.Fingerprint
		fr     *index.Fragment
		genome *Individual
	}
	var todo []pending
	seen := map[tir.Hash]bool{}
	r.mu.Lock()
	recs := r.evaluated
	r.evaluated = nil
	r.mu.Unlock()
	defer func() {
		for _, rec := range recs {
			if rel, ok := r.ev.(interface{ Release(eval.Program) error }); ok {
				_ = rel.Release(rec.prog)
			}
		}
	}()
	for i := range r.p.Islands {
		rec := recs[i]
		if rec == nil {
			continue
		}
		fns := rec.prog.Funcs()
		for j, name := range rec.names {
			ind := all[i*r.p.Population+j]
			if !ind.Eligible() || len(rec.traces[j]) == 0 {
				continue
			}
			fps := VarFingerprints(rec.src, name, ind.F, fns[name], rec.traces[j])
			for _, s := range ind.F.Body {
				a, ok := s.(*tir.Assign)
				if !ok || !a.Define {
					continue
				}
				fp, ok := fps[a.Dst.ID]
				if !ok {
					continue
				}
				fr, ok := index.Slice(ind.F, a.Dst)
				if !ok || seen[fr.Hash] {
					continue
				}
				if _, known := r.ix.FingerprintOf(fr.Hash); known {
					continue
				}
				seen[fr.Hash] = true
				todo = append(todo, pending{fp, fr, ind})
			}
		}
	}
	if len(todo) == 0 {
		return nil
	}

	// Evaluate the new fragments for their cost, one package per result
	// type (evaluators may require one signature per package).
	byType := map[tir.Type][]int{}
	var types []tir.Type
	for k, pd := range todo {
		t := pd.fr.Result.T
		if _, ok := byType[t]; !ok {
			types = append(types, t)
		}
		byType[t] = append(byType[t], k)
	}
	costs := make([]uint64, len(todo))
	ok := make([]bool, len(todo))
	for ti, t := range types {
		ks := byType[t]
		named := make([]tir.Named, len(ks))
		for n, k := range ks {
			named[n] = tir.Named{Name: fmt.Sprintf("f_%04d", n+1), F: todo[k].fr.Func(r.prob.Grammar.Sig)}
		}
		dir := filepath.Join(r.work, "fragments", fmt.Sprintf("gen%04d-%d", g, ti))
		if err := eval.WriteGeneration(dir, tir.PrintFile("g", named)); err != nil {
			return err
		}
		prog, err := r.ev.Build(dir)
		if err != nil {
			return err
		}
		fns := prog.Funcs()
		for n, k := range ks {
			res, err := r.ev.Run(fns[named[n].Name], r.prob.Selection, eval.RunOptions{MaxSteps: r.p.MaxSteps})
			if err != nil {
				return err
			}
			if len(res.Violations) == 0 && !res.Exhausted {
				costs[k], ok[k] = r.cost(res), true
			}
		}
		if rel, isRel := r.ev.(interface{ Release(eval.Program) error }); isRel {
			_ = rel.Release(prog)
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	for k, pd := range todo {
		if ok[k] {
			r.ix.Add(pd.fp, pd.fr, costs[k], pd.genome.ID)
			r.donorC[pd.genome.ID] = pd.genome.C
		}
	}
	return nil
}

// top is the best n eligible individuals of pop by rank.
func top(pop []*Individual, n int) []*Individual {
	var el []*Individual
	for _, ind := range pop {
		if ind.Eligible() {
			el = append(el, ind)
		}
	}
	slices.SortStableFunc(el, better)
	return el[:min(n, len(el))]
}

// elites are the top e fully correct individuals by abstract cost, one per
// phenotype.
func elites(all []*Individual, e int) []*Individual {
	var fc []*Individual
	seen := map[[32]byte]bool{}
	for _, ind := range all {
		if !ind.FullyCorrect() {
			continue
		}
		if id := ind.F.Identity(); !seen[id] {
			seen[id] = true
			fc = append(fc, ind)
		}
	}
	slices.SortStableFunc(fc, better)
	return fc[:min(e, len(fc))]
}

// recordingDonor wraps the index for one substitution and remembers which
// entry donated.
type recordingDonor struct {
	ix    *index.Index
	entry index.Entry
	used  bool
}

func (d *recordingDonor) Empty() bool { return d.ix.Empty() }

func (d *recordingDonor) Cheaper(f *tir.Func, v tir.Var) ([]tir.Stmt, tir.Var, bool) {
	stmts, res, e, ok := d.ix.CheaperEntry(f, v)
	if ok {
		d.entry, d.used = e, true
	}
	return stmts, res, ok
}

// breed replaces is's population with the next generation: in migrants
// first, then children of lexicase-selected parents, one operator each. It
// returns the operator yield and the number of discarded children.
func (r *runner) breed(is *island, g int, in []*Individual) (ops.Yield, int) {
	var y ops.Yield
	discarded := 0
	gram := r.prob.Grammar
	var eligible []*Individual
	for _, ind := range is.pop {
		if ind.Eligible() {
			eligible = append(eligible, ind)
		}
	}
	next := make([]*Individual, 0, r.p.Population)
	for _, m := range in {
		next = append(next, &Individual{F: m.F.Clone(), Origin: Migrant})
	}
	if len(eligible) == 0 {
		// Nothing survived the cascade: restart the island from random
		// genomes rather than breed from discards.
		for _, f := range (grammar.Gen{G: gram, R: is.r}).Ramped(r.p.Population - len(next)) {
			next = append(next, &Individual{F: f, Origin: Random})
		}
		is.pop = next
		return y, 0
	}
	for attempts := 0; len(next) < r.p.Population; attempts++ {
		if attempts > 50*r.p.Population {
			f := (grammar.Gen{G: gram, R: is.r}).Random(gram.InitMaxLen)
			next = append(next, &Individual{F: f, Origin: Random})
			continue
		}
		if r.p.Substitution > 0 && is.r.Float64() < r.p.Substitution {
			parent := Lexicase(is.r, eligible)
			d := &recordingDonor{ix: r.ix}
			child, applied, err := ops.Substitute(is.r, gram, d, parent.F)
			if applied {
				y.Record(ops.Substitution, err == nil)
				if err != nil {
					discarded++
					continue
				}
				ind := &Individual{F: child, Origin: FromSubstitution, parentBest: parent}
				if d.used {
					ind.donorC = r.donorC[d.entry.Genome]
				}
				next = append(next, ind)
				continue
			}
		}
		if is.r.Float64() < r.p.Crossover {
			a, b := Lexicase(is.r, eligible), Lexicase(is.r, eligible)
			child, err := ops.TwoPoint(is.r, gram, a.F, b.F)
			y.Record(ops.Crossover, err == nil)
			if err != nil {
				discarded++
				continue
			}
			pb := a
			if better(b, a) < 0 {
				pb = b
			}
			next = append(next, &Individual{F: child, Origin: FromCrossover, parentBest: pb})
			continue
		}
		parent := Lexicase(is.r, eligible)
		child, err := ops.Point(is.r, gram, parent.F)
		y.Record(ops.Mutation, err == nil)
		if err != nil {
			discarded++
			continue
		}
		next = append(next, &Individual{F: child, Origin: FromMutation, parentBest: parent})
	}
	is.pop = next
	return y, discarded
}
