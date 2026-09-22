package front

import (
	"math"

	"stefanservice/internal/erf"
	"stefanservice/internal/stefan"
)

// Evaluate 给定工况与时刻 t（秒），返回完整正向核算。
func Evaluate(cs Case, t float64) (Result, error) {
	if err := cs.Validate(); err != nil {
		return Result{}, err
	}
	if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 {
		return Result{}, ErrNegativeTime
	}
	ste := stefan.Number(cs.C, cs.Tf, cs.Tw, cs.Lf)
	root, err := stefan.Solve(ste)
	if err != nil {
		return Result{}, err
	}
	return evaluateRoot(cs, t, root), nil
}

// EvaluateWithRoot 与 Evaluate 相同，但复用已解出的根——
// 长时间序列里 λ 只需求解一次。
func EvaluateWithRoot(cs Case, t float64, root stefan.RootResult) Result {
	return evaluateRoot(cs, t, root)
}

// evaluateRoot 汇总单点结果：精确根驱动锋面与热流，近似只作对照。
func evaluateRoot(cs Case, t float64, root stefan.RootResult) Result {
	res := Result{
		Ste:           root.Ste,
		Root:          root,
		Front:         FrontPosition(root.Lambda, cs.Alpha, t),
		ApproxQuality: approxQuality(root),
	}
	approxS := FrontPosition(root.Approx, cs.Alpha, t)
	res.ApproxFront = &approxS

	zero := 0.0
	if t == 0 {
		res.HeatPerArea = &zero
		res.Note = "t=0 时锋面位于壁面；壁面瞬时热流按 1/sqrt(t) 发散，记为 null"
		return res
	}

	q := WallHeatFlux(cs, root.Lambda, t)
	mag := math.Abs(q)
	res.WallHeatFlux = &q
	res.HeatOutFlux = &mag
	// q"(τ)∝1/√τ，单位面积累计取热为
	//   Q/A = ∫_0^t q"(τ)dτ = 2 t q"(t)
	//       = 2 k (Tf-Tw) √t / ( √(πα) erf(λ) )
	heat := 2 * cs.K * (cs.Tf - cs.Tw) * math.Sqrt(t/cs.Alpha) /
		(math.SqrtPi * erf.Erf(root.Lambda))
	res.HeatPerArea = &heat
	return res
}

// FrontPosition 返回锋面位置 s(t) = 2λ√(αt)。
func FrontPosition(lambda, alpha, t float64) float64 {
	return 2 * lambda * math.Sqrt(alpha*t)
}

// WallHeatFlux 返回壁面瞬时热流（W/m²）：
//
//	q"(t) = k (Tf-Tw) / ( √(παt) erf(λ) )
//
// 符号约定：热流指向物料内部（凝固推进方向）为正；
// 从壁面“取走热量”的强度为 |q"|。t=0 时发散，返回 +Inf，
// 由 Evaluate/EvaluateWithRoot 转为 null 输出。
func WallHeatFlux(cs Case, lambda, t float64) float64 {
	return cs.K * (cs.Tf - cs.Tw) /
		(math.Sqrt(math.Pi*cs.Alpha*t) * erf.Erf(lambda))
}
