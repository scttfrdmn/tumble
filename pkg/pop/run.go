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
	"time"

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

	// VCHitRate is the value-cache hit rate over this generation's genome
	// evaluations: pure instructions served from the cache over all pure
	// instructions (served plus executed). The cache is per instruction
	// (giri's hook), so a hit saves that instruction, not its operands'
	// computation. VCMeasured is false when the evaluator has no value
	// cache (eval.stub), and the rate is then 0.
	VCHitRate      float64 `json:"vc_hit_rate"`
	VCMeasured     bool    `json:"vc_measured"`
	VCHits         uint64  `json:"vc_hits"`
	VCPureExecuted uint64  `json:"vc_pure_executed"`
	VCEntries      int     `json:"vc_entries"` // committed cache entries after this generation
	VCFull         bool    `json:"vc_full"`    // the cache reached its bound and dropped entries

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

	// Steps is the generation's interpreted instructions over its genome
	// evaluations (fragment evaluations not included).
	Steps uint64 `json:"steps"`

	// Timing is the generation's wall time by phase. It varies run to
	// run, so it stays out of the readings' JSON; the run record writes it
	// to a file of its own.
	Timing Timing `json:"-"`
}

// Timing is one generation's wall time by phase, in seconds. Build and
// Interpret are summed over islands, which run them concurrently.
type Timing struct {
	Evaluate  float64 `json:"evaluate"`  // the parallel evaluation phase, wall
	Build     float64 `json:"build"`     // writing, type-checking and building packages, island sum
	Interpret float64 `json:"interpret"` // running genomes, island sum
	Index     float64 `json:"index"`     // the barrier: fragments evaluated and indexed
	Breed     float64 `json:"breed"`     // the next generation's operators, wall
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
	r := &runner{ev: ev, prob: prob, p: p, work: work, ix: index.New(), archive: NewArchive(p.LenBucket), donorC: map[uint64][]bool{}, failed: map[tir.Hash]bool{}}
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
		times := make([][2]float64, len(islands))
		t0 := time.Now()
		var wg sync.WaitGroup
		for i, is := range islands {
			wg.Add(1)
			go func() {
				defer wg.Done()
				times[i], errs[i] = r.evaluate(is, g)
			}()
		}
		wg.Wait()
		rd.Timing.Evaluate = time.Since(t0).Seconds()
		for _, tm := range times {
			rd.Timing.Build += tm[0]
			rd.Timing.Interpret += tm[1]
		}
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
			rd.Steps += ind.steps
			rd.VCMeasured = rd.VCMeasured || ind.vcMeasured
			rd.VCHits += ind.vcHits
			rd.VCPureExecuted += ind.vcPure
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
		}
		t1 := time.Now()
		if err := r.indexGeneration(g, all); err != nil {
			return nil, err
		}
		rd.Timing.Index = time.Since(t1).Seconds()
		// Placement after indexing: novelty, the archive's tiebreaker, is
		// set there.
		for _, ind := range all {
			r.archive.Offer(ind)
		}
		if pure := rd.VCHits + rd.VCPureExecuted; pure > 0 {
			rd.VCHitRate = float64(rd.VCHits) / float64(pure)
		}
		// Commit the generation's cache entries: the next generation's
		// lookups see them (eval.ValueCache).
		if c, ok := r.ev.(interface{ EndGeneration() }); ok {
			c.EndGeneration()
		}
		if c, ok := r.ev.(interface{ Cache() *eval.ValueCache }); ok && c.Cache() != nil {
			rd.VCEntries, rd.VCFull = c.Cache().Len(), c.Cache().Full()
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
			t2 := time.Now()
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
			rd.Timing.Breed = time.Since(t2).Seconds()
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

	// failed are fragments whose evaluation faulted or ran out of budget on
	// some vector: they have no fingerprint and are not evaluated again.
	failed map[tir.Hash]bool
}

// evaluate runs island is's generation g through the cascade's giri stage.
// evaluate returns the island's build and interpretation wall seconds.
func (r *runner) evaluate(is *island, g int) (times [2]float64, err error) {
	start := time.Now()
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
		return times, err
	}
	prog, err := r.ev.Build(dir)
	if err != nil {
		return times, fmt.Errorf("pop: island %d generation %d: %w", is.n, g, err)
	}
	times[0] = time.Since(start).Seconds()
	start = time.Now()
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
			results[i], errs[i] = r.ev.Run(fns[names[i]], r.prob.Selection, eval.RunOptions{MaxSteps: r.p.MaxSteps})
		}()
	}
	wg.Wait()
	times[1] = time.Since(start).Seconds()
	if err := errors.Join(errs...); err != nil {
		return times, err
	}
	for i, ind := range is.pop {
		r.score(ind, results[i])
	}
	r.release(prog)
	return times, nil
}

