// Package front 在 stefan 求根内核之上做一维 Stefan 凝固锋面核算：
// Stefan 数、相似常数、锋面位置 s(t)=2λ√(αt)、壁面热流以及时间反求。
//
// 文件按职责拆分：
//   - case.go：工况物性与解方程之前的严格校验、结果结构；
//   - front.go：正向核算、锋面推进与壁面热流；
//   - inverse.go：由目标凝固厚度反求时间。
//
// t=0 时锋面位置为零、热流按 1/√t 发散，此时热流字段为空（null），
// 而不是给出 Inf。
package front

import (
	"errors"
	"math"

	"stefanservice/internal/stefan"
)

// 工况校验失败时的哨兵错误。
var (
	ErrNonPositiveAlpha   = errors.New("front: 热扩散率 alpha 必须为正")
	ErrNonPositiveLf      = errors.New("front: 凝固潜热 Lf 必须为正")
	ErrNonPositiveC       = errors.New("front: 比热 c 必须为正")
	ErrNonPositiveK       = errors.New("front: 导热系数 k 必须为正")
	ErrWallNotBelowFreeze = errors.New("front: 壁温 Tw 必须严格低于凝固点 Tf，否则不会凝固")
	ErrNonFinite          = errors.New("front: 参数必须是有限实数")
	ErrNegativeTime       = errors.New("front: 时间 t 不能为负")
	ErrNegativeDepth      = errors.New("front: 目标凝固厚度不能为负")
)

// Case 描述一次凝固核算的全部物性与边界温度（SI 单位）。
type Case struct {
	C     float64 `json:"c"`     // 固态比热 J/(kg·K)
	K     float64 `json:"k"`     // 固态导热系数 W/(m·K)
	Alpha float64 `json:"alpha"` // 热扩散率 m²/s
	Lf    float64 `json:"lf"`    // 凝固潜热 J/kg
	Tf    float64 `json:"tf"`    // 凝固点 K（或 ℃，温差一致即可）
	Tw    float64 `json:"tw"`    // 被冷却壁温，与 Tf 同单位且 Tw < Tf
}

// Validate 在解方程之前拦截非法工况，返回带原因的错误。
func (cs Case) Validate() error {
	if !allFinite(cs.C, cs.K, cs.Alpha, cs.Lf, cs.Tf, cs.Tw) {
		return ErrNonFinite
	}
	if cs.Alpha <= 0 {
		return ErrNonPositiveAlpha
	}
	if cs.Lf <= 0 {
		return ErrNonPositiveLf
	}
	if cs.C <= 0 {
		return ErrNonPositiveC
	}
	if cs.K <= 0 {
		return ErrNonPositiveK
	}
	if cs.Tw >= cs.Tf {
		return ErrWallNotBelowFreeze
	}
	return nil
}

func allFinite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// Result 是单点正向核算结果。
type Result struct {
	Ste           float64           `json:"ste"`                      // Stefan 数
	Root          stefan.RootResult `json:"root"`                     // 精确根、残差、迭代次数与近似对照
	Front         float64           `json:"front_m"`                  // 锋面位置 s(t)，m
	WallHeatFlux  *float64          `json:"wall_heat_flux_w_m2"`      // 壁面热流（指向物料为正），W/m²；t=0 为 null
	HeatOutFlux   *float64          `json:"heat_extraction_w_m2"`     // 壁面取热强度（正值），W/m²；t=0 为 null
	HeatPerArea   *float64          `json:"cumulative_heat_j_m2"`     // 0~t 单位面积累计取热，J/m²；t=0 为 0
	Note          string            `json:"note,omitempty"`           // t=0 等特殊情形说明
	ApproxFront   *float64          `json:"approx_front_m,omitempty"` // 使用小 Ste 近似给出的对照锋面位置
	ApproxQuality string            `json:"approx_quality,omitempty"` // 近似分支是否在其适用区间内
}

// approxQuality 判定小 Ste 近似分支在该根下是否可用，并给出明确标注。
// 精确分支永远用于正式输出；这里的结论只作对照说明。
func approxQuality(root stefan.RootResult) string {
	if root.Lambda > 0 &&
		math.Abs(root.Approx-root.Lambda)/root.Lambda < 0.01 &&
		math.Abs(root.ApproxError) < 0.05*root.Ste {
		return "small_ste_region: 近似可用（相对误差<1%），仅作对照"
	}
	return "out_of_range: 已超出小 Ste 适用区间，近似不可用于定量结论"
}
