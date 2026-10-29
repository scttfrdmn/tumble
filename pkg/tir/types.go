// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"fmt"
	"go/types"
	"strings"
)

// Type is a TIR type: a basic integer or boolean type, or a slice of one.
// Which types a run admits is a grammar parameter; tir accepts any of these.
type Type struct {
	Kind  types.BasicKind // the element kind, for a slice
	Slice bool
}

// Common types.
var (
	Int     = Type{Kind: types.Int}
	Bool    = Type{Kind: types.Bool}
	Uint64  = Type{Kind: types.Uint64}
	Int32   = Type{Kind: types.Int32}
	Uint64s = Type{Kind: types.Uint64, Slice: true}
	Int32s  = Type{Kind: types.Int32, Slice: true}
)

// basicKinds are the kinds tir can represent.
var basicKinds = map[types.BasicKind]bool{
	types.Bool: true,
	types.Int:  true, types.Int8: true, types.Int16: true, types.Int32: true, types.Int64: true,
	types.Uint: true, types.Uint8: true, types.Uint16: true, types.Uint32: true, types.Uint64: true,
}

// Valid reports whether t is representable in tir.
func (t Type) Valid() bool { return basicKinds[t.Kind] }

func (t Type) String() string {
	if !t.Valid() {
		return fmt.Sprintf("invalid(%d)", t.Kind)
	}
	s := types.Typ[t.Kind].Name()
	if t.Slice {
		return "[]" + s
	}
	return s
}

// Elem is the element type of a slice type.
func (t Type) Elem() Type { return Type{Kind: t.Kind} }

// SliceOf is the slice type with element t.
func (t Type) SliceOf() Type { return Type{Kind: t.Kind, Slice: true} }

// IsBool reports whether t is bool.
func (t Type) IsBool() bool { return !t.Slice && t.Kind == types.Bool }

// IsInteger reports whether t is a (non-slice) integer type.
func (t Type) IsInteger() bool { return !t.Slice && t.Valid() && t.Kind != types.Bool }

// IsUnsigned reports whether t is an unsigned integer type.
func (t Type) IsUnsigned() bool {
	return t.IsInteger() && types.Typ[t.Kind].Info()&types.IsUnsigned != 0
}

// Width is the bit width of an integer type; int and uint are 64 bits, as on
// every platform tumble targets.
func (t Type) Width() int {
	switch t.Kind {
	case types.Int8, types.Uint8:
		return 8
	case types.Int16, types.Uint16:
		return 16
	case types.Int32, types.Uint32:
		return 32
	case types.Bool:
		return 1
	}
	return 64
}

// GoType is the go/types type for t.
func (t Type) GoType() types.Type {
	if t.Slice {
		return types.NewSlice(types.Typ[t.Kind])
	}
	return types.Typ[t.Kind]
}

// FromGoType converts a go/types type to a Type, if tir can represent it.
func FromGoType(gt types.Type) (Type, bool) {
	slice := false
	if s, ok := gt.Underlying().(*types.Slice); ok {
		slice = true
		gt = s.Elem()
	}
	b, ok := gt.(*types.Basic)
	if !ok {
		return Type{}, false
	}
	t := Type{Kind: b.Kind(), Slice: slice}
	return t, t.Valid()
}

// ParseType parses a type written as Go source: "int", "[]uint64".
func ParseType(s string) (Type, error) {
	name, slice := strings.CutPrefix(s, "[]")
	for k := range basicKinds {
		if types.Typ[k].Name() == name {
			return Type{Kind: k, Slice: slice}, nil
		}
	}
	return Type{}, fmt.Errorf("tir: unsupported type %q", s)
}

// MarshalText writes t as Go source, so types appear as "[]uint64" in JSON.
func (t Type) MarshalText() ([]byte, error) {
	if !t.Valid() {
		return nil, fmt.Errorf("tir: invalid type %d", t.Kind)
	}
	return []byte(t.String()), nil
}

// UnmarshalText parses Go source written by MarshalText.
func (t *Type) UnmarshalText(b []byte) error {
	p, err := ParseType(string(b))
	if err != nil {
		return err
	}
	*t = p
	return nil
}
