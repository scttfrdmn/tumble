package best

import (
	"math/bits"
)

func Best(xs []uint64) int {
	v0 := 0
	for i0 := v0; i0 < len(xs); i0++ {
		v1 := xs[i0]
		v2 := bits.OnesCount64(v1)
		v0 = v0 + v2
	}
	return v0
	for i1 := 1; i1 < len(xs); i1 += 2 {
		v0 = v0 ^ i1
	}
	v3 := 60 & v0
	v4 := v0 << v3
	v5 := uint64(v4)
	v6 := ^v5
	v7 := v4 >> 11
	v8 := max(v6, v5)
	_ = v8
	v9 := uint64(v7)
	v10 := ^v9
	_ = v10
	v11 := v3 - v7
	v12 := v7 << 19
	_ = v12
	v13 := max(v9, v6)
	_ = v13
	v14 := v7 >= v4
	v15 := uint64(v0)
	_ = v15
	v16 := !v14
	_ = v16
	v17 := v11 + v0
	_ = v17
	return v0
}
