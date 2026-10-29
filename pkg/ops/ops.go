// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"encoding/json"
	"fmt"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Kind is a variation operator.
type Kind int

// The operators of PROJECT.md §4.2.
const (
	Crossover Kind = iota
	Mutation
	Substitution
	numKinds
)

var kindNames = [numKinds]string{"crossover", "mutation", "substitution"}

func (k Kind) String() string { return kindNames[k] }

// Kinds are all operators, in order.
func Kinds() []Kind { return []Kind{Crossover, Mutation, Substitution} }

// Yield counts, per operator, the children attempted and the children that
// survived repair and the type checker (PROJECT.md §8, operator yield).
// Substitution counts only applications; a no-op (empty index) is neither.
type Yield struct {
	Attempted [numKinds]int
	Survived  [numKinds]int
}

// Record counts one attempt by k.
func (y *Yield) Record(k Kind, survived bool) {
	y.Attempted[k]++
	if survived {
		y.Survived[k]++
	}
}

// Add accumulates z into y.
func (y *Yield) Add(z Yield) {
	for k := range numKinds {
		y.Attempted[k] += z.Attempted[k]
		y.Survived[k] += z.Survived[k]
	}
}

// Rate is survived/attempted for k, or 0 with no attempts.
func (y Yield) Rate(k Kind) float64 {
	if y.Attempted[k] == 0 {
		return 0
	}
	return float64(y.Survived[k]) / float64(y.Attempted[k])
}

// MarshalJSON writes {"crossover": {"attempted": n, "survived": m, "rate": r}, ...}.
func (y Yield) MarshalJSON() ([]byte, error) {
	type entry struct {
		Attempted int     `json:"attempted"`
		Survived  int     `json:"survived"`
		Rate      float64 `json:"rate"`
	}
	m := map[string]entry{}
	for _, k := range Kinds() {
		m[k.String()] = entry{y.Attempted[k], y.Survived[k], y.Rate(k)}
	}
	return json.Marshal(m)
}

// Discarded is the error for a child that could not be repaired.
type Discarded struct {
	Op     Kind
	Reason error
}

func (d *Discarded) Error() string { return fmt.Sprintf("ops: %s child discarded: %v", d.Op, d.Reason) }
func (d *Discarded) Unwrap() error { return d.Reason }

// pos is a statement's place: its block and index in it.
type pos struct {
	block *[]tir.Stmt
	i     int
	depth int // block depth: 0 for the function body
}

func (p pos) stmt() tir.Stmt { return (*p.block)[p.i] }

// positions lists every statement of f in pre-order, sinks excluded.
func positions(f *tir.Func) []pos {
	var out []pos
	var walk func(b *[]tir.Stmt, depth int)
	walk = func(b *[]tir.Stmt, depth int) {
		for i, s := range *b {
			if _, ok := s.(*tir.Sink); !ok {
				out = append(out, pos{b, i, depth})
			}
			switch s := s.(type) {
			case *tir.Loop:
				walk(&s.Body, depth+1)
			case *tir.If:
				walk(&s.Then, depth+1)
				walk(&s.Else, depth+1)
			}
		}
	}
	walk(&f.Body, 0)
	return out
}

// blocks lists every block of f with its depth, in pre-order. Empty blocks
// are included: they are valid crossover sites.
func blocks(f *tir.Func) []pos {
	out := []pos{{&f.Body, 0, 0}}
	for _, p := range positions(f) {
		switch s := p.stmt().(type) {
		case *tir.Loop:
			out = append(out, pos{&s.Body, 0, p.depth + 1})
		case *tir.If:
			out = append(out, pos{&s.Then, 0, p.depth + 1}, pos{&s.Else, 0, p.depth + 1})
		}
	}
	return out
}

func insert(ss []tir.Stmt, i int, add ...tir.Stmt) []tir.Stmt {
	out := make([]tir.Stmt, 0, len(ss)+len(add))
	out = append(out, ss[:i]...)
	out = append(out, add...)
	return append(out, ss[i:]...)
}

func remove(ss []tir.Stmt, i, j int) []tir.Stmt {
	out := make([]tir.Stmt, 0, len(ss)-(j-i))
	out = append(out, ss[:i]...)
	return append(out, ss[j:]...)
}
