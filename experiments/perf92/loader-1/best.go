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
	for i1 := 10; i1 < len(xs); i1 += 3 {
		v3 := max(v0, 13)
		v0 = v3 &^ v0
	}
	v4 := v0 + v0
	v5 := uint64(v4)
	_ = v5
	v6 := v4 + v4
	v7 := uint64(v6)
	_ = v7
	return v6
	v8 := v6 + v6
	v9 := uint64(v8)
	v10 := 23 < v9
	_ = v10
	v11 := 17 &^ v9
	_ = v11
	return v8
	v12 := v8 + v8
	v13 := uint64(v12)
	v14 := v0 != v12
	_ = v14
	v15 := 17 &^ v13
	_ = v15
	return v12
}
