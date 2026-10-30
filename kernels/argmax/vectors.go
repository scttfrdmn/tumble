// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package argmax

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
)

//go:embed testdata/corpus
var corpus embed.FS

// Source is the reference implementation's source file, from which the
// seed genome is parsed.
//
//go:embed argmax.go
var Source []byte

// HoldoutStride is the holdout split, as for popcount: after sorting the
// fuzz-derived and random-fill vectors by content hash, every
// HoldoutStride-th one is held out of selection.
const HoldoutStride = 4

// Random-fill vectors supplement the fuzz corpus. Half draw from the full
// int32 range; half from [-3, 3], so ties for the maximum are common and the
// "first" in "first largest" is tested. They come from a fixed PCG stream.
const (
	RandomCount  = 50
	RandomMaxLen = 32
	randomSeed   = 0x6172676d6178 // "argmax"
)

// EdgeCases are the fixed vectors that are always in the selection set.
func EdgeCases() [][]int32 {
	return [][]int32{
		{},
		{7},
		{math.MinInt32},
		{4, 4, 4, 4},
		{1, 2, 3, 9},
		{9, 3, 2, 1},
		{-5, -2, -9, -2},
		{math.MinInt32, math.MaxInt32, math.MaxInt32, 0},
		{0, -1, 0, -1, 0},
	}
}

// Ints decodes a fuzz input into the kernel's argument: little-endian
// 4-byte words, trailing bytes dropped. FuzzArgmax uses the same decoding.
func Ints(data []byte) []int32 {
	xs := make([]int32, len(data)/4)
	for i := range xs {
		xs[i] = int32(binary.LittleEndian.Uint32(data[4*i:]))
	}
	return xs
}

// Vectors returns the selection and holdout test vectors: the edge cases, the
// committed fuzz corpus, and the random-fill vectors, deduplicated, in a
// deterministic order. Holdout vectors are never used in selection
// (PROJECT.md §4.4).
func Vectors() (selection, holdout [][]int32, err error) {
	seen := map[[32]byte]bool{}
	for _, xs := range EdgeCases() {
		seen[hashInts(xs)] = true
		selection = append(selection, xs)
	}
	type keyed struct {
		h  [32]byte
		xs []int32
	}
	var more []keyed
	add := func(xs []int32) {
		if h := hashInts(xs); !seen[h] {
			seen[h] = true
			more = append(more, keyed{h, xs})
		}
	}
	err = fs.WalkDir(corpus, "testdata/corpus", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := corpus.ReadFile(path)
		if err != nil {
			return err
		}
		data, err := parseCorpusFile(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		add(Ints(data))
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	r := rand.New(rand.NewPCG(randomSeed, 0))
	for k := range RandomCount {
		xs := make([]int32, 1+r.IntN(RandomMaxLen))
		for i := range xs {
			if k%2 == 0 {
				xs[i] = int32(r.Uint32())
			} else {
				xs[i] = int32(r.IntN(7)) - 3
			}
		}
		add(xs)
	}
	slices.SortFunc(more, func(a, b keyed) int { return bytes.Compare(a.h[:], b.h[:]) })
	for i, k := range more {
		if i%HoldoutStride == HoldoutStride-1 {
			holdout = append(holdout, k.xs)
		} else {
			selection = append(selection, k.xs)
		}
	}
	return selection, holdout, nil
}

func hashInts(xs []int32) [32]byte {
	b := make([]byte, 4*len(xs))
	for i, x := range xs {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(x))
	}
	return sha256.Sum256(b)
}

// parseCorpusFile reads a "go test fuzz v1" file holding a single []byte.
// Each kernel package carries its own copy: the native stage copies a
// kernel's directory as a standalone module, so a kernel may import only
// the standard library.
func parseCorpusFile(raw []byte) ([]byte, error) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	if !sc.Scan() || sc.Text() != "go test fuzz v1" {
		return nil, fmt.Errorf("not a go test fuzz v1 file")
	}
	if !sc.Scan() {
		return nil, fmt.Errorf("missing value line")
	}
	line := strings.TrimSpace(sc.Text())
	lit, ok := strings.CutPrefix(line, "[]byte(")
	if !ok || !strings.HasSuffix(lit, ")") {
		return nil, fmt.Errorf("value is not []byte: %q", line)
	}
	s, err := strconv.Unquote(strings.TrimSuffix(lit, ")"))
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}
