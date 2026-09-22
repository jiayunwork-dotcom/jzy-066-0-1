// Package front 负责锋面推进 s(t)=2λ√(αt) 与壁面热流计算，
// 以及可取消的长时间序列推进作业。它不做 λ 的数值求根
// （那在 internal/stefan），只消费已经求得的相似常数。
package front

import (
	"context"
	"errors"
	"fmt"
	"math"

	"stefan-service/internal/erfx"
	"stefan-service/internal/stefan"
)

// Result 是单一时刻的正向核算结果。
type Result struct {
	Ste float64 `json:"ste"`
	// Lambda 精确分支求根结果（含残差）
	Lambda stefan.LambdaResult `json:"lambda"`
	// LambdaApprox 明确标注的小 Ste 近似对照分支
	LambdaApprox stefan.LambdaResult `json:"lambda_approx"`
	// Time 核算时刻 [s]
	Time float64 `json:"time"`
	// Front 凝固锋面位置 s(t) [m]
	Front float64 `json:"front"`
	// FrontSpeed 锋面推进速度 ds/dt [m/s]；t=0 时为 +Inf
	FrontSpeed float64 `json:"front_speed"`
	// WallHeatFlux 从固相传向冷壁的热流密度大小 |q_w| [W/m²]；
	// 约定恒报正值（由壁面向外取热方向）；t=0 时为 +Inf。
	WallHeatFlux float64 `json:"wall_heat_flux"`
}

// ErrNegativeTime 表示时刻为负。
var ErrNegativeTime = errors.New("时间不能为负")

// Compute 在给定物性与时刻做一次完整正向核算：Ste → λ → s(t) → 热流。
func Compute(p stefan.Params, t float64) (Result, error) {
	if t < 0 {
		return Result{}, ErrNegativeTime
	}
	lr, err := stefan.Solve(p)
	if err != nil {
		return Result{}, err
	}
	return Assemble(p, t, lr), nil
}

// Assemble 用已求得的 λ 组装推进结果。纯函数，无任何共享状态，
// 多工况并行调用时各自的中间量互不渗透。
func Assemble(p stefan.Params, t float64, lr stefan.LambdaResult) Result {
	lam := lr.Lambda
	rootAlphaT := math.Sqrt(p.Alpha * t)
	s := 2 * lam * rootAlphaT

	// 由温度分布 T(x,t)=Tw+(Tf−Tw)·erf(x/(2√αt))/erf(λ) 在壁面 x=0 求导：
	//   q_w(t) = k(Tf−Tw)/(√(παt)) · e^{-λ²}/erf(λ)   (t>0)
	// t=0 为理想阶跃边界下的 +Inf。
	var q, speed float64
	if t == 0 {
		q = math.Inf(1)
		speed = math.Inf(1)
	} else {
		q = p.K * (p.Tf - p.Tw) / (math.SqrtPi * rootAlphaT) *
			math.Exp(-lam*lam) / erfx.Erf(lam)
		speed = lam * math.Sqrt(p.Alpha/t)
	}

	return Result{
		Ste:          lr.Ste,
		Lambda:       lr,
		LambdaApprox: stefan.ApproxResidual(lr.Ste),
		Time:         t,
		Front:        s,
		FrontSpeed:   speed,
		WallHeatFlux: q,
	}
}

// SeriesSpec 描述一段锋面推进时间序列。
type SeriesSpec struct {
	Params    stefan.Params `json:"params"`
	T0        float64       `json:"t0"`        // 起始时刻 [s]（含）
	T1        float64       `json:"t1"`        // 终止时刻 [s]（含）
	Intervals int           `json:"intervals"` // 分段数；采样点数 = Intervals+1
}

// Point 是时间序列上的一个采样点。
type Point struct {
	Time  float64 `json:"time"`
	Front float64 `json:"front"`
	// WallHeatFlux t=0 采样点为 +Inf（HTTP 层序列化为 null）
	WallHeatFlux float64 `json:"wall_heat_flux"`
}

// SeriesResult 是完整的时间序列结果。
type SeriesResult struct {
	Ste         float64 `json:"ste"`
	Lambda      float64 `json:"lambda"`
	AbsResidual float64 `json:"abs_residual"`
	RelResidual float64 `json:"rel_residual"`
	T0          float64 `json:"t0"`
	T1          float64 `json:"t1"`
	Intervals   int     `json:"intervals"`
	Points      []Point `json:"points"`
}

// Validate 只做时间序列描述的参数校验，不启动计算。
func (s SeriesSpec) Validate() error {
	return s.validate()
}

// 校验时间序列描述。
func (s SeriesSpec) validate() error {
	if err := s.Params.Validate(); err != nil {
		return err
	}
	if s.T0 < 0 || s.T1 < 0 {
		return ErrNegativeTime
	}
	if s.T1 < s.T0 {
		return fmt.Errorf("终止时刻 %g 早于起始时刻 %g", s.T1, s.T0)
	}
	if s.Intervals <= 0 {
		return fmt.Errorf("intervals 必须为正整数，收到 %d", s.Intervals)
	}
	return nil
}

// ComputeSeries 在 [T0,T1] 上均匀采样计算锋面推进。
//
// 每个采样点前后检查 ctx：一旦取消，立即返回 ctx.Err()，
// 绝不把计算了一半的点列包装成完整结果返回。
func ComputeSeries(ctx context.Context, spec SeriesSpec) (SeriesResult, error) {
	if err := spec.validate(); err != nil {
		return SeriesResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return SeriesResult{}, err
	}

	lr, err := stefan.Solve(spec.Params)
	if err != nil {
		return SeriesResult{}, err
	}

	n := spec.Intervals
	// 用 nil + append 而非一次性 make：被取消时不会产生巨大的半成切片，
	// 且半成品切片只存在于局部、永不返回。
	var points []Point
	dt := (spec.T1 - spec.T0) / float64(n)
	for i := 0; i <= n; i++ {
		if i&0x3ff == 0 { // 每 1024 点检查一次，避免无谓的 ctx 开销
			if err := ctx.Err(); err != nil {
				return SeriesResult{}, err
			}
		}
		t := spec.T0 + dt*float64(i)
		r := Assemble(spec.Params, t, lr)
		points = append(points, Point{
			Time:         t,
			Front:        r.Front,
			WallHeatFlux: r.WallHeatFlux,
		})
	}
	if err := ctx.Err(); err != nil {
		return SeriesResult{}, err
	}

	return SeriesResult{
		Ste:         lr.Ste,
		Lambda:      lr.Lambda,
		AbsResidual: lr.AbsResidual,
		RelResidual: lr.RelResidual,
		T0:          spec.T0,
		T1:          spec.T1,
		Intervals:   n,
		Points:      points,
	}, nil
}
