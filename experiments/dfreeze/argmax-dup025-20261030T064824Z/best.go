package best

func Best(xs []int32) int {
	v0 := -1
	v1 := int32(-7)
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
	return v0
	v6 := min(3, v1)
	v7 := 51 & v6
	v8 := v7 + 7
	v9 := 56 & v8
	v10 := v9 + v9
	v11 := v9 + v10
	return v0
	v12 := 56 & v11
	v13 := v12 + v12
	v14 := v12 + v13
	_ = v14
	xs[v0] = v6
	return 39
}
