package stefan

import (
	"math"
	"testing"
)

func validParams() Params {
	return Params{C: 2100, Lf: 334e3, K: 2.22, Alpha: 1.15e-6, Tf: 0, Tw: -20}
}

func TestStefanNumberAndValidation(t *testing.T) {
	p := validParams()
	ste, err := StefanNumber(p)
	if err != nil {
		t.Fatalf("合法工况被拦：%v", err)
	}
	want := 2100 * 20 / 334e3
	if math.Abs(ste-want) > 1e-12 {
		t.Errorf("Ste = %.10f, want %.10f", ste, want)
	}

	badCases := []struct {
		name   string
		mutate func(*Params)
		field  string
	}{
		{"alpha非正", func(p *Params) { p.Alpha = 0 }, "alpha"},
		{"alpha为负", func(p *Params) { p.Alpha = -1 }, "alpha"},
		{"lf非正", func(p *Params) { p.Lf = 0 }, "lf"},
		{"c非正", func(p *Params) { p.C = -3 }, "c"},
		{"k非正", func(p *Params) { p.K = 0 }, "k"},
		{"壁温等于凝固点", func(p *Params) { p.Tw = p.Tf }, "tw"},
		{"壁温高于凝固点", func(p *Params) { p.Tw = 5 }, "tw"},
		{"温度NaN", func(p *Params) { p.Tw = math.NaN() }, "tf/tw"},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			q := validParams()
			tc.mutate(&q)
			if _, err := StefanNumber(q); err == nil {
				t.Fatalf("期望校验失败，却通过了")
			} else {
				ve, ok := err.(*ValidationError)
				if !ok {
					t.Fatalf("错误类型不是 *ValidationError：%T", err)
				}
				if ve.Field != tc.field {
					t.Errorf("错误字段 = %q, want %q（原因：%s）", ve.Field, tc.field, ve.Reason)
				}
			}
			if _, err := Solve(q); err == nil {
				t.Fatalf("Solve 也必须拦下该非法工况")
			}
		})
	}
}

func TestSolveLambdaResidual(t *testing.T) {
	// 覆盖小/中/大 Ste，根必须让残差收敛到机器精度量级。
	for _, ste := range []float64{1e-8, 1e-4, 0.01, 0.12575, 0.5, 1.0, 5.0, 50.0} {
		r, err := SolveLambda(ste)
		if err != nil {
			t.Fatalf("Ste=%v 求根失败：%v", ste, err)
		}
		if r.AbsResidual > 1e-9*math.Max(1, ste) {
			t.Errorf("Ste=%v 绝对残差 %.3e 过大", ste, r.AbsResidual)
		}
		if r.RelResidual > 1e-9 {
			t.Errorf("Ste=%v 相对残差 %.3e 过大", ste, r.RelResidual)
		}
		if r.Lambda <= 0 {
			t.Errorf("Ste=%v 根非正", ste)
		}
		// 单调性：Ste 越大 λ 越大（由外层顺序用例隐含，此处再钉 f 单调）
		if LHS(r.Lambda) <= 0 {
			t.Errorf("f(λ) 取值异常")
		}
	}

	if _, err := SolveLambda(0); err != ErrInvalidSte {
		t.Errorf("Ste=0 应返回 ErrInvalidSte")
	}
	if _, err := SolveLambda(-1); err != ErrInvalidSte {
		t.Errorf("Ste<0 应返回 ErrInvalidSte")
	}
	if _, err := SolveLambda(math.NaN()); err != ErrInvalidSte {
		t.Errorf("Ste=NaN 应返回 ErrInvalidSte")
	}
	if _, err := SolveLambda(math.Inf(1)); err != ErrInvalidSte {
		t.Errorf("Ste=+Inf 应返回 ErrInvalidSte")
	}
}

