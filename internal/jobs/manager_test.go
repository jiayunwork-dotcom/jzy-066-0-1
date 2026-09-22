package jobs

import (
	"errors"
	"testing"
	"time"

	"stefan-service/internal/front"
	"stefan-service/internal/stefan"
)

func ice() stefan.Params {
	return stefan.Params{C: 2100, Lf: 334e3, K: 2.22, Alpha: 1.15e-6, Tf: 0, Tw: -20}
}

// waitFor 轮询直到作业进入给定终态之一或超时。
func waitFor(t *testing.T, m *Manager, id string, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := m.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == want {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("作业 %s 未在超时前进入 %s", id, want)
	return Job{}
}

func TestCompleteReturnsFullSeries(t *testing.T) {
	m := NewManager()
	j, err := m.Start(front.SeriesSpec{Params: ice(), T0: 0, T1: 3600, Intervals: 8})
	if err != nil {
		t.Fatal(err)
	}
	done := waitFor(t, m, j.ID, StatusCompleted)
	if done.Result == nil || len(done.Result.Points) != 9 {
		t.Fatalf("完成态应携带完整 9 个点：%+v", done.Result)
	}
}

func TestCancelNeverReturnsPartial(t *testing.T) {
	m := NewManager()
	j, err := m.Start(front.SeriesSpec{
		Params: ice(), T0: 0, T1: 86400, Intervals: 500_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 给它一点时间进入 running 再取消。
	time.Sleep(5 * time.Millisecond)
	if err := m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	canceled := waitFor(t, m, j.ID, StatusCanceled)
	if canceled.Result != nil {
		t.Fatalf("取消态泄露了半成品点列：%+v", canceled.Result)
	}
	if canceled.Error == "" {
		t.Errorf("取消态应带说明")
	}
}

func TestCancelFinishedJob(t *testing.T) {
	m := NewManager()
	j, _ := m.Start(front.SeriesSpec{Params: ice(), T0: 0, T1: 10, Intervals: 1})
	waitFor(t, m, j.ID, StatusCompleted)
	if err := m.Cancel(j.ID); err == nil {
		t.Errorf("取消已完成作业应报错")
	}
	if _, err := m.Get("job-999999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("查询不存在作业应返回 ErrNotFound，得到 %v", err)
	}
	if err := m.Cancel("job-999999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("取消不存在作业应返回 ErrNotFound，得到 %v", err)
	}
}

func TestStartRejectsInvalidSpec(t *testing.T) {
	m := NewManager()
	bad := ice()
	bad.Alpha = 0
	if _, err := m.Start(front.SeriesSpec{Params: bad, T0: 0, T1: 10, Intervals: 2}); err == nil {
		t.Fatalf("非法 spec 不应产生作业")
	}
}

// TestParallelJobsIsolation 并行多作业互不串结果。
func TestParallelJobsIsolation(t *testing.T) {
	m := NewManager()
	p1 := ice()
	p2 := ice()
	p2.Tw = -50
	j1, err := m.Start(front.SeriesSpec{Params: p1, T0: 0, T1: 3600, Intervals: 4})
	if err != nil {
		t.Fatal(err)
	}
	j2, err := m.Start(front.SeriesSpec{Params: p2, T0: 0, T1: 1800, Intervals: 4})
	if err != nil {
		t.Fatal(err)
	}
	r1 := waitFor(t, m, j1.ID, StatusCompleted)
	r2 := waitFor(t, m, j2.ID, StatusCompleted)
	if r1.ID == r2.ID || r1.Result == nil || r2.Result == nil {
		t.Fatal("作业状态串台")
	}
	if r1.Result.Points[4].Front == r2.Result.Points[4].Front {
		t.Fatal("两个不同工况末点锋面不应相等，疑似结果互写")
	}
	// j2 温差大但时间短，这里只钉各自的 λ 不互相渗透。
	if mathAbs(r1.Result.Lambda-r2.Result.Lambda) < 1e-6 {
		t.Fatalf("两个工况 λ 异常接近：%.10f", r1.Result.Lambda)
	}
}

func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
