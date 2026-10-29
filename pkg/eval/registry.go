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
}

// Constructor builds an evaluator using scratch space under work.
type Constructor func(cfg Config, work string) (Named, error)

var constructors = map[string]Constructor{
	"stub": func(cfg Config, work string) (Named, error) { return NewStub(cfg.Giri, work) },
}

// Register adds an evaluator constructor; the giri adapter registers itself
// when built with -tags giriapi.
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
		hint := ""
		if cfg.Name == "giri" {
			hint = " (the giri adapter needs giri's library API and -tags giriapi; see scttfrdmn/tumble#14)"
		}
		return nil, fmt.Errorf("eval: unknown evaluator %q, have %s%s", cfg.Name, strings.Join(names, ", "), hint)
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
