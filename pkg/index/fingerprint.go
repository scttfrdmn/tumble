// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"go/types"

	"github.com/scttfrdmn/tumble/pkg/eval"
)

// Fingerprint is a 128-bit semantic fingerprint (PROJECT.md §5.3).
type Fingerprint [16]byte

func (f Fingerprint) String() string { return fmt.Sprintf("%x", f[:]) }

// FromTrace is the fingerprint of an evaluator trace. A truncated trace, or
// one holding a value fingerprints do not cover, has none.
func FromTrace(t eval.ValueTrace) (Fingerprint, bool) {
	if t.Truncated || t.Value == nil {
		return Fingerprint{}, false
	}
	return Of(t.Value.Type(), t.Seqs)
}

// Of is fp(v) = hash(type(v) || for each vector t: hash(sequence_t(v))). The
// sequence hash covers the sequence's length and each value's encoding, so
// sequences that differ in length or in any value hash differently. v0
// covers integers and bools; any other value (a float, a slice) yields no
// fingerprint, as float fingerprints need bucketing first (§6.3).
func Of(t types.Type, seqs [][]any) (Fingerprint, bool) {
	outer := sha256.New()
	ts := types.TypeString(t, nil)
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(ts)))
	outer.Write(n[:])
	outer.Write([]byte(ts))
	binary.LittleEndian.PutUint64(n[:], uint64(len(seqs)))
	outer.Write(n[:])
	for _, seq := range seqs {
		inner := sha256.New()
		binary.LittleEndian.PutUint64(n[:], uint64(len(seq)))
		inner.Write(n[:])
		for _, x := range seq {
			bits, ok := encode(x)
			if !ok {
				return Fingerprint{}, false
			}
			binary.LittleEndian.PutUint64(n[:], bits)
			inner.Write(n[:])
		}
		outer.Write(inner.Sum(nil))
	}
	var fp Fingerprint
	copy(fp[:], outer.Sum(nil))
	return fp, true
}

// encode is a value's 64-bit encoding: two's complement for integers, 0/1
// for bools. The type is hashed separately, so int8(-1) and uint64(255) do
// not collide in practice: they never share a type.
func encode(x any) (uint64, bool) {
	switch x := x.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case int:
		return uint64(x), true
	case int8:
		return uint64(x), true
	case int16:
		return uint64(x), true
	case int32:
		return uint64(x), true
	case int64:
		return uint64(x), true
	case uint:
		return uint64(x), true
	case uint8:
		return uint64(x), true
	case uint16:
		return uint64(x), true
	case uint32:
		return uint64(x), true
	case uint64:
		return x, true
	}
	return 0, false
}
