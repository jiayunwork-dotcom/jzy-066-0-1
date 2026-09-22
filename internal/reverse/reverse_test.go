package reverse

import (
	"errors"
	"math"
	"testing"

	"stefan-service/internal/front"
	"stefan-service/internal/stefan"
)

func iceParams() stefan.Params {
	return stefan.Params{C: 2100, Lf: 334e3, K: 2.22, Alpha: 1.15e-6, Tf: 0, Tw: -20}
}

// TestRoundTrip 反求时间再正算锋面，必须回到同一目标厚度；
// 且与闭式 t=(s/(2λ))²/α 一致。
func TestRoundTrip(t *testing.T) {
	p := iceParams()
	const target = 0.05 // 5 cm
	r, err := Time(Request{Params: p, S: target})
	if err != nil {
		t.Fatal(err)
	}
	lr, _ := stefan.Solve(p)
	wantT := (target / (2 * lr.Lambda)) * (target / (2 * lr.Lambda)) / p.Alpha
	if math.Abs(r.Time-wantT)/wantT > 1e-12 {
		t.Errorf("反求时间 %.9f 与公式 %.9f 不符", r.Time, wantT)
	}
	fwd, err := front.Compute(p, r.Time)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(fwd.Front-target)/target > 1e-9 {
		t.Errorf("往返不一致：正算 s=%.10f，目标 %.10f", fwd.Front, target)
	}
	// 冰层 5cm 大约需要 2.5 小时量级。
	if r.Time < 7000 || r.Time > 12000 {
		t.Errorf("冰层达到 5cm 耗时 %g s 偏离预期", r.Time)
	}
}

func TestInvalidThicknessAndParams(t *testing.T) {
	if _, err := Time(Request{Params: iceParams(), S: 0}); !errors.Is(err, ErrNonPositiveThickness) {
		t.Errorf("S=0 应被拦截，得到 %v", err)
	}
	if _, err := Time(Request{Params: iceParams(), S: -0.01}); !errors.Is(err, ErrNonPositiveThickness) {
		t.Errorf("S<0 应被拦截，得到 %v", err)
	}
	bad := iceParams()
	bad.Lf = -1
	var ve *stefan.ValidationError
	_, err := Time(Request{Params: bad, S: 0.01})
	if !errors.As(err, &ve) {
		t.Errorf("非法物性应透传 ValidationError，得到 %v", err)
	}
}
