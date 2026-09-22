package front

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"stefan-service/internal/stefan"
)

func iceParams() stefan.Params {
	return stefan.Params{C: 2100, Lf: 334e3, K: 2.22, Alpha: 1.15e-6, Tf: 0, Tw: -20}
}

// TestZeroTimeFront 时刻为零，锋面位置恒为零（热流按理论为 +∞，HTTP 层转 null）。
func TestZeroTimeFront(t *testing.T) {
	r, err := Compute(iceParams(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Front != 0 {
		t.Errorf("t=0 锋面位置 = %v，要求恒为 0", r.Front)
	}
	if !math.IsInf(r.WallHeatFlux, 1) || !math.IsInf(r.FrontSpeed, 1) {
		t.Errorf("t=0 理想阶跃边界下热流/速度应为 +Inf")
	}
}

// TestQuarterTimeRootRelation 时间放大 4 倍，锋面位置正好加倍（√t 律）。
func TestQuarterTimeRootRelation(t *testing.T) {
	p := iceParams()
	r1, err := Compute(p, 900) // 15 min
	if err != nil {
		t.Fatal(err)
	}
	r4, err := Compute(p, 3600) // 60 min = 4×
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r4.Front-2*r1.Front)/(2*r1.Front) > 1e-12 {
		t.Errorf("s(4t)=%.9f 与 2s(t)=%.9f 不符", r4.Front, 2*r1.Front)
	}
	// 同一工况两次核算 λ 必须一致。
	if r1.Lambda.Lambda != r4.Lambda.Lambda {
		t.Errorf("同工况 λ 不一致：%.15f vs %.15f", r1.Lambda.Lambda, r4.Lambda.Lambda)
	}
}

// TestDeeperWithLargerSubcooling 只加大壁面与凝固点温差，同一时刻锋面更深。
func TestDeeperWithLargerSubcooling(t *testing.T) {
	base := iceParams()
	colder := iceParams()
	colder.Tw = -40
	tm := 3600.0
	ra, err := Compute(base, tm)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := Compute(colder, tm)
	if err != nil {
		t.Fatal(err)
	}
	if !(rb.Front > ra.Front) {
		t.Errorf("更大温差反而推进更浅：%.6f <= %.6f", rb.Front, ra.Front)
	}
	if !(rb.WallHeatFlux > ra.WallHeatFlux) {
		t.Errorf("更大温差壁面热流应更大")
	}
}

// TestStagnationAsLatentHeatDiverges 潜热趋于很大（Ste→0），锋面趋于停滞。
func TestStagnationAsLatentHeatDiverges(t *testing.T) {
	reference := Compute
	_ = reference
	tm := 3600.0
	prev := math.Inf(1)
	for _, lf := range []float64{334e3, 3.34e6, 3.34e9, 3.34e12} {
		p := iceParams()
		p.Lf = lf
		r, err := Compute(p, tm)
		if err != nil {
			t.Fatal(err)
		}
		if r.Ste <= 0 || r.Front <= 0 {
			t.Fatalf("物理量应为正：Ste=%v s=%v", r.Ste, r.Front)
		}
		if r.Front >= prev {
			t.Errorf("潜热加大锋面应收窄：Lf=%g 时 s=%.3e >= 上一档 %.3e", lf, r.Front, prev)
		}
		prev = r.Front
	}
	// 极限档：Ste≈6.3e-9，一小时推进应小于 1 微米。
	p := iceParams()
	p.Lf = 3.34e15
	r, _ := Compute(p, tm)
	if r.Front > 1e-6 {
		t.Errorf("Ste 趋于零时锋面未停滞：s=%.3e", r.Front)
	}
}

// TestHeatFluxStefanBalance 壁面热流必须与界面 Stefan 能量守恒自洽：
// ρLf·ds/dt = k·(界面处温度梯度)，且界面热流等于壁面热流
// （固相内无内热源、1/√t 自相似，梯度的热流处处相等）。
func TestHeatFluxStefanBalance(t *testing.T) {
	p := iceParams()
	r, err := Compute(p, 1800)
	if err != nil {
		t.Fatal(err)
	}
	rho := p.K / (p.Alpha * p.C) // 由 α=k/(ρc) 反演密度
	interfaceRelease := rho * p.Lf * r.FrontSpeed
	if math.Abs(interfaceRelease-r.WallHeatFlux)/r.WallHeatFlux > 1e-9 {
		t.Errorf("能量守恒不闭合：壁面热流 %.6f vs ρLf·ds/dt %.6f",
			r.WallHeatFlux, interfaceRelease)
	}
	if !(r.WallHeatFlux > 0 && r.FrontSpeed > 0) {
		t.Errorf("热流与速度应为正")
	}
}

// TestIceOneHourCentimeters 预置冰层算例：1 小时凝固厚度为厘米量级（约 3.16 cm）。
func TestIceOneHourCentimeters(t *testing.T) {
	r, err := Compute(iceParams(), 3600)
	if err != nil {
		t.Fatal(err)
	}
	if r.Front < 0.02 || r.Front > 0.05 {
		t.Errorf("冰层 1 小时厚度 = %.4f m，不在预期厘米量级区间", r.Front)
	}
	if math.Abs(r.Front-0.0316) > 0.002 {
		t.Errorf("冰层 1 小时厚度 = %.5f m，预期约 0.0316 m", r.Front)
	}
}

// TestNegativeTime 负时刻必须被拒。
func TestNegativeTime(t *testing.T) {
	if _, err := Compute(iceParams(), -1); !errors.Is(err, ErrNegativeTime) {
		t.Fatalf("负时刻未被拦截：%v", err)
	}
}

// TestSeriesCancellation 取消后绝不能返回半成品点列。
func TestSeriesCancellation(t *testing.T) {
	spec := SeriesSpec{Params: iceParams(), T0: 0, T1: 86400, Intervals: 200_000_000}

	// 1) 启动前即取消：直接报错，零值结果。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ComputeSeries(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("预取消应返回 context.Canceled，得到 %v", err)
	}

	// 2) 运行中取消：同样必须报错且不携带点列。
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan struct{})
	var res SeriesResult
	var err2 error
	go func() {
		res, err2 = ComputeSeries(ctx2, spec)
		close(done)
	}()
	cancel2()
	<-done
	if !errors.Is(err2, context.Canceled) {
		t.Fatalf("运行中取消应返回 context.Canceled，得到 %v", err2)
	}
	if res.Points != nil || res.Lambda != 0 {
		t.Fatalf("取消后泄露了半成品结果：%+v", res)
	}
}

