// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"bytes"
	"encoding/binary"
	"go/types"
	"hash/fnv"
	"math"
	"slices"
	"sync"

	gexec "github.com/scttfrdmn/giri/pkg/exec"
	"golang.org/x/tools/go/ssa"
)

// DefaultCacheEntries bounds the value cache.
const DefaultCacheEntries = 1 << 22

// ValueCache is the population-wide value cache (PROJECT.md §5.2) on giri's
// per-instruction hook: an entry maps a pure instruction's shape (operator,
// operand and result types, callee) and operand values to its result. The
// key holds no ssa.Value, so an entry made by one genome serves every genome
// and generation that executes the same operation on the same values.
//
// Lookups during a generation see only the entries committed at the last
// generation boundary plus the current Run's own entries. New entries are
// committed by EndGeneration in key order. Which instructions hit is then a
// function of the run's seed, not of how evaluations were scheduled across
// goroutines, so a run's readings reproduce byte for byte.
type ValueCache struct {
	max int

	frozen map[vcKey]vcEntry // read-only between EndGeneration calls

	mu      sync.Mutex
	pending map[vcKey]vcEntry
	full    bool
}

type vcKey [16]byte

type vcEntry struct {
	result gexec.Value
	steps  uint64
}

// NewValueCache returns an empty cache holding at most max entries
// (DefaultCacheEntries if max <= 0).
func NewValueCache(max int) *ValueCache {
	if max <= 0 {
		max = DefaultCacheEntries
	}
	return &ValueCache{max: max, frozen: map[vcKey]vcEntry{}, pending: map[vcKey]vcEntry{}}
}

// Len is the number of committed entries.
func (c *ValueCache) Len() int { return len(c.frozen) }

// Full reports whether an EndGeneration dropped entries at the bound.
func (c *ValueCache) Full() bool { return c.full }

// EndGeneration commits the entries stored since the last call. Entries are
// inserted in key order until the bound is reached, so the cache's contents
// do not depend on the order Runs finished in. It must not be called while
// any Run is in progress.
func (c *ValueCache) EndGeneration() {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]vcKey, 0, len(c.pending))
	for k := range c.pending {
		if _, ok := c.frozen[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b vcKey) int { return bytes.Compare(a[:], b[:]) })
	for _, k := range keys {
		if len(c.frozen) >= c.max {
			c.full = true
			break
		}
		c.frozen[k] = c.pending[k]
	}
	clear(c.pending)
}

// hook is the CacheHook for one Run: the committed entries, then the Run's
// own. A Run interprets on one goroutine, so local needs no lock.
func (c *ValueCache) hook() *runCache {
	return &runCache{c: c, local: map[vcKey]vcEntry{}, shapes: map[ssa.Value][]byte{}}
}

type runCache struct {
	c      *ValueCache
	local  map[vcKey]vcEntry
	shapes map[ssa.Value][]byte
}

var _ gexec.CacheHook = (*runCache)(nil)

func (r *runCache) Lookup(v ssa.Value, operands []gexec.Value) (gexec.Value, uint64, bool) {
	k, ok := r.key(v, operands)
	if !ok {
		return gexec.Value{}, 0, false
	}
	if e, ok := r.c.frozen[k]; ok {
		return e.result, e.steps, true
	}
	if e, ok := r.local[k]; ok {
		return e.result, e.steps, true
	}
	return gexec.Value{}, 0, false
}

func (r *runCache) Store(v ssa.Value, operands []gexec.Value, result gexec.Value, steps uint64) {
	if !scalarValue(result) {
		return
	}
	if k, ok := r.key(v, operands); ok {
		r.local[k] = vcEntry{result, steps}
	}
}

// flush hands the Run's entries to the cache for the next commit.
func (r *runCache) flush() {
	if len(r.local) == 0 {
		return
	}
	r.c.mu.Lock()
	defer r.c.mu.Unlock()
	for k, e := range r.local {
		r.c.pending[k] = e
	}
}

// key hashes an instruction's shape and operand values. Operands that are
// not plain scalars (a uintptr carrying a pointer, scttfrdmn/giri#255) make
// the instruction uncacheable.
func (r *runCache) key(v ssa.Value, operands []gexec.Value) (vcKey, bool) {
	shape, ok := r.shapes[v]
	if !ok {
		shape = opShape(v)
		r.shapes[v] = shape
	}
	h := fnv.New128a()
	h.Write(shape)
	var b [9]byte
	for _, o := range operands {
		if !scalarValue(o) {
			return vcKey{}, false
		}
		switch x := o.Raw.(type) {
		case int64:
			b[0] = 'i'
			binary.LittleEndian.PutUint64(b[1:], uint64(x))
			h.Write(b[:])
		case float64:
			b[0] = 'f'
			binary.LittleEndian.PutUint64(b[1:], math.Float64bits(x))
			h.Write(b[:])
		case bool:
			b[0], b[1] = 'b', 0
			if x {
				b[1] = 1
			}
			h.Write(b[:2])
		case string:
			b[0] = 's'
			binary.LittleEndian.PutUint64(b[1:], uint64(len(x)))
			h.Write(b[:])
			h.Write([]byte(x))
		case complex128:
			b[0] = 'c'
			binary.LittleEndian.PutUint64(b[1:], math.Float64bits(real(x)))
			h.Write(b[:])
			binary.LittleEndian.PutUint64(b[1:], math.Float64bits(imag(x)))
			h.Write(b[1:])
		}
	}
	var k vcKey
	h.Sum(k[:0])
	return k, true
}

func scalarValue(v gexec.Value) bool {
	if v.Provenance != nil {
		return false
	}
	switch v.Raw.(type) {
	case int64, float64, bool, string, complex128:
		return true
	}
	return false
}

// opShape is what, besides its operand values, a pure instruction's result
// depends on: the operation and the static types involved.
func opShape(v ssa.Value) []byte {
	ts := func(t types.Type) string { return types.TypeString(t, nil) }
	var s string
	switch x := v.(type) {
	case *ssa.BinOp:
		s = "binop " + x.Op.String() + " " + ts(x.X.Type()) + " " + ts(x.Y.Type()) + " " + ts(x.Type())
	case *ssa.UnOp:
		s = "unop " + x.Op.String() + " " + ts(x.X.Type())
	case *ssa.Convert:
		s = "convert " + ts(x.X.Type()) + " " + ts(x.Type())
	case *ssa.ChangeType:
		s = "changetype " + ts(x.X.Type()) + " " + ts(x.Type())
	case *ssa.Call:
		callee := "?"
		if f := x.Call.StaticCallee(); f != nil {
			callee = f.String()
		}
		s = "call " + callee + " " + ts(x.Type())
	default:
		// giri offers only the kinds above; anything else gets a shape
		// that cannot collide with them.
		s = "other " + v.String() + " " + ts(v.Type())
	}
	return append([]byte(s), 0)
}
