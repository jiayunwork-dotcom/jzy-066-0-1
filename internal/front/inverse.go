package front

import (
	"math"
	"time"

	"stefanservice/internal/stefan"
)

// TimeToReach 反求锋面达到目标厚度 s（m）所需时间：
//
//	t = ( s / (2λ) )² / α
//
// 物性在求根之前校验；s=0 时返回零时长。
func TimeToReach(cs Case, s float64) (time.Duration, float64, error) {
	if err := cs.Validate(); err != nil {
		return 0, 0, err
	}
	if math.IsNaN(s) || math.IsInf(s, 0) || s < 0 {
		return 0, 0, ErrNegativeDepth
	}
	ste := stefan.Number(cs.C, cs.Tf, cs.Tw, cs.Lf)
	root, err := stefan.Solve(ste)
	if err != nil {
		return 0, 0, err
	}
	if s == 0 || root.Lambda == 0 {
		return 0, 0, nil
	}
	secs := (s / (2 * root.Lambda)) * (s / (2 * root.Lambda)) / cs.Alpha
	return time.Duration(secs * float64(time.Second)), secs, nil
}
