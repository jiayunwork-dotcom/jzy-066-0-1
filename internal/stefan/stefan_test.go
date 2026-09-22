package stefan

import (
	"math"
	"testing"

	"stefanservice/internal/erf"
)

// TestSolve_SatisfiesTranscendentalEquation 精确分支必须把超越方程
// 残差压到容差以内，覆盖从极小到中等的 Stefan 数。
func TestSolve_SatisfiesTranscendentalEquation(t *testing.T) {
	for _, ste := range []float64{1e-12, 1e-6, 1e-3, 0.01, 0.05, 0.189, 0.5, 1.0, 2.0, 5.0} {
		r, err := Solve(ste)
		if err != nil {
			t.Fatalf("Solve(%v) unexpected error: %v", ste, err)
		}
		if r.Lambda <= 0 || math.IsNaN(r.Lambda) {
			t.Fatalf("Solve(%v) lambda = %v, want positive", ste, r.Lambda)
		}
		if math.Abs(r.Residual) > 1e-10 {
			t.Fatalf("Ste=%v residual = %.3e, exceeds tolerance", ste, r.Residual)
		}
		// 代回方程独立复核（而不是只信返回的 Residual 字段）。
		if got := Equation(r.Lambda); math.Abs(got-ste) > 1e-10*(1+ste) {
			t.Fatalf("Ste=%v F(lambda)=%.12f, want %.12f", ste, got, ste)
		}
	}
}

// TestSolve_VeryLargeStefan 大 Ste 下初始上界即落入 exp(λ²) 上溢区，
// 仍须稳健求根：上溢的 +Inf 是合法上界，折半进入有限区即可。
// 大 Ste 处 F'(λ)≈2λF 很大，λ 末位舍入会被放大成 ~F·1e-14 的绝对残差，
// 因此以相对残差衡量收敛质量。
func TestSolve_VeryLargeStefan(t *testing.T) {
	for _, ste := range []float64{1e3, 1e4, 1e5, 1e6} {
		r, err := Solve(ste)
		if err != nil {
			t.Fatalf("Solve(%v) unexpected error: %v", ste, err)
		}
		if math.IsInf(r.Lambda, 0) || math.IsNaN(r.Lambda) {
			t.Fatalf("Solve(%v) lambda non-finite", ste)
		}
		if math.Abs(r.Residual)/ste > 1e-12 {
			t.Fatalf("Ste=%v relative residual %.3e", ste, r.Residual/ste)
		}
		// 独立代回，并要求方程在解处为有限值。
		f := Equation(r.Lambda)
		if math.IsInf(f, 0) || math.Abs(f-ste) > 1e-12*ste {
			t.Fatalf("Ste=%v F(lambda)=%.6e", ste, f)
		}
	}
}

// TestSolve_MonotonicInSte λ 随 Stefan 数单调增大。
func TestSolve_MonotonicInSte(t *testing.T) {
	prev := 0.0
	for _, ste := range []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 4, 8} {
		r, _ := Solve(ste)
		if !(r.Lambda > prev) {
			t.Fatalf("lambda not increasing at Ste=%v: %v <= %v", ste, r.Lambda, prev)
		}
		prev = r.Lambda
	}
}

