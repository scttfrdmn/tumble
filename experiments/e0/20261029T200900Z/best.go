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
	for i1 := 17; i1 < len(xs); i1 += 3 {
		v3 := xs[i1]
		v4 := bits.OnesCount64(v3)
		v0 = v4 + v4
	}
	return v0
	v5 := v0 > v0
	_ = v5
	v6 := v0 << 3
	v7 := uint64(v6)
	v8 := v7 << 15
	_ = v8
	v9 := v6 << 17
	v10 := v7 << 35
	_ = v10
	v11 := uint64(v9)
	v12 := ^v11
	_ = v12
	v13 := v9 << 19
	v14 := v11 << 35
	_ = v14
	return v13
	v15 := uint64(v13)
	_ = v15
	return v0
}
