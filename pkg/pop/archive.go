// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import (
	"cmp"
	"math/bits"
	"slices"
)

// Cell is a MAP-Elites descriptor (PROJECT.md §4.5): abstract cost bucket
// floor(log2(cost+1)), statement-count bucket, and maximum loop depth.
type Cell struct {
	CostBucket int `json:"cost_bucket"`
	LenBucket  int `json:"len_bucket"`
	Depth      int `json:"depth"`
}

func compareCells(a, b Cell) int {
	return cmp.Or(cmp.Compare(a.CostBucket, b.CostBucket), cmp.Compare(a.LenBucket, b.LenBucket), cmp.Compare(a.Depth, b.Depth))
}

// Archive is the global MAP-Elites archive: the best eligible individual per
// cell, by the rank order of better (more vectors correct first, then cost,
// then novelty as the tiebreaker). It is the only place genomes persist
// across generations (§6.5).
type Archive struct {
	lenBucket int
	cells     map[Cell]*Individual
}

// NewArchive returns an empty archive.
func NewArchive(lenBucket int) *Archive {
	return &Archive{lenBucket: lenBucket, cells: map[Cell]*Individual{}}
}

// CellOf is ind's descriptor.
func (a *Archive) CellOf(ind *Individual) Cell {
	return Cell{
		CostBucket: bits.Len64(ind.Cost),
		LenBucket:  ind.F.Len() / a.lenBucket,
		Depth:      ind.F.Depth(),
	}
}

// Offer places ind in its cell if the cell is empty or ind ranks better
// than the occupant, and reports whether it did. Ineligible individuals are
// never placed.
func (a *Archive) Offer(ind *Individual) bool {
	if !ind.Eligible() {
		return false
	}
	c := a.CellOf(ind)
	if cur, ok := a.cells[c]; ok && better(ind, cur) >= 0 {
		return false
	}
	a.cells[c] = ind
	return true
}

// Len is the number of occupied cells.
func (a *Archive) Len() int { return len(a.cells) }

// Elite is one archive cell and its occupant.
type Elite struct {
	Cell Cell
	Ind  *Individual
}

// Elites are the occupied cells in descriptor order.
func (a *Archive) Elites() []Elite {
	out := make([]Elite, 0, len(a.cells))
	for c, ind := range a.cells {
		out = append(out, Elite{c, ind})
	}
	slices.SortFunc(out, func(x, y Elite) int { return compareCells(x.Cell, y.Cell) })
	return out
}

// Best is the archive's best individual by rank, or nil.
func (a *Archive) Best() *Individual {
	var best *Individual
	for _, e := range a.Elites() {
		if best == nil || better(e.Ind, best) < 0 {
			best = e.Ind
		}
	}
	return best
}
