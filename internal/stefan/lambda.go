package stefan

import (
	"errors"
	"math"

	"stefan-service/internal/erfx"
)

// sqrtPi 是 √π。
var sqrtPi = math.SqrtPi

// LHS 返回超越方程左侧 f(λ) = √π·λ·exp(λ²)·erf(λ)。
//
// 这是整个核算最容易写错符号的地方：必须是 erf 而不是 erfc。
// 本函数只依赖 erfx.Erf，并有专门的测试钉住；
// 若把 erf 换成 erfc，求根会偏出数倍并被测试立刻抓出。
func LHS(lambda float64) float64 {
	if lambda < 0 {
		// f 关于 λ 为奇函数；外层求根只走正半轴，这里仅为完备性。
		return -LHS(-lambda)
	}
	// λ 很大时 exp(λ²)·λ 会溢出，而 erf(λ)→1。先封顶 exp 的指数，
	// 求根区间（Ste 为工程常见量级）根本到不了这里，封顶只用于稳健性。
	z := lambda * lambda
	e := math.Exp(math.Min(z, 700))
	return sqrtPi * lambda * e * erfx.Erf(lambda)
}

// LambdaResult 是超越方程求根结果及收敛质量信息。
type LambdaResult struct {
	// Lambda 数值求得的相似常数 λ（精确分支）
	Lambda float64 `json:"lambda"`
	// Ste 对应的 Stefan 数
	Ste float64 `json:"ste"`
	// F 方程左侧 √πλe^{λ²}erf(λ) 在根处的取值
	F float64 `json:"f"`
	// AbsResidual 绝对残差 |f(λ) − Ste|
	AbsResidual float64 `json:"abs_residual"`
	// RelResidual 相对残差 |f(λ) − Ste| / Ste
	RelResidual float64 `json:"rel_residual"`
	// Iterations 二分迭代次数
	Iterations int `json:"iterations"`
}

const (
	// xTol 是根的绝对收敛容差（f 对 λ 的导数量级约为 √π，
	// λ 侧 1e-13 对应残差约 1e-13）。
	xTol    = 1e-13
	maxIter = 200
)

// ErrInvalidSte 表示 Stefan 数本身非法（必须为正的有限值）。
var ErrInvalidSte = errors.New("Stefan 数必须为正的有限值")

// SolveLambda 对 √π·λ·exp(λ²)·erf(λ) = Ste 数值求根。
//
// f 在 λ≥0 上严格单调（f(0)=0，f→∞），采用先倍增找括号、
// 再二分收敛的方法，对任意 Ste>0 稳健，不依赖初值猜测。
// 残差随结果一并回报，调用方可直接看到收敛质量。
func SolveLambda(ste float64) (LambdaResult, error) {
	if !(ste > 0) || math.IsInf(ste, 1) {
		return LambdaResult{}, ErrInvalidSte
	}

	// 倍增定位上界，保证 f(hi) >= Ste。
	lo, hi := 0.0, math.Max(1e-8, math.Min(ste, 1.0))
	for LHS(hi) < ste && hi < 1e100 {
		lo = hi
		hi *= 2
	}

	var mid float64
	n := 0
	for n = 0; n < maxIter; n++ {
		mid = (lo + hi) / 2
		if hi-lo <= xTol*math.Max(1, hi) {
			break
		}
		if LHS(mid) < ste {
			lo = mid
		} else {
			hi = mid
		}
	}
	mid = (lo + hi) / 2

	f := LHS(mid)
	abs := math.Abs(f - ste)
	return LambdaResult{
		Lambda:      mid,
		Ste:         ste,
		F:           f,
		AbsResidual: abs,
		RelResidual: abs / ste,
		Iterations:  n + 1,
	}, nil
}

// ApproxLambda 返回小 Stefan 数近似 λ ≈ √(Ste/2)。
//
// 这只是“专门标注的对照分支”，绝不能冒充精确根对外输出：
// 中等 Ste 下它的残差会显著超标，由测试同时钉死其
// “小 Ste 可用、中等 Ste 失效”两侧。
func ApproxLambda(ste float64) float64 {
	if ste <= 0 {
		return 0
	}
	return math.Sqrt(ste / 2)
}

// ApproxResidual 返回近似根代回方程后的残差信息，供对照分支展示。
func ApproxResidual(ste float64) LambdaResult {
	lam := ApproxLambda(ste)
	f := LHS(lam)
	abs := math.Abs(f - ste)
	rel := 0.0
	if ste > 0 {
		rel = abs / ste
	}
	return LambdaResult{
		Lambda:      lam,
		Ste:         ste,
		F:           f,
		AbsResidual: abs,
		RelResidual: rel,
	}
}

// Solve 是便捷入口：从物性参数出发，校验、算 Ste、解 λ 一次完成。
func Solve(p Params) (LambdaResult, error) {
	ste, err := StefanNumber(p)
	if err != nil {
		return LambdaResult{}, err
	}
	return SolveLambda(ste)
}