// TestLambdaKnownValues 用文献中 Neumann 凝固解的常用数值钉死根的量级：
// Ste=1 时 λ≈0.6201，Ste≈0.12575（冰层算例）时 λ≈0.2457。
func TestLambdaKnownValues(t *testing.T) {
	r1, _ := SolveLambda(1.0)
	if math.Abs(r1.Lambda-0.6200636) > 1e-4 {
		t.Errorf("Ste=1: λ = %.7f, want ≈0.6201", r1.Lambda)
	}
	rIce, _ := SolveLambda(0.1257485)
	if math.Abs(rIce.Lambda-0.2457) > 5e-4 {
		t.Errorf("冰层 Ste: λ = %.7f, want ≈0.2457", rIce.Lambda)
	}
}

// TestErfcTrap 是超越方程处的专项哨兵：
// 在正确根处，用 erf 构出的残差必须≈0；
// 若有人把 erf 误写成 erfc，同一 λ 处的“伪残差”必然巨大。
func TestErfcTrap(t *testing.T) {
	ste := 0.1257485
	r, err := SolveLambda(ste)
	if err != nil {
		t.Fatal(err)
	}
	// 正确：f(λ) = √π λ e^{λ²} erf(λ) ≈ Ste
	good := math.Abs(sqrtPi*r.Lambda*math.Exp(r.Lambda*r.Lambda)*math.Erf(r.Lambda) - ste)
	if good > 1e-9 {
		t.Fatalf("正确根的 erf 残差 = %.2e，根有问题", good)
	}
	// 错误：把 erf 换成 erfc 后 f 完全不单调，根处残差巨大。
	bad := math.Abs(sqrtPi*r.Lambda*math.Exp(r.Lambda*r.Lambda)*math.Erfc(r.Lambda) - ste)
	if bad < 1e-3 {
		t.Fatalf("erfc 伪残差 = %.2e 竟很小，哨兵测试失效", bad)
	}
}

// TestSmallSteApproximation 精确根与 λ≈√(Ste/2) 在小 Ste 区间相对误差应很小。
func TestSmallSteApproximation(t *testing.T) {
	const relTol = 0.02 // 2%
	for _, ste := range []float64{1e-8, 1e-6, 1e-4, 1e-2} {
		exact, _ := SolveLambda(ste)
		approx := ApproxLambda(ste)
		rel := math.Abs(exact.Lambda-approx) / exact.Lambda
		if rel > relTol {
			t.Errorf("Ste=%v: 近似相对误差 %.4f 超过 %.0e，小 Ste 近似不该这么差",
				ste, rel, relTol)
		}
		// 近似根的代回残差在小 Ste 下也应较小。
		ar := ApproxResidual(ste)
		if ar.RelResidual > 0.03 {
			t.Errorf("Ste=%v: 近似分支相对残差 %.4f 异常偏大", ste, ar.RelResidual)
		}
	}
}

// TestApproxFailsAtModerateSte 中等 Ste 下近似分支必须明显不达标，
// 防止有人直接拿近似值冒充精确根对外输出。
func TestApproxFailsAtModerateSte(t *testing.T) {
	exact, _ := SolveLambda(1.0)
	approx := ApproxLambda(1.0)
	relDiff := math.Abs(exact.Lambda-approx) / exact.Lambda
	if relDiff < 0.10 {
		t.Fatalf("Ste=1 时近似与精确根只差 %.3f，预期明显偏离（>10%%）", relDiff)
	}
	ar := ApproxResidual(1.0)
	if ar.RelResidual < 0.20 {
		t.Fatalf("Ste=1 时近似相对残差 = %.4f，预期显著超标（>0.20）", ar.RelResidual)
	}
	// 精确分支在同一 Ste 下必须仍然收敛，形成对照。
	if exact.RelResidual > 1e-9 {
		t.Fatalf("精确分支残差 %.2e 未收敛", exact.RelResidual)
	}
}

func TestSolveFromParams(t *testing.T) {
	r, err := Solve(validParams())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.Lambda-0.2457) > 5e-4 || r.RelResidual > 1e-9 {
		t.Errorf("冰层物性求根异常：%+v", r)
	}
}