// TestSeriesHappyPath 正常序列满足 √t 律且点数完整。
func TestSeriesHappyPath(t *testing.T) {
	spec := SeriesSpec{Params: iceParams(), T0: 0, T1: 3600, Intervals: 4}
	res, err := ComputeSeries(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Points) != 5 {
		t.Fatalf("采样点数 = %d，要求 5", len(res.Points))
	}
	if res.Points[0].Front != 0 {
		t.Errorf("首点 t=0 锋面应为 0")
	}
	if math.Abs(res.Points[4].Front-2*res.Points[1].Front)/res.Points[4].Front > 1e-12 {
		t.Errorf("序列内 √t 关系不成立")
	}
}

// TestSeriesValidation 非法参数在作业计算前被拦下。
func TestSeriesValidation(t *testing.T) {
	bad := iceParams()
	bad.Alpha = 0
	_, err := ComputeSeries(context.Background(), SeriesSpec{
		Params: bad, T0: 0, T1: 10, Intervals: 2,
	})
	var ve *stefan.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("期望 *stefan.ValidationError，得到 %v", err)
	}
}

// TestParallelIsolation 多组工况并行推算，各自 λ 与锋面互不渗透。
func TestParallelIsolation(t *testing.T) {
	mk := func(tw float64) stefan.Params {
		p := iceParams()
		p.Tw = tw
		return p
	}
	cases := []struct {
		p    stefan.Params
		t    float64
		sMin float64
		sMax float64
	}{
		{mk(-5), 3600, 0.014, 0.017},
		{mk(-20), 3600, 0.030, 0.034},
		{mk(-60), 1800, 0.035, 0.045},
	}
	// 先算单线程基准。
	want := make([]Result, len(cases))
	for i, c := range cases {
		r, err := Compute(c.p, c.t)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = r
	}

	const goroutines = 32
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*len(cases)*5)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for rep := 0; rep < 5; rep++ {
				for i, c := range cases {
					r, err := Compute(c.p, c.t)
					if err != nil {
						errCh <- err
						return
					}
					if r.Lambda.Lambda != want[i].Lambda.Lambda ||
						r.Front != want[i].Front ||
						r.Ste != want[i].Ste {
						errCh <- errors.New("并行工况结果与单线程基准不一致，存在状态渗透")
						return
					}
					if r.Front < c.sMin || r.Front > c.sMax {
						errCh <- errors.New("锋面落出该工况独有区间")
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
