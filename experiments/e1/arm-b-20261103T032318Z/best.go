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
	for i1 := 0; i1 < len(xs); i1 += 2 {
		v0 = 61 - i1
	}
	v3 := min(v0, 37)
	v4 := uint64(v3)
	_ = v4
	v5 := v3 - 11
	v6 := v5 * v5
	v7 := uint64(v6)
	v8 := ^v7
	v9 := bits.OnesCount64(v8)
	_ = v9
	v10 := int(v8)
	_ = v10
	v11 := uint64(v5)
	v12 := v11 + v11
	v13 := min(v8, v12)
	v14 := bits.OnesCount64(v12)
	v15 := ^v14
	_ = v15
	v16 := bits.OnesCount64(v13)
	_ = v16
	v17 := ^v5
	return v17
}
