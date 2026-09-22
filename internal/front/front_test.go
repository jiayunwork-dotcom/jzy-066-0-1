package front

import (
	"math"
	"testing"
	"time"
)

// iceLike 是一组接近冰的典型物性，供多个测试共用。
func iceLike() Case {
	return Case{
		C:     2100,    // J/(kg·K)
		K:     2.22,    // W/(m·K)
		Alpha: 1.15e-6, // m²/s
		Lf:    334e3,   // J/kg
		Tf:    273.15,
		Tw:    253.15, // -20 ℃
	}
}

func TestValidate(t *testing.T) {
	good := iceLike()
	if err := good.Validate(); err != nil {
		t.Fatalf("valid case rejected: %v", err)
	}

	bad := []Case{
		func() Case { c := good; c.Alpha = 0; return c }(),
		func() Case { c := good; c.Alpha = -1; return c }(),
		func() Case { c := good; c.Lf = 0; return c }(),
		func() Case { c := good; c.Lf = -10; return c }(),
		func() Case { c := good; c.C = -1; return c }(),
		func() Case { c := good; c.K = -1; return c }(),
		func() Case { c := good; c.Tw = c.Tf; return c }(),      // 壁温=凝固点
		func() Case { c := good; c.Tw = c.Tf + 10; return c }(), // 壁温高于凝固点
		func() Case { c := good; c.Lf = math.NaN(); return c }(),
		func() Case { c := good; c.Alpha = math.Inf(1); return c }(),
	}
	want := []error{
		ErrNonPositiveAlpha, ErrNonPositiveAlpha,
		ErrNonPositiveLf, ErrNonPositiveLf,
		ErrNonPositiveC, ErrNonPositiveK,
		ErrWallNotBelowFreeze, ErrWallNotBelowFreeze,
		ErrNonFinite, ErrNonFinite,
	}
	for i, c := range bad {
		if err := c.Validate(); err != want[i] {
			t.Fatalf("case %d: got %v, want %v", i, err, want[i])
		}
	}
}