// TestSolve_ZeroStefan Ste=0 时根为零。
func TestSolve_ZeroStefan(t *testing.T) {
	r, err := Solve(0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lambda != 0 || r.Residual != 0 {
		t.Fatalf("Solve(0) = %+v, want zero root", r)
	}
}

// TestSolve_NegativeStefan Ste<0（意味着 Tw>Tf，根本不凝固）必须被拒绝。
func TestSolve_NegativeStefan(t *testing.T) {
	if _, err := Solve(-1); err != ErrNegativeStefan {
		t.Fatalf("got %v, want ErrNegativeStefan", err)
	}
}

// TestSmallStefanApprox_AccurateAtSmallSte 小 Ste 区间（Ste<=0.01）
// 精确根与 λ0=sqrt(Ste/2) 的相对误差应低于 1%，且代入方程的残差
// （其量级为 O(Ste^{3/2})，即相对 Ste 约为 O(sqrt(Ste))）足够小。
func TestSmallStefanApprox_AccurateAtSmallSte(t *testing.T) {
	const relTol = 0.01
	for _, ste := range []float64{1e-6, 1e-4, 0.001, 0.005, 0.01} {
		r, _ := Solve(ste)
		rel := math.Abs(r.Approx-r.Lambda) / r.Lambda
		if rel >= relTol {
			t.Fatalf("Ste=%v approx rel error %.4f >= %.4f", ste, rel, relTol)
		}
		// 近似残差为 O(Ste^{3/2})，相对于 Ste 应足够小。
		if math.Abs(r.ApproxError) > 0.05*ste {
			t.Fatalf("Ste=%v approx residual %.3e too large relative to Ste", ste, r.ApproxError)
		}
	}
}

// TestSmallStefanApprox_FailsAtModerateSte 中等 Ste 下近似明显偏离：
// 相对误差超过 1%，且代入超越方程的残差远超容差。这条与上一条共同
// 保证“近似分支”不会被越界当成精确根使用。
func TestSmallStefanApprox_FailsAtModerateSte(t *testing.T) {
	r, err := Solve(1.0)
	if err != nil {
		t.Fatal(err)
	}
	rel := math.Abs(r.Approx-r.Lambda) / r.Lambda
	if rel < 0.05 {
		t.Fatalf("Ste=1 approx rel error only %.4f, expected clear deviation", rel)
	}
	// 残差必须超出精确分支容差几个数量级。
	if math.Abs(r.ApproxError) < 1e-3 {
		t.Fatalf("Ste=1 approx residual %.3e suspiciously small", r.ApproxError)
	}
	if math.Abs(r.Residual) > 1e-10 {
		t.Fatalf("exact root residual %.3e not converged", r.Residual)
	}
}

// erfcSwappedEquation 模拟把实现中的 erf 错写成 erfc 后的“超越方程”：
// sqrt(pi) λ exp(λ²) erfc(λ)。
func erfcSwappedEquation(lambda float64) float64 {
	return math.SqrtPi * lambda * math.Exp(lambda*lambda) * erf.Erfc(lambda)
}

// TestErfErfcSwap_IsCaught 守卫测试：Stefan 方程中若把 erf 换成 erfc，
// 函数从单调递增趋于无穷变为有上界（λ→∞ 时趋于 1，见 erfc 的渐近展开
// erfc(λ) ~ e^(-λ²)/(√π λ)(1-1/(2λ²)+...)）。
// 在 Ste=1 处不存在有限根——以此保证这种符号错误无法静默蒙混过关。
func TestErfErfcSwap_IsCaught(t *testing.T) {
	const ste = 1.0

	// 正确方程：在有限区间内必然越过 Ste（有根）。
	hi := 1.0
	sawAbove := false
	for hi <= 1<<20 {
		f := Equation(hi)
		if !math.IsNaN(f) && f >= ste {
			sawAbove = true
			break
		}
		hi *= 2
	}
	if !sawAbove {
		t.Fatal("correct erf equation unexpectedly failed to exceed Ste")
	}

	// 错误（erfc）方程：扫描到进入上溢/下溢区之前，函数值必须始终
	// 严格小于 Ste（从下方逼近渐近界 1）；任何非 NaN 的“越过”都说明
	// 守卫逻辑失效。
	for lam := 1e-3; lam <= 1<<20; lam *= 2 {
		f := erfcSwappedEquation(lam)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			break // 进入 exp(λ²) 上溢区，此前已扫过全部有限取值
		}
		if f >= ste {
			t.Fatalf("erfc-swapped equation reaches Ste at lambda=%v (f=%v): erf/erfc guard broken", lam, f)
		}
	}
	// 核对有界性：λ=5 时已接近渐近界 1（1-1/(2λ²)≈0.98），且严格小于 1。
	if g := erfcSwappedEquation(5); g >= 1 || math.Abs(g-1) > 0.05 {
		t.Fatalf("erfc-swapped value at lambda=5 = %v unexpected", g)
	}

	// 正确实现解出的 λ 满足 erf 方程、不满足 erfc 方程。
	r, _ := Solve(ste)
	if math.Abs(Equation(r.Lambda)-ste) > 1e-10 {
		t.Fatal("solved lambda does not satisfy the erf equation")
	}
	if math.Abs(erfcSwappedEquation(r.Lambda)-ste) < 0.1 {
		t.Fatal("solved lambda also satisfies erfc equation — guard logic suspect")
	}
}
