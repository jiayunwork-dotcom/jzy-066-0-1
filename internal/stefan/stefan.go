// Package stefan 是一维 Stefan 凝固问题的求根内核：
// 只负责 Stefan 数与相似常数 λ 的求解，不涉及锋面推进与热流。
//
// 凝固（半无限大液相初始处于凝固点 Tf，壁面恒定 Tw < Tf）的
// Neumann 解中，相似常数 λ 是超越方程
//
//	sqrt(pi) * lambda * exp(lambda^2) * erf(lambda) = Ste
//	Ste = c * (Tf - Tw) / Lf
//
// 的唯一正根。方程单调递增，用区间二分法求根，数值上无条件稳健。
package stefan

import (
	"errors"
	"math"

	"stefanservice/internal/erf"
)

// 默认求根控制参数。
const (
	DefaultTol      = 1e-12 // 残差 |f(λ)-Ste| 的绝对容差
	maxBisections   = 600   // 二分上限（Ste 极小时 λ 极小，需要较多步）
	bracketMaxScale = 1 << 60
)

// 求根失败时返回的哨兵错误，便于上层区分“参数非法”和“数值失败”。
var (
	ErrNegativeStefan = errors.New("stefan: Stefan 数为负，无法凝固（需要 Tf > Tw）")
	ErrNoBracket      = errors.New("stefan: 无法为超越方程找到包含根的区间")
)

// RootResult 是相似常数求根结果。
type RootResult struct {
	Lambda      float64 // 数值解出的精确根
	Residual    float64 // 方程残差：sqrt(pi)*λ*exp(λ²)*erf(λ) - Ste
	Iterations  int     // 二分迭代次数
	Ste         float64 // 对应的 Stefan 数
	Approx      float64 // 小 Stefan 数近似 λ0 = sqrt(Ste/2)，仅作对照，不参与输出结论
	ApproxError float64 // 近似代入超越方程的残差，供调用方判定近似是否可用
}

// Number 由过冷程度定义 Stefan 数：Ste = c (Tf - Tw) / Lf。
//
// 仅做计算，不做物性校验（物性校验属于工况层）；
// 当 Tf < Tw 时返回负值，上层应在调用前拦截。
func Number(c, tf, tw, lf float64) float64 {
	return c * (tf - tw) / lf
}

// Equation 是超越方程左侧：F(λ) = sqrt(pi) λ exp(λ²) erf(λ)。
func Equation(lambda float64) float64 {
	l2 := lambda * lambda
	return math.SqrtPi * lambda * math.Exp(l2) * erf.Erf(lambda)
}

// SmallStefanApprox 返回小 Stefan 数渐近近似 λ0 = sqrt(Ste/2)。
//
// 该值只能作为明确标注的对照分支存在，绝不允许冒充精确根对外输出。
func SmallStefanApprox(ste float64) float64 {
	return math.Sqrt(ste / 2)
}

// Solve 对给定的 Stefan 数求相似常数 λ。
//
//	ste > 0：正常二分求根
//	ste = 0：无相变驱动力，精确根为 0
//	ste < 0：返回 ErrNegativeStefan
func Solve(ste float64) (RootResult, error) {
	if math.IsNaN(ste) || math.IsInf(ste, 0) {
		return RootResult{}, ErrNoBracket
	}
	if ste < 0 {
		return RootResult{}, ErrNegativeStefan
	}
	approx := SmallStefanApprox(ste)
	res := RootResult{Ste: ste, Approx: approx, ApproxError: Equation(approx) - ste}

	if ste == 0 {
		res.Lambda = 0
		res.Residual = 0
		return res, nil
	}

	// 找区间 [lo, hi]：F(0)=0 < Ste，F 单调递增，把 hi 倍增到 F(hi) >= Ste。
	// 大 Ste 下初始上界可能已使 exp(λ²) 上溢为 +Inf——IEEE 语义下
	// +Inf >= Ste 成立，它就是合法上界，二分从上方折半自会进入有限区，
	// 绝不能把溢出误判为“找不到区间”。
	lo, hi := 0.0, math.Max(approx, math.SmallestNonzeroFloat64)
	scale := 1
	for {
		fHi := Equation(hi)
		if !math.IsNaN(fHi) && fHi >= ste { // +Inf 也算越过 Ste
			break
		}
		hi *= 2
		scale++
		if scale > bracketMaxScale {
			res.ApproxError = Equation(approx) - ste
			return res, ErrNoBracket
		}
	}

	var iter int
	for iter = 0; iter < maxBisections; iter++ {
		mid := (lo + hi) / 2
		fMid, ok := safeEquation(mid)
		if !ok {
			hi = mid
			continue
		}
		if fMid < ste {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo <= math.Max(1e-15*(1+hi), 0) || math.Abs(fMid-ste) <= DefaultTol {
			break
		}
	}

	lambda := (lo + hi) / 2
	res.Lambda = lambda
	res.Iterations = iter + 1
	f, ok := safeEquation(lambda)
	if ok {
		res.Residual = f - ste
	} else {
		res.Residual = math.Inf(1)
	}
	res.ApproxError = Equation(approx) - ste
	return res, nil
}

// safeEquation 包住 exp(λ²) 上溢：上溢意味着 F(λ) 必然远超 Ste，
// 视为区间上界处理，不允许让 +Inf 干扰二分。
func safeEquation(lambda float64) (float64, bool) {
	v := Equation(lambda)
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return math.Inf(1), false
	}
	return v, true
}
