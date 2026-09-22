package job

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"stefanservice/internal/front"
)

func caseForTest() front.Case {
	return front.Case{C: 2100, K: 2.22, Alpha: 1.15e-6, Lf: 334e3, Tf: 273.15, Tw: 243.15}
}

// TestSeries_CompletesWithAllPoints 正常作业：completed，点列完整，
// √t 关系在序列上成立，t=0 点锋面为零。
func TestSeries_CompletesWithAllPoints(t *testing.T) {
	mgr := NewManager()
	times := []float64{0, 100, 400, 900, 1600, 3600}
	snap, err := mgr.Submit(SeriesRequest{Case: caseForTest(), Times: times})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = mgr.Wait(context.Background(), snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != StatusCompleted {
		t.Fatalf("status = %v (%s)", snap.Status, snap.Error)
	}
	if len(snap.Points) != len(times) {
		t.Fatalf("points = %d, want %d", len(snap.Points), len(times))
	}
	if snap.Points[0].Front != 0 {
		t.Fatalf("front at t=0 = %v", snap.Points[0].Front)
	}
	// 100s 与 400s（4×）锋面正好加倍。
	if got := snap.Points[2].Front / snap.Points[1].Front; got != 2.0 {
		t.Fatalf("s(400)/s(100) = %v, want 2", got)
	}
	// 所有点共用同一个 λ。
	lam := snap.Points[1].Root.Lambda
	for _, p := range snap.Points[1:] {
		if p.Root.Lambda != lam {
			t.Fatal("lambda varies across series points")
		}
	}
}

// TestSeries_CancelDiscardsPartialPoints 取消的作业绝不返回半列点。
// 用一个预先取消的 context 做确定性测试：作业一启动即注定 canceled。
func TestSeries_CancelDiscardsPartialPoints(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(mgr.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	snap, err := mgr.submit(ctx, SeriesRequest{
		Case:  caseForTest(),
		Times: make([]float64, 500_000), // 保证不会“恰好瞬间做完”
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = mgr.Wait(context.Background(), snap.ID)
	if snap.Status != StatusCanceled {
		t.Fatalf("status = %v, want canceled", snap.Status)
	}
	if snap.Points != nil {
		t.Fatalf("canceled job leaked %d partial points", len(snap.Points))
	}
}

// TestSeries_CancelLongRunning HTTP 式取消：提交一个巨型作业，
// 观察到 running 后取消，终态必须是 canceled 且点列为空。
func TestSeries_CancelLongRunning(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(mgr.Shutdown)
	snap, err := mgr.Submit(SeriesRequest{
		Case:  caseForTest(),
		Times: make([]float64, 2_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 等它真的开始推进。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := mgr.Get(snap.ID)
		if cur.Progress > 0 || cur.Status == StatusRunning {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := mgr.Cancel(snap.ID); err != nil {
		t.Fatal(err)
	}
	final, _ := mgr.Wait(context.Background(), snap.ID)
	if final.Status != StatusCanceled {
		t.Fatalf("status = %v, want canceled", final.Status)
	}
	if final.Points != nil {
		t.Fatalf("canceled long job leaked %d points", len(final.Points))
	}
}

// TestSeries_DoubleCancelAndUnknown 重复取消安全；未知作业报错。
func TestSeries_DoubleCancelAndUnknown(t *testing.T) {
	mgr := NewManager()
	snap, _ := mgr.Submit(SeriesRequest{Case: caseForTest(), Times: []float64{1, 2, 3}})
	if err := mgr.Cancel(snap.ID); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Cancel(snap.ID); err != nil {
		t.Fatalf("second cancel: %v", err)
	}
	if err := mgr.Cancel("does-not-exist"); err != ErrJobNotFound {
		t.Fatalf("unknown cancel: %v", err)
	}
	if _, err := mgr.Get("nope"); err != ErrJobNotFound {
		t.Fatalf("unknown get: %v", err)
	}
	final, _ := mgr.Wait(context.Background(), snap.ID)
	if final.Status != StatusCanceled {
		t.Fatalf("status = %v", final.Status)
	}
}

// TestSeries_InvalidRequests 工况与时间点在启动前被拦截，不登记作业。
func TestSeries_InvalidRequests(t *testing.T) {
	mgr := NewManager()
	if _, err := mgr.Submit(SeriesRequest{Case: caseForTest(), Times: nil}); err != ErrEmptySeries {
		t.Fatalf("empty series: %v", err)
	}
	badCase := caseForTest()
	badCase.Lf = 0
	if _, err := mgr.Submit(SeriesRequest{Case: badCase, Times: []float64{1}}); err == nil {
		t.Fatal("non-positive Lf accepted")
	}
	if _, err := mgr.Submit(SeriesRequest{Case: caseForTest(), Times: []float64{1, -2}}); err != ErrBadTime {
		t.Fatalf("negative time: %v", err)
	}
	if len(mgr.jobs) != 0 {
		t.Fatalf("invalid requests registered %d jobs", len(mgr.jobs))
	}
}

// TestSeries_ParallelIsolation 多个作业并行，各自的 λ 与点列互不渗透。
func TestSeries_ParallelIsolation(t *testing.T) {
	mgr := NewManager()
	const n = 12
	ids := make([]string, n)
	wantLam := make(map[string]float64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := caseForTest()
			c.Tw = c.Tf - float64(2*(i+1)) // 每种过冷度一个 λ
			snap, err := mgr.Submit(SeriesRequest{
				Case:  c,
				Times: []float64{0, 60, 600, 3600},
			})
			if err != nil {
				t.Errorf("submit %d: %v", i, err)
				return
			}
			ids[i] = snap.ID
			done, _ := mgr.Wait(context.Background(), snap.ID)
			if done.Status != StatusCompleted {
				t.Errorf("job %d status %v", i, done.Status)
				return
			}
			lam := done.Points[1].Root.Lambda
			// 点列内所有 λ 一致，且锋面随自己的 λ 走。
			for _, p := range done.Points {
				if p.Root.Lambda != lam {
					t.Errorf("job %d internal lambda mismatch", i)
				}
			}
			mgr.mu.Lock()
			wantLam[snap.ID] = lam
			mgr.mu.Unlock()
		}(i)
	}
	wg.Wait()

	// 每个作业的 λ 都应与兄弟作业不同，并复核其锋面由自己的 λ 产生。
	seen := map[float64]string{}
	for id, lam := range wantLam {
		if other, dup := seen[lam]; dup {
			t.Fatalf("lambda %v shared by jobs %s and %s", lam, id, other)
		}
		seen[lam] = id
		snap, _ := mgr.Get(id)
		c := mgr.jobs[id].req.Case
		for _, p := range snap.Points {
			want := 2 * lam * math.Sqrt(c.Alpha*p.Time)
			if math.Abs(p.Front-want) > 1e-12 {
				t.Fatalf("job %s point t=%v front contaminated", id, p.Time)
			}
		}
	}
}
