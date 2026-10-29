// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"fmt"
	"slices"
	"strings"
)

// Named is an evaluator that reports its name and whether its readings are
// measurements.
type Named interface {
	Evaluator
	Name() string
	IsInstrument() bool
}

// Config selects and configures an evaluator in a run config.
type Config struct {
	Name string `json:"name"`           // "stub" or "giri"
	Giri string `json:"giri,omitempty"` // the giri binary, for the stub ("" = PATH)

	// For the giri evaluator: the seed for giri's modelled randomness, and
	// the value cache (on unless NoCache; CacheEntries bounds it, 0 for
	// DefaultCacheEntries).
	Seed         int64 `json:"seed,omitempty"`
	NoCache      bool  `json:"no_cache,omitempty"`
	CacheEntries int   `json:"cache_entries,omitempty"`
}

// Constructor builds an evaluator using scratch space under work.
type Constructor func(cfg Config, work string) (Named, error)

var constructors = map[string]Constructor{
	"stub": func(cfg Config, work string) (Named, error) { return NewStub(cfg.Giri, work) },
}

// Register adds an evaluator constructor.
func Register(name string, c Constructor) { constructors[name] = c }

// New builds the evaluator cfg names.
func New(cfg Config, work string) (Named, error) {
	c, ok := constructors[cfg.Name]
	if !ok {
		var names []string
		for n := range constructors {
			names = append(names, n)
		}
		slices.Sort(names)
		return nil, fmt.Errorf("eval: unknown evaluator %q, have %s", cfg.Name, strings.Join(names, ", "))
	}
	return c(cfg, work)
}

// CheckExperiment refuses an evaluator that is not an instrument: no
// experiment runs against the stub (SETUP.md).
func CheckExperiment(e Named) error {
	if !e.IsInstrument() {
		return fmt.Errorf("eval: %s is not an instrument; experiments refuse to run on it", e.Name())
	}
	return nil
}
