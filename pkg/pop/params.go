// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import "errors"

// Params is the run parameter set (PROJECT.md §4.5 defaults). It is written
// into the run record in full.
type Params struct {
	Seed        uint64 `json:"seed"`
	Generations int    `json:"generations"`

	Islands      int `json:"islands"`       // I
	Population   int `json:"population"`    // P, per island
	MigrateEvery int `json:"migrate_every"` // G
	Migrants     int `json:"migrants"`      // m

	// Each child comes from one operator: substitution with probability
	// Substitution when it applies, otherwise crossover with probability
	// Crossover, otherwise mutation. Substitution 0 is the ablation arm.
	Crossover    float64 `json:"crossover"`
	Substitution float64 `json:"substitution"`

	// SeedReference puts the parsed reference into each island's first
	// generation alongside the random genomes.
	SeedReference bool `json:"seed_reference"`
	// SeedIslands limits the seeding to the first SeedIslands islands;
	// 0 seeds every island.
	SeedIslands int `json:"seed_islands"`

	MaxSteps uint64 `json:"max_steps"` // giri step budget per genome over all vectors
	Elites   int    `json:"elites"`    // E: per-generation candidates for the native stage

	// MAP-Elites descriptor bucketing: cost bucket = floor(log2(cost+1)),
	// statement-count bucket = Len / LenBucket, plus max loop depth.
	LenBucket int `json:"len_bucket"`

	// CostWeights weight instruction classes in the abstract cost; a class
	// not listed weighs 1, and v0 lists none (§6.4). With no per-class
	// counts from the evaluator, cost is the step count.
	CostWeights map[string]float64 `json:"cost_weights"`

	Workers int `json:"workers"` // concurrent evaluations per island

	// ParsimonyAbove is the bounded-parsimony limit: after abstract cost,
	// selection prefers fewer statements above this many. Below it, length
	// is free (neutral dead code is kept). 0 counts every statement; a value
	// at or above the grammar's max_len turns parsimony off.
	ParsimonyAbove int `json:"parsimony_above"`

	// IndexBudget bounds the new fragments evaluated for the index per
	// generation (0 for no bound); see indexGeneration.
	IndexBudget int `json:"index_budget"`
}

// Defaults are PROJECT.md §4.5's values and this project's recorded choices.
func Defaults() Params {
	return Params{
		Seed:           1,
		Generations:    50,
		Islands:        8,
		Population:     256,
		MigrateEvery:   10,
		Migrants:       4,
		Crossover:      0.5,
		Substitution:   0.1,
		SeedReference:  true,
		MaxSteps:       1 << 20,
		Elites:         8,
		LenBucket:      4,
		CostWeights:    map[string]float64{},
		Workers:        4,
		IndexBudget:    4096,
		ParsimonyAbove: 24,
	}
}

// Check reports whether p is usable.
func (p Params) Check() error {
	switch {
	case p.Generations < 1, p.Islands < 1, p.Population < 2:
		return errors.New("pop: need at least one generation, one island, and two genomes per island")
	case p.Migrants < 0 || p.Migrants >= p.Population || p.MigrateEvery < 1:
		return errors.New("pop: bad migration parameters")
	case p.Crossover < 0 || p.Crossover > 1 || p.Substitution < 0 || p.Substitution > 1:
		return errors.New("pop: operator probabilities must be in [0, 1]")
	case p.Elites < 0 || p.LenBucket < 1 || p.Workers < 1 || p.IndexBudget < 0 || p.ParsimonyAbove < 0 || p.SeedIslands < 0:
		return errors.New("pop: bad elites, bucket, or worker count")
	}
	return nil
}