func (r *runner) release(p eval.Program) {
	if rel, ok := r.ev.(interface{ Release(eval.Program) error }); ok {
		_ = rel.Release(p)
	}
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
	ind.vcHits, ind.vcPure, ind.vcMeasured = res.CacheHits, res.PureExecuted, res.CacheMeasured
	ind.steps = res.Steps
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

// indexGeneration adds the generation's new fragments to the index and sets
// each genome's novelty. A genome's fragments are the backward slices at its
// top-level points (tir.Points). A fragment not seen before is evaluated as
// a function returning its variable (Fragment.Func), one package per result
// type; its outputs on the selection vectors are its fingerprint
// (index.FromOutputs) and its abstract cost is the entry's cost. A fragment
// that faults or exhausts the budget on some vector has no fingerprint.
// Novelty is the number of distinct fingerprints a genome's fragments have
// that were not in the index before this generation. Everything runs in
// island and genome order, so the index is a function of the seed.
//
// The stub is not an instrument, and its step counts cost a giri search per
// fragment, so under it the index stays empty.
func (r *runner) indexGeneration(g int, all []*Individual) error {
	if n, ok := r.ev.(eval.Named); ok && !n.IsInstrument() {
		return nil
	}
	type pending struct {
		fr     *index.Fragment
		genome *Individual
	}
	var todo []pending
	queued := map[tir.Hash]int{} // fragment hash → index into todo
	frags := make([][]tir.Hash, len(all))
	for i, ind := range all {
		if !ind.Eligible() {
			continue
		}
		for _, p := range tir.Points(ind.F) {
			fr, ok := index.Slice(ind.F, p.At, p.V)
			if !ok || !fingerprintable(fr.Result.T) {
				continue
			}
			frags[i] = append(frags[i], fr.Hash)
			if _, known := r.ix.FingerprintOf(fr.Hash); known || r.failed[fr.Hash] {
				continue
			}
			if _, q := queued[fr.Hash]; q {
				continue
			}
			queued[fr.Hash] = len(todo)
			todo = append(todo, pending{fr, ind})
		}
	}

	fps := make([]index.Fingerprint, len(todo))
	costs := make([]uint64, len(todo))
	ok := make([]bool, len(todo))
	byType := map[tir.Type][]int{}
	var types []tir.Type
	for k, pd := range todo {
		t := pd.fr.Result.T
		if _, seen := byType[t]; !seen {
			types = append(types, t)
		}
		byType[t] = append(byType[t], k)
	}
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
				fps[k], ok[k] = index.FromOutputs(t.GoType(), res.Outputs)
				costs[k] = r.cost(res)
			}
		}
		r.release(prog)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}

	// Novelty against the index as it stood before this generation.
	fpOf := func(h tir.Hash) (index.Fingerprint, bool) {
		if fp, known := r.ix.FingerprintOf(h); known {
			return fp, true
		}
		if k, q := queued[h]; q && ok[k] {
			return fps[k], true
		}
		return index.Fingerprint{}, false
	}
	for i, ind := range all {
		var mine []index.Fingerprint
		for _, h := range frags[i] {
			if fp, has := fpOf(h); has {
				mine = append(mine, fp)
			}
		}
		ind.Novelty = r.ix.Novelty(mine)
	}

	for k, pd := range todo {
		if !ok[k] {
			r.failed[pd.fr.Hash] = true
			continue
		}
		r.ix.Add(fps[k], pd.fr, costs[k], pd.genome.ID)
		r.donorC[pd.genome.ID] = pd.genome.C
	}
	return nil
}

// fingerprintable reports whether v0 fingerprints cover a value of type t.
func fingerprintable(t tir.Type) bool { return !t.Slice && t.Valid() }

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

func (d *recordingDonor) Cheaper(f *tir.Func, p tir.Point) ([]tir.Stmt, tir.Var, bool) {
	stmts, res, e, ok := d.ix.CheaperEntry(f, p)
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
