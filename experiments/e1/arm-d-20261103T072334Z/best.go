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
	for i1 := 28; i1 < len(xs); i1 += 2 {
		v0 = ^v0
	}
	v3 := 57 - v0
	v4 := uint64(v3)
	v5 := 61 * v3
	v6 := 61 * v5
	v7 := max(v4, 27)
	_ = v7
	v8 := 61 * v6
	v9 := uint64(v8)
	_ = v9
	v10 := 61 * v8
	v11 := uint64(v10)
	v12 := 58 * v10
	v13 := uint64(v12)
	v14 := v10 - v12
	_ = v14
	v15 := 61 * v12
	_ = v15
	v16 := bits.OnesCount64(v13)
	v17 := min(v11, v4)
	_ = v17
	return v16
}
