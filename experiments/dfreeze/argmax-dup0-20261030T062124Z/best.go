package best

func Best(xs []int32) int {
	v0 := -2
	_ = v0
	v1 := -1
	v2 := int32(-7)
	for i0 := 0; i0 < len(xs); i0++ {
		v3 := xs[i0]
		v4 := v1 < 0
		v5 := v3 > v2
		v6 := v5 || v4
		if v6 {
			v2 = v3
			v1 = i0
		}
	}
	v7 := v1
	v8 := v7
	v9 := v8
	return v9
	v10 := -1 + v2
	v11 := v10 * 13
	v12 := v11 * 18
	v13 := 33 + v12
	v14 := v13 * 13
	v15 := 33 + v14
	v16 := v15 * 13
	v17 := 33 + v16
	_ = v17
	return v9
}
