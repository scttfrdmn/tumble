// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
)

// Hash is a canonical structural hash (PROJECT.md §5.1).
type Hash [32]byte

func (h Hash) String() string { return fmt.Sprintf("%x", h[:8]) }

// StructHash is the canonical structural hash of f's body: the statements in
// order, with locals α-renamed by first occurrence and parameters identified
// by position. Two genomes that differ only in variable IDs hash equal.
func (f *Func) StructHash() Hash {
	return HashStmts(len(f.Sig.Params), f.Body)
}

// HashStmts is the structural hash of a statement list over nparams
// parameters (Var IDs 0..nparams-1), α-renaming every other variable by first
// occurrence. pkg/index hashes fragments with it.
func HashStmts(nparams int, ss []Stmt) Hash {
	h := hasher{h: sha256.New(), nparams: nparams, ren: map[int]uint64{}}
	h.stmts(ss)
	var out Hash
	h.h.Sum(out[:0])
	return out
}

type hasher struct {
	h       hash.Hash
	nparams int
	ren     map[int]uint64
	buf     [8]byte
}

const (
	tagAssign byte = iota + 1
	tagReassign
	tagStore
	tagLoop
	tagIf
	tagReturn
	tagSink
	tagEnd
	tagElse
	tagAtom
	tagBinop
	tagUnop
	tagIndex
	tagLen
	tagCall
	tagConvert
	tagParam
	tagLocal
	tagConst
)

func (h *hasher) byte(b byte) { h.h.Write([]byte{b}) }

func (h *hasher) u64(x uint64) {
	binary.LittleEndian.PutUint64(h.buf[:], x)
	h.h.Write(h.buf[:])
}

func (h *hasher) str(s string) {
	h.u64(uint64(len(s)))
	h.h.Write([]byte(s))
}

func (h *hasher) typ(t Type) {
	b := byte(t.Kind)
	if t.Slice {
		b |= 0x80
	}
	h.byte(b)
}

func (h *hasher) v(v Var) {
	if v.ID < h.nparams {
		h.byte(tagParam)
		h.u64(uint64(v.ID))
	} else {
		n, ok := h.ren[v.ID]
		if !ok {
			n = uint64(len(h.ren))
			h.ren[v.ID] = n
		}
		h.byte(tagLocal)
		h.u64(n)
	}
	h.typ(v.T)
}

func (h *hasher) operand(o Operand) {
	switch o := o.(type) {
	case Var:
		h.v(o)
	case Const:
		h.byte(tagConst)
		h.typ(o.T)
		h.u64(o.Bits)
	}
}

func (h *hasher) stmts(ss []Stmt) {
	for _, s := range ss {
		h.stmt(s)
	}
	h.byte(tagEnd)
}

func (h *hasher) stmt(s Stmt) {
	switch s := s.(type) {
	case *Assign:
		if s.Define {
			h.byte(tagAssign)
		} else {
			h.byte(tagReassign)
		}
		h.v(s.Dst)
		h.expr(s.X)
	case *Store:
		h.byte(tagStore)
		h.v(s.X)
		h.operand(s.I)
		h.operand(s.Val)
	case *Loop:
		h.byte(tagLoop)
		h.v(s.Ind)
		h.operand(s.Lo)
		h.expr(s.Hi)
		h.u64(uint64(s.Step))
		h.stmts(s.Body)
	case *If:
		h.byte(tagIf)
		h.v(s.Cond)
		h.stmts(s.Then)
		h.byte(tagElse)
		h.stmts(s.Else)
	case *Return:
		h.byte(tagReturn)
		h.operand(s.X)
	case *Sink:
		h.byte(tagSink)
		h.v(s.X)
	default:
		panic(fmt.Sprintf("tir: unknown stmt %T", s))
	}
}

func (h *hasher) expr(e Expr) {
	switch e := e.(type) {
	case *Atom:
		h.byte(tagAtom)
	case *Binop:
		h.byte(tagBinop)
		h.str(e.Op.String())
	case *Unop:
		h.byte(tagUnop)
		h.str(e.Op.String())
	case *Index:
		h.byte(tagIndex)
	case *Len:
		h.byte(tagLen)
	case *Call:
		h.byte(tagCall)
		h.str(e.Pkg)
		h.str(e.Name)
		h.u64(uint64(len(e.Args)))
	case *Convert:
		h.byte(tagConvert)
	default:
		panic(fmt.Sprintf("tir: unknown expr %T", e))
	}
	for _, o := range ExprOperands(e) {
		h.operand(o)
	}
}
