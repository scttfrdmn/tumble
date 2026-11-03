package best

import (
	"math/bits"
)

func Best(xs []uint64) int {
	v0 := 0
	for i0 := 0; i0 < len(xs); i0++ {
		v1 := xs[i0]
		v2 := bits.OnesCount64(v1)
		v0 = v0 + v2
	}
	return v0
	for i1 := 5; i1 < len(xs); i1 += 2 {
		v0 = i1 - v0
	}
	v3 := uint64(v0)
	v4 := bits.OnesCount64(v3)
	_ = v4
	v5 := max(v0, 55)
	v6 := v3 * v3
	_ = v6
	v7 := uint64(v5)
	v8 := bits.OnesCount64(v7)
	return v8
	v9 := -v0
	_ = v9
	v10 := uint64(v8)
	v11 := ^v3
	v12 := v11 + v7
	_ = v12
	v13 := bits.OnesCount64(v11)
	_ = v13
	v14 := 28 ^ v8
	_ = v14
	v15 := ^v0
	v16 := v3 - v10
	_ = v16
	return v15
}
