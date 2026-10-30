package best

func Best(xs []int32) int {
	v0 := -1
	v1 := int32(-6)
	for i0 := 0; i0 < len(xs); i0++ {
		v2 := xs[i0]
		v3 := v0 < 0
		v4 := v2 > v1
		v5 := v4 || v3
		if v5 {
			v1 = v2
			v0 = i0
		}
	}
	v6 := v0
	v7 := v6
	return v7
	v8 := min(v7, v7)
	for i1 := 5; i1 < len(xs); i1 += 2 {
		v9 := v1 << v0
		v10 := 38 > v9
		if v10 {
			v8 = i1
			v1 = int32(13)
		}
	}
	v11 := v1 > 34
	_ = v11
	v12 := v7 - v8
	v13 := v12 + v0
	return v12
	v14 := v13 << 48
	v15 := min(v14, v12)
	_ = v15
	v16 := v1 * v1
	_ = v16
	return v14
}
