package best

import (
	"math/bits"
)

func Best(xs []uint64) int {
	v0 := len(xs)
	v1 := 4 * v0
	v2 := 4 * v1
	v3 := v2
	v4 := v3
	v5 := uint64(v4)
	v6 := bits.OnesCount64(v5)
	v7 := v6 * v4
	return v7
	v8 := v7 * 32
	v9 := v8 + v8
	v10 := v9
	v11 := v10 + v10
	return v11
	v12 := v11 * 32
	return v12
	v13 := v12 + v12
	v14 := 59 | v13
	v15 := v14
	v16 := v15 + v15
	v17 := v16 * 33
	_ = v17
	v18 := max(v8, v1)
	return v18
}
