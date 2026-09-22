package erf

import (
	"math"
	"testing"
)

// TestErf_KnownValues 钉死几个经典取值，立即暴露 erf/erfc 符号写反。
func TestErf_KnownValues(t *testing.T) {
	cases := []struct {
		x    float64
		want float64
	}{
		{0, 0},
		{0.5, 0.5204998778},
		{1, 0.8427007929},
		{2, 0.9953222650},
		{3, 0.9999779095},
		{-1, -0.8427007929},
	}
	for _, tc := range cases {
		got := Erf(tc.x)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("Erf(%v) = %.10f, want %.10f", tc.x, got, tc.want)
		}
	}
}

// TestErf_MatchesStdLib 在全区间网格上与标准库 math.Erf 比对（含序列/连分式切换点）。
func TestErf_MatchesStdLib(t *testing.T) {
	for x := -6.0; x <= 6.0; x += 0.05 {
		got := Erf(x)
		want := math.Erf(x)
		const tol = 2e-13
		if math.Abs(got-want) > tol*(1+math.Abs(want)) {
			t.Fatalf("Erf(%.2f) = %.15f, stdlib = %.15f", x, got, want)
		}
	}
}

// TestErfc_MatchesStdLib 校验余误差函数及其在大 x 下的精度。
func TestErfc_MatchesStdLib(t *testing.T) {
	for x := -6.0; x <= 10.0; x += 0.05 {
		got := Erfc(x)
		want := math.Erfc(x)
		const tol = 2e-13
		if math.Abs(got-want) > tol*(1+math.Abs(want)) {
			t.Fatalf("Erfc(%.2f) = %.15e, stdlib = %.15e", x, got, want)
		}
	}
}

// TestErf_NotErfc 守卫：erf(1) 绝不能等于 erfc(1)，且 erf+erfc 恒为 1。
func TestErf_NotErfc(t *testing.T) {
	if math.Abs(Erf(1)-Erfc(1)) < 1e-6 {
		t.Fatalf("Erf(1) 与 Erfc(1) 相等，两个函数被实现成同一个")
	}
	for _, x := range []float64{-3, -0.7, 0, 0.3, 1.5, 2, 4, 8} {
		if got := Erf(x) + Erfc(x); math.Abs(got-1) > 2e-13 {
			t.Fatalf("Erf(%v)+Erfc(%v) = %v, want 1", x, x, got)
		}
	}
}
