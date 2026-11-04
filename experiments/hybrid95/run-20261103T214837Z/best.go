package best

import (
	"math/bits"
)

func Best(xs []uint64) int {
	v0 := 0
	v1 := len(xs)
	v2 := v1 &^ 3
	for i0 := 0; i0 < v2; i0 += 4 {
		v3 := xs[i0]
		v4 := i0 + 1
		v5 := xs[v4]
		v6 := i0 + 2
		v7 := xs[v6]
		v8 := i0 + 3
		v9 := xs[v8]
		v10 := bits.OnesCount64(v3)
		v11 := bits.OnesCount64(v5)
		v12 := v10 + v11
		v13 := bits.OnesCount64(v7)
		v14 := v12 + v13
		v15 := bits.OnesCount64(v9)
		v16 := v14 + v15
		v0 = v0 + v16
	}
	for i1 := v2; i1 < v1; i1++ {
		v17 := xs[i1]
		v18 := bits.OnesCount64(v17)
		v0 = v0 + v18
	}
	return v0
}
