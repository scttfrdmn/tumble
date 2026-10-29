// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package popcount

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"fmt"
	"io/fs"
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
//go:embed popcount.go
var Source []byte

// HoldoutStride is the holdout split: after sorting the fuzz-derived and
// random-fill vectors by content hash, every HoldoutStride-th one is held out
// of selection.
const HoldoutStride = 4

// Random-fill vectors supplement the fuzz corpus, which saturates at a handful
// of near-identical inputs because the reference has no data-dependent
// branches for coverage guidance to find. They are drawn from a fixed PCG
// stream, so the vector set is the same on every machine.
const (
	RandomCount  = 50
	RandomMaxLen = 32
	randomSeed   = 0x706f70636f756e74 // "popcount"
)

// EdgeCases are the fixed vectors that are always in the selection set,
// whether or not the fuzzer found them.
func EdgeCases() [][]uint64 {
	const ones = ^uint64(0)
	return [][]uint64{
		{},
		{0x8000000000000001},
		{0, 0, 0, 0, 0, 0, 0, 0},
		{ones, ones, ones, ones, ones, ones, ones, ones},
		{0xAAAAAAAAAAAAAAAA, 0x5555555555555555, 0xAAAAAAAAAAAAAAAA, 0x5555555555555555},
	}
}

// Words decodes a fuzz input into the kernel's argument: little-endian
// 8-byte words, trailing bytes dropped. FuzzPopcount uses the same decoding.
func Words(data []byte) []uint64 {
	xs := make([]uint64, len(data)/8)
	for i := range xs {
		xs[i] = binary.LittleEndian.Uint64(data[8*i:])
	}
	return xs
}

// Vectors returns the selection and holdout test vectors: the edge cases, the
// committed fuzz corpus, and the random-fill vectors, deduplicated, in a
// deterministic order. Holdout
// vectors are never used in selection (PROJECT.md §4.4).
func Vectors() (selection, holdout [][]uint64, err error) {
	seen := map[[32]byte]bool{}
	for _, xs := range EdgeCases() {
		seen[hashWords(xs)] = true
		selection = append(selection, xs)
	}

	type keyed struct {
		h  [32]byte
		xs []uint64
	}
	var fuzzed []keyed
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
		xs := Words(data)
		h := hashWords(xs)
		if !seen[h] {
			seen[h] = true
			fuzzed = append(fuzzed, keyed{h, xs})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	r := rand.New(rand.NewPCG(randomSeed, 0))
	for range RandomCount {
		xs := make([]uint64, 1+r.IntN(RandomMaxLen))
		for i := range xs {
			xs[i] = r.Uint64()
		}
		if h := hashWords(xs); !seen[h] {
			seen[h] = true
			fuzzed = append(fuzzed, keyed{h, xs})
		}
	}
	slices.SortFunc(fuzzed, func(a, b keyed) int { return bytes.Compare(a.h[:], b.h[:]) })
	for i, k := range fuzzed {
		if i%HoldoutStride == HoldoutStride-1 {
			holdout = append(holdout, k.xs)
		} else {
			selection = append(selection, k.xs)
		}
	}
	return selection, holdout, nil
}

func hashWords(xs []uint64) [32]byte {
	b := make([]byte, 8*len(xs))
	for i, x := range xs {
		binary.LittleEndian.PutUint64(b[8*i:], x)
	}
	return sha256.Sum256(b)
}

// parseCorpusFile reads a "go test fuzz v1" file holding a single []byte.
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
