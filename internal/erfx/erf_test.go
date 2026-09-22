package erfx

import (
	"math"
	"testing"
)

// TestAgainstStdlib 用标准库 math.Erf/math.Erfc 作为参考，
// 钉死自实现的幂级数与连分式两条分支在全域的精度。
func TestAgainstStdlib(t *testing.T) {
	xs := []float64{0, 0.1, 0.5, 0.99, 1.0, 1.5, 1.99, 2.0, 2.01, 2.5, 3, 4, 5.5, 6, 10}
	for _, x := range xs {
		got := Erf(x)
		want := math.Erf(x)
		if math.Abs(got-want) > 1e-14 {
			t.Errorf("Erf(%v) = %.15f, want %.15f, err %.2e", x, got, want, math.Abs(got-want))
		}
		gotC := Erfc(x)
		wantC := math.Erfc(x)
		if math.Abs(gotC-wantC) > 1e-14 {
			t.Errorf("Erfc(%v) = %.15f, want %.15f, err %.2e", x, gotC, wantC, math.Abs(gotC-wantC))
		}
	}
}

func TestOddnessAndBoundaries(t *testing.T) {
	if Erf(0) != 0 {
		t.Errorf("Erf(0) = %v, want 0", Erf(0))
	}
	for _, x := range []float64{0.7, 1.3, 2.7, 4.2} {
		if math.Abs(Erf(x)+Erf(-x)) > 1e-15 {
			t.Errorf("erf not odd at %v", x)
		}
		if math.Abs(Erf(x)+Erfc(x)-1) > 1e-15 {
			t.Errorf("erf(x)+erfc(x) != 1 at %v", x)
		}
	}
	if Erf(math.Inf(1)) != 1 || Erf(math.Inf(-1)) != -1 {
		t.Errorf("erf infinities wrong: %v %v", Erf(math.Inf(1)), Erf(math.Inf(-1)))
	}
	if Erfc(math.Inf(1)) != 0 || Erfc(math.Inf(-1)) != 2 {
		t.Errorf("erfc infinities wrong")
	}
	if !math.IsNaN(Erf(math.NaN())) || !math.IsNaN(Erfc(math.NaN())) {
		t.Errorf("NaN propagation wrong")
	}
}

// TestErfNotErfc 是专门的“符号陷阱”哨兵测试：
// Stefan 超越方程需要的是 erf(λ)，若某处误写成 erfc，
// 这里的数值立刻对不上。
func TestErfNotErfc(t *testing.T) {
	// erf(0.5) ≈ 0.5204998778，而 erfc(0.5) ≈ 0.4795001222。
	if math.Abs(Erf(0.5)-0.5204998778) > 1e-9 {
		t.Fatalf("Erf(0.5) = %.10f 与已知值不符，疑似误用了 erfc", Erf(0.5))
	}
	if math.Abs(Erf(0.5)+Erfc(0.5)-1) > 1e-12 {
		t.Fatalf("erf 与 erfc 不自洽")
	}
	// 在 Stefan 常见根区间 λ≈0.25 附近，erf 与 1-erfc 必须一致，
	// 而 erf 本身远小于 1（erfc 不会是这个值）。
	if math.Abs(Erf(0.25)-0.2763263902) > 1e-9 {
		t.Errorf("Erf(0.25) = %.10f, want 0.2763263902", Erf(0.25))
	}
}
