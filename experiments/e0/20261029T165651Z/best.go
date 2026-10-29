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
	for i1 := 22; i1 < len(xs); i1 += 3 {
		v0 = bits.OnesCount64(14)
		v0 = v0 * i1
	}
	v3 := v0 * 56
	v4 := uint64(v3)
	v5 := v3 * 56
	v6 := v5 * 56
	v7 := v6 | 56
	_ = v7
	v8 := bits.OnesCount64(v4)
	v9 := uint64(v8)
	_ = v9
	v10 := v8 * 56
	v11 := v10 * 56
	v12 := v11 * 56
	v13 := v12 * 56
	v14 := v13 * 56
	v15 := v14 | 56
	v16 := v15 * 56
	v17 := v16 * 56
	v18 := v17 * 56
	v19 := uint64(v18)
	v20 := v18 * 56
	v21 := v20 * 56
	v22 := v21 | 56
	_ = v22
	v23 := bits.OnesCount64(v19)
	v24 := uint64(v23)
	_ = v24
	return v23
}
