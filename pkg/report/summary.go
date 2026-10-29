// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"cmp"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
)

// Summary is a run's E0 readings (PROJECT.md §8) computed from its record.
type Summary struct {
	Generations int `json:"generations"`

	// Operator yield over the run, per operator: survived / attempted.
	Yield map[string]Rate `json:"yield"`

	// FP collisions: substitution children failing a vector their donor
	// passed, over substitution children evaluated.
	Collisions Rate `json:"fp_collisions"`

	// Cost-model fit: Spearman rank correlation between abstract cost and
	// native ns/op over the natively benchmarked, verified genomes, one per
	// benchmark (input size). Rows without an interval are left out.
	CostFit []Fit `json:"cost_fit"`

	// Timing: mean wall seconds per generation, by phase, and island-wall
	// nanoseconds of interpretation per interpreted instruction.
	MeanSeconds float64            `json:"mean_seconds_per_generation"`
	MeanPhases  map[string]float64 `json:"mean_phase_seconds"`
	NsPerInstr  float64            `json:"interpret_ns_per_instruction"`

	// MaxLoad is the highest 1-minute load average recorded (-1 if none).
	MaxLoad float64 `json:"max_load1"`
}

// Rate is a count over a count; Value is 0, and Measured false, over zero.
type Rate struct {
	Num, Den int
	Value    float64
	Measured bool
}

func rate(n, d int) Rate {
	if d == 0 {
		return Rate{Num: n, Den: d}
	}
	return Rate{Num: n, Den: d, Value: float64(n) / float64(d), Measured: true}
}

// Fit is one benchmark's cost-model fit.
type Fit struct {
	Benchmark string  `json:"benchmark"`
	N         int     `json:"n"`
	Spearman  float64 `json:"spearman"` // NaN when N < 3 or a side is constant
}

// Summarize computes the summary of a loaded record. The timing file is
// read from dir.
func Summarize(dir string, l *Loaded) (*Summary, error) {
	s := &Summary{Generations: len(l.Generations), Yield: map[string]Rate{}, MeanPhases: map[string]float64{}}
	att, surv := map[string]int{}, map[string]int{}
	subs, colls := 0, 0
	for _, g := range l.Generations {
		var rd struct {
			Yield map[string]struct {
				Attempted int `json:"attempted"`
				Survived  int `json:"survived"`
			} `json:"yield"`
			Substitutions int    `json:"substitutions"`
			FPCollisions  int    `json:"fp_collisions"`
			Steps         uint64 `json:"steps"`
		}
		if err := json.Unmarshal(g, &rd); err != nil {
			return nil, err
		}
		for k, y := range rd.Yield {
			att[k] += y.Attempted
			surv[k] += y.Survived
		}
		subs += rd.Substitutions
		colls += rd.FPCollisions
	}
	for k := range att {
		s.Yield[k] = rate(surv[k], att[k])
	}
	s.Collisions = rate(colls, subs)

	// Cost-model fit.
	type pair struct{ cost, ns float64 }
	byBench := map[string][]pair{}
	for _, e := range l.Native {
		if e.Result == nil || !e.Result.Verified {
			continue
		}
		for _, r := range e.Result.Rows {
			if r.NoInterval {
				continue // not a result
			}
			byBench[r.Name] = append(byBench[r.Name], pair{float64(e.Cost), r.NsOp})
		}
	}
	var names []string
	for n := range byBench {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		ps := byBench[n]
		xs, ys := make([]float64, len(ps)), make([]float64, len(ps))
		for i, p := range ps {
			xs[i], ys[i] = p.cost, p.ns
		}
		s.CostFit = append(s.CostFit, Fit{Benchmark: n, N: len(ps), Spearman: Spearman(xs, ys)})
	}

	// Timing.
	lines, err := readLines(filepath.Join(dir, TimingFile))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var steps uint64
	for _, g := range l.Generations {
		var rd struct {
			Steps uint64 `json:"steps"`
		}
		_ = json.Unmarshal(g, &rd)
		steps += rd.Steps
	}
	var interp float64
	s.MaxLoad = -1
	for _, ln := range lines {
		var t TimingEntry
		if err := json.Unmarshal(ln, &t); err != nil {
			return nil, err
		}
		s.MeanSeconds += t.Seconds
		s.MeanPhases["evaluate"] += t.Phases.Evaluate
		s.MeanPhases["build"] += t.Phases.Build
		s.MeanPhases["interpret"] += t.Phases.Interpret
		s.MeanPhases["index"] += t.Phases.Index
		s.MeanPhases["breed"] += t.Phases.Breed
		s.MeanPhases["native"] += t.Native
		interp += t.Phases.Interpret
		if t.Load != nil {
			s.MaxLoad = max(s.MaxLoad, *t.Load)
		}
	}
	if n := float64(len(lines)); n > 0 {
		s.MeanSeconds /= n
		for k := range s.MeanPhases {
			s.MeanPhases[k] /= n
		}
	}
	if steps > 0 {
		s.NsPerInstr = interp * 1e9 / float64(steps)
	}
	return s, nil
}

// Spearman is the rank correlation of xs and ys, ties given their average
// rank; NaN if fewer than three pairs or either side is constant.
func Spearman(xs, ys []float64) float64 {
	if len(xs) != len(ys) || len(xs) < 3 {
		return math.NaN()
	}
	rx, ry := ranks(xs), ranks(ys)
	return pearson(rx, ry)
}

func ranks(xs []float64) []float64 {
	idx := make([]int, len(xs))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(xs[a], xs[b]) })
	r := make([]float64, len(xs))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

func pearson(xs, ys []float64) float64 {
	n := float64(len(xs))
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx, my = mx/n, my/n
	var sxy, sxx, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}