// TestZeroTime 时刻为零，锋面恒为零；热流字段必须为 null（*float64 nil）。
func TestZeroTime(t *testing.T) {
	res, err := Evaluate(iceLike(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Front != 0 {
		t.Fatalf("front at t=0 = %v, want 0", res.Front)
	}
	if res.WallHeatFlux != nil || res.HeatOutFlux != nil {
		t.Fatalf("heat flux at t=0 must be null, got %+v", res.WallHeatFlux)
	}
	if res.HeatPerArea == nil || *res.HeatPerArea != 0 {
		t.Fatalf("cumulative heat at t=0 must be 0, got %+v", res.HeatPerArea)
	}
	// λ 由物性与温差决定、与时间无关：t=0 时仍须是收敛的正根，
	// 锋面为零来自 sqrt(t)，而不是把 λ 当成 0。
	if res.Root.Lambda <= 0 || math.Abs(res.Root.Residual) > 1e-10 {
		t.Fatalf("lambda at t=0 must still be the converged root: %+v", res.Root)
	}
}

// TestSqrtTimeScaling 时间放大四倍，锋面正好加倍。
func TestSqrtTimeScaling(t *testing.T) {
	cs := iceLike()
	r1, err := Evaluate(cs, 900) // 15 min
	if err != nil {
		t.Fatal(err)
	}
	r4, err := Evaluate(cs, 3600) // 1 h = 4×15 min
	if err != nil {
		t.Fatal(err)
	}
	ratio := r4.Front / r1.Front
	if math.Abs(ratio-2) > 1e-12 {
		t.Fatalf("s(4t)/s(t) = %.12f, want 2", ratio)
	}
	// 两次求解的 λ 必须相同（与时间无关）。
	if r1.Root.Lambda != r4.Root.Lambda {
		t.Fatalf("lambda depends on time: %v vs %v", r1.Root.Lambda, r4.Root.Lambda)
	}
	// 热流按 1/sqrt(t) 衰减：四倍时间后减半。
	if math.Abs(*r4.WallHeatFlux-*r1.WallHeatFlux/2) > 1e-9*math.Abs(*r1.WallHeatFlux) {
		t.Fatalf("heat flux does not scale as 1/sqrt(t)")
	}
}

// TestCumulativeHeat 单位面积累计取热必须等于瞬时热流积分：
// Q/A = ∫q"dτ = 2 t q"(t)。
func TestCumulativeHeat(t *testing.T) {
	for _, ts := range []float64{1, 60, 900, 3600, 86400} {
		r, err := Evaluate(iceLike(), ts)
		if err != nil {
			t.Fatal(err)
		}
		want := 2 * ts * *r.WallHeatFlux
		if math.Abs(*r.HeatPerArea-want) > 1e-9*want {
			t.Fatalf("t=%v cumulative %.6e != 2t*q %.6e", ts, *r.HeatPerArea, want)
		}
		if *r.HeatPerArea <= 0 {
			t.Fatalf("t=%v cumulative heat must be positive", ts)
		}
	}
}

// TestDeeperWithSubcooling 只增大壁面与凝固点的温差，同一时刻锋面更深。
func TestDeeperWithSubcooling(t *testing.T) {
	base := iceLike()

	less := base
	less.Tw = base.Tf - 5 // 5 K 过冷
	more := base
	more.Tw = base.Tf - 40 // 40 K 过冷

	a, err := Evaluate(less, 3600)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Evaluate(more, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if !(b.Front > a.Front) {
		t.Fatalf("larger subcooling front %v not deeper than %v", b.Front, a.Front)
	}
	if !(b.Ste > a.Ste) || !(b.Root.Lambda > a.Root.Lambda) {
		t.Fatalf("Ste/lambda ordering wrong: %+v vs %+v", a, b)
	}
}

// TestStagnationForLargeLatentHeat 潜热趋于很大（Ste→0），锋面趋于停滞。
func TestStagnationForLargeLatentHeat(t *testing.T) {
	cs := iceLike()
	rRef, _ := Evaluate(cs, 3600)

	huge := cs
	huge.Lf = 334e9 // 潜热放大 1e6 倍 → Ste 缩小 1e6 倍
	rHuge, err := Evaluate(huge, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if !(rHuge.Ste < rRef.Ste/1e5) {
		t.Fatalf("Ste did not shrink: %v vs %v", rHuge.Ste, rRef.Ste)
	}
	if !(rHuge.Front < rRef.Front/500) {
		t.Fatalf("front did not stagnate: %v vs %v", rHuge.Front, rRef.Front)
	}

	// 精确核对 √(1/Lf) 标度：取两个都处于小 Ste 区间的工况，
	// 潜热差 100 倍则精确根给出的锋面厚度差应接近 10 倍。
	a := cs
	a.Lf = cs.Lf * 1e5 // Ste 约 1.3e-6
	b := cs
	b.Lf = cs.Lf * 1e7 // Ste 约 1.3e-8
	ra, _ := Evaluate(a, 3600)
	rb, _ := Evaluate(b, 3600)
	if math.Abs(ra.Front/rb.Front-10) > 0.02 {
		t.Fatalf("small-Ste front ratio %.4f, want 10", ra.Front/rb.Front)
	}
}

// TestExactRootUsed 对外结果必须走精确分支：残差收敛、且与近似值在中高 Ste 下不同。
func TestExactRootUsed(t *testing.T) {
	// 取一个中等 Ste 的工况：提高比热/过冷度。
	cs := iceLike()
	cs.C = 4000
	cs.Tw = cs.Tf - 80
	res, err := Evaluate(cs, 1800)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Root.Residual) > 1e-10 {
		t.Fatalf("exact root residual %.3e not converged", res.Root.Residual)
	}
	rel := math.Abs(res.Root.Approx-res.Root.Lambda) / res.Root.Lambda
	if rel < 0.05 {
		t.Fatalf("moderate Ste: lambda suspiciously close to approximation (rel=%.4f)", rel)
	}
	if res.ApproxQuality == "" || res.ApproxFront == nil {
		t.Fatal("approximation branch must be reported separately and labelled")
	}
	// 锋面必须用精确 λ 计算，不允许用近似值。
	wantFront := 2 * res.Root.Lambda * math.Sqrt(cs.Alpha*1800)
	if math.Abs(res.Front-wantFront) > 1e-15*(1+wantFront) {
		t.Fatalf("front computed from non-exact lambda")
	}
}

// TestTimeToReach_RoundTrip 反求时间再正算，厚度必须回到目标值。
func TestTimeToReach_RoundTrip(t *testing.T) {
	cs := iceLike()
	target := 0.05 // 5 cm
	d, secs, err := TimeToReach(cs, target)
	if err != nil {
		t.Fatal(err)
	}
	if secs <= 0 {
		t.Fatalf("time = %v, want positive", secs)
	}
	r, err := Evaluate(cs, secs)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.Front-target) > 1e-12 {
		t.Fatalf("round-trip front = %.9f, want %v", r.Front, target)
	}
	// Duration 与秒数一致。
	if math.Abs(d.Seconds()-secs) > 1e-9 {
		t.Fatalf("duration %v != %v s", d, secs)
	}
}

func TestTimeToReach_Invalid(t *testing.T) {
	cs := iceLike()
	if _, _, err := TimeToReach(cs, -1); err != ErrNegativeDepth {
		t.Fatalf("got %v, want ErrNegativeDepth", err)
	}
	bad := cs
	bad.Tw = bad.Tf + 5
	if _, _, err := TimeToReach(bad, 0.01); err != ErrWallNotBelowFreeze {
		t.Fatalf("got %v, want ErrWallNotBelowFreeze", err)
	}
	if d, _, err := TimeToReach(cs, 0); err != nil || d != time.Duration(0) {
		t.Fatalf("s=0 must give t=0, got %v %v", d, err)
	}
}

// TestIcePreset_CentimeterPerHour 预置冰层算例：壁温远低于零度时，
// 一小时凝固厚度落在厘米量级（1~10 cm）。
func TestIcePreset_CentimeterPerHour(t *testing.T) {
	cs := iceLike()
	res, err := Evaluate(cs, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if res.Front < 0.01 || res.Front > 0.10 {
		t.Fatalf("one-hour ice front = %.4f m, not in centimeter range", res.Front)
	}
	if res.Ste <= 0 || res.Ste > 1 {
		t.Fatalf("ice Ste = %v unexpected", res.Ste)
	}
	// 热流为正（指向物料），取热强度等于其绝对值。
	if *res.WallHeatFlux <= 0 || math.Abs(*res.WallHeatFlux-*res.HeatOutFlux) > 1e-9 {
		t.Fatalf("heat flux sign/magnitude wrong: %+v %+v", res.WallHeatFlux, res.HeatOutFlux)
	}
}

// TestEvaluateNegativeTime 负时间必须被拦截。
func TestEvaluateNegativeTime(t *testing.T) {
	if _, err := Evaluate(iceLike(), -1); err != ErrNegativeTime {
		t.Fatalf("got %v, want ErrNegativeTime", err)
	}
}
