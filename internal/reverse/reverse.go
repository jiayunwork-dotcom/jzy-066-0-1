// Package reverse 实现 Stefan 问题的反向核算：
// 给定目标凝固厚度 s，反求达到该厚度所需时间
//
//	t = (s/(2λ))² / α
package reverse

import (
	"errors"
	"math"

	"stefan-service/internal/stefan"
)

// Request 是反求输入。
type Request struct {
	Params stefan.Params
	// S 目标凝固厚度 [m]，必须为正（厚度为零对应 t=0 无核算意义）
	S float64
}

// Result 是反求输出。
type Result struct {
	Ste    float64             `json:"ste"`
	Lambda stefan.LambdaResult `json:"lambda"`
	S      float64             `json:"s"`
	Time   float64             `json:"time"`
	// FrontSpeed 到达该厚度瞬间的锋面速度 [m/s]
	FrontSpeed float64 `json:"front_speed"`
}

// ErrNonPositiveThickness 表示目标厚度非法。
var ErrNonPositiveThickness = errors.New("目标凝固厚度必须为正")

// Time 反求达到厚度 s 所需时间。
func Time(req Request) (Result, error) {
	if !(req.S > 0) {
		return Result{}, ErrNonPositiveThickness
	}
	lr, err := stefan.Solve(req.Params)
	if err != nil {
		return Result{}, err
	}
	lam := lr.Lambda
	t := (req.S / (2 * lam)) * (req.S / (2 * lam)) / req.Params.Alpha
	// ds/dt = λ√(α/t)
	speed := lam * math.Sqrt(req.Params.Alpha/t)
	return Result{
		Ste:        lr.Ste,
		Lambda:     lr,
		S:          req.S,
		Time:       t,
		FrontSpeed: speed,
	}, nil
}
