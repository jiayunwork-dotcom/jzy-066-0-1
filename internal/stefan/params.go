// Package stefan 是一维 Stefan 凝固锋面核算的内核：
// 材料物性/工况定义、参数校验、Stefan 数、相似常数 λ 的数值求根。
//
// 这里只放“方程本身”，锋面推进与壁面热流在 internal/front，
// 时间反求在 internal/reverse，互不耦合，也不持有任何可变全局状态。
package stefan

import "fmt"

// Params 是一次凝固核算的全部材料物性与边界温度。
//
// 温度单位由调用方自洽即可（摄氏温差与开尔文温差等价）；
// c 与 Lf 的能量单位、k 与 α 的单位同样只需内部自洽。
type Params struct {
	// C 固相材料比热 c [J/(kg·K)]
	C float64 `json:"c"`
	// Lf 凝固潜热 L_f [J/kg]，必须为正
	Lf float64 `json:"lf"`
	// K 固相导热系数 k [W/(m·K)]，用于壁面热流
	K float64 `json:"k"`
	// Alpha 热扩散率 α = k/(ρc) [m²/s]，必须为正
	Alpha float64 `json:"alpha"`
	// Tf 凝固点（相变温度）[K 或 ℃]
	Tf float64 `json:"tf"`
	// Tw 被持续冷却的壁温，必须严格低于 Tf
	Tw float64 `json:"tw"`
}

// ValidationError 携带不合法入参的字段名与中文原因，便于 HTTP 层原样回传。
type ValidationError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("参数 %s 非法：%s", e.Field, e.Reason)
}

func bad(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

// Validate 在进入任何方程求解之前拦下非法工况。
func (p Params) Validate() error {
	switch {
	case !isPositive(p.C):
		return bad("c", "比热必须为正的有限值")
	case !isPositive(p.Lf):
		return bad("lf", "凝固潜热必须为正的有限值，否则无潜热可释放")
	case !isPositive(p.K):
		return bad("k", "导热系数必须为正的有限值")
	case !isPositive(p.Alpha):
		return bad("alpha", "热扩散率必须为正的有限值")
	case !isFinite(p.Tf) || !isFinite(p.Tw):
		return bad("tf/tw", "温度必须为有限值")
	case p.Tw >= p.Tf:
		return bad("tw", "壁温不低于凝固点时不会发生凝固，必须满足 Tw < Tf")
	}
	return nil
}

func isPositive(v float64) bool {
	return isFinite(v) && v > 0
}

func isFinite(v float64) bool {
	return v == v && v <= 1e308 && v >= -1e308
}

// StefanNumber 按 Ste = c·(Tf − Tw)/Lf 计算过冷 Stefan 数。
// 调用前先做完整参数校验，因此返回值恒为正。
func StefanNumber(p Params) (float64, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	return p.C * (p.Tf - p.Tw) / p.Lf, nil
}
