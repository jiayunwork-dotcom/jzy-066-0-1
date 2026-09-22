// Package job 管理“长时间序列锋面推进计算”的可取消作业。
//
// 语义约定：
//   - λ 在作业内只求解一次（与时间无关），随后逐点核算；
//   - 作业只有两种终态：completed（点列完整可用）或 canceled（不返回
//     任何算了一半的点列，points 恒为空）；
//   - 取消是协作式的：每个时间点之前检查 context；
//   - 每个作业独立持有自己的工况、根与点列，作业之间不共享中间量。
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"runtime"
	"sync"
	"time"

	"stefanservice/internal/front"
	"stefanservice/internal/stefan"
)

var (
	ErrJobNotFound = errors.New("job: 作业不存在")
	ErrEmptySeries = errors.New("job: 时间序列为空")
	ErrBadTime     = errors.New("job: 时间点必须是 t >= 0 的有限实数")
)

// Status 是作业状态。
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusCanceled  Status = "canceled"
	StatusFailed    Status = "failed"
)

// SeriesRequest 描述一次锋面推进时间序列作业。
type SeriesRequest struct {
	Case  front.Case `json:"case"`
	Times []float64  `json:"times_s"` // 秒，允许包含 0
}

// Point 是单个时间点的核算结果。
type Point struct {
	Time float64 `json:"time_s"`
	front.Result
}

// Job 是一个作业的内部表示（字段均由 Manager 的锁保护）。
type Job struct {
	id          string
	req         SeriesRequest
	status      Status
	progress    int
	points      []Point // 仅在 completed 时挂载
	failure     string
	createdAt   time.Time
	completedAt time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

// Snapshot 是作业对外的只读快照。
type Snapshot struct {
	ID          string  `json:"id"`
	Status      Status  `json:"status"`
	Progress    int     `json:"progress"`
	Total       int     `json:"total"`
	Points      []Point `json:"points,omitempty"` // 仅 completed 非空
	Error       string  `json:"error,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt string  `json:"completed_at,omitempty"`
}

// Manager 维护作业注册表并调度工作协程。
type Manager struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

// NewManager 创建空的作业管理器。
func NewManager() *Manager {
	return &Manager{jobs: make(map[string]*Job)}
}

// Shutdown 取消所有仍在运行的作业（不等待），用于服务关停。
func (m *Manager) Shutdown() {
	m.mu.Lock()
	jobs := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()
	for _, j := range jobs {
		j.cancel()
	}
}

// validateRequest 在启动作业前校验工况与时间点。
func validateRequest(req SeriesRequest) error {
	if err := req.Case.Validate(); err != nil {
		return err
	}
	if len(req.Times) == 0 {
		return ErrEmptySeries
	}
	for _, t := range req.Times {
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 {
			return ErrBadTime
		}
	}
	return nil
}

// Submit 校验入参、登记作业并异步开始计算，返回作业 ID 快照。
func (m *Manager) Submit(req SeriesRequest) (Snapshot, error) {
	return m.submit(context.Background(), req)
}

// submit 允许调用方提供外部 ctx（测试中用来确定性地预取消）；
// 返回的作业同时受内部 cancel 控制。
func (m *Manager) submit(parent context.Context, req SeriesRequest) (Snapshot, error) {
	if err := validateRequest(req); err != nil {
		return Snapshot{}, err
	}
	ctx, cancel := context.WithCancel(parent)
	j := &Job{
		id:        newID(),
		req:       req,
		status:    StatusQueued,
		createdAt: time.Now().UTC(),
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	m.mu.Lock()
	m.jobs[j.id] = j
	m.mu.Unlock()

	go m.run(ctx, j)
	return m.snapshotOf(j), nil
}

// run 是工作协程：λ 只求一次，逐点推进，取消即丢弃全部部分结果。
func (m *Manager) run(ctx context.Context, j *Job) {
	defer close(j.done)

	m.mu.Lock()
	j.status = StatusRunning
	m.mu.Unlock()

	// 取消优先：若 context 在作业真正开始前就已取消，直接终结，
	// 不做任何（可能很大的）结果切片分配。
	select {
	case <-ctx.Done():
		m.finish(j, StatusCanceled, nil, "")
		return
	default:
	}

	ste := stefan.Number(j.req.Case.C, j.req.Case.Tf, j.req.Case.Tw, j.req.Case.Lf)
	root, err := stefan.Solve(ste)
	if err != nil {
		m.finish(j, StatusFailed, nil, err.Error())
		return
	}

	// 部分结果只活在这个局部切片里；只有全部算完才挂到作业上。
	// 容量封顶，避免超长时间序列在开始计算前一次性吞下巨量内存。
	cap0 := len(j.req.Times)
	if cap0 > 4096 {
		cap0 = 4096
	}
	pts := make([]Point, 0, cap0)
	for i, t := range j.req.Times {
		// 每个点之前都响应取消。
		select {
		case <-ctx.Done():
			m.finish(j, StatusCanceled, nil, "")
			return
		default:
		}

		r := front.EvaluateWithRoot(j.req.Case, t, root)
		pts = append(pts, Point{Time: t, Result: r})

		m.mu.Lock()
		j.progress = i + 1
		m.mu.Unlock()

		// 给调度器/取消方留出即时插入点，避免紧密循环拖住取消响应。
		if i&0x3ff == 0 {
			runtime.Gosched()
		}
	}
	m.finish(j, StatusCompleted, pts, "")
}

// finish 终结作业；canceled/failed 时 points 必须为 nil。
func (m *Manager) finish(j *Job, st Status, pts []Point, failure string) {
	now := time.Now().UTC()
	m.mu.Lock()
	j.status = st
	j.progress = len(pts)
	j.points = pts // 取消与失败路径传入 nil：部分点列绝不外泄
	j.failure = failure
	j.completedAt = now
	m.mu.Unlock()
	j.cancel() // 释放 context 资源
}

// Get 取作业快照；不存在返回 ErrJobNotFound。
func (m *Manager) Get(id string) (Snapshot, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return Snapshot{}, ErrJobNotFound
	}
	return m.snapshotOf(j), nil
}

// Cancel 请求取消作业。已结束的作业取消其终态不变；
// 重复取消一个已经（或必将）取消的作业返回 nil。
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return ErrJobNotFound
	}
	j.cancel()
	return nil
}

// Wait 阻塞到作业进入终态或 ctx 结束，返回届时快照。
func (m *Manager) Wait(ctx context.Context, id string) (Snapshot, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return Snapshot{}, ErrJobNotFound
	}
	select {
	case <-j.done:
	case <-ctx.Done():
	}
	return m.snapshotOf(j), nil
}

// snapshotLocked 必须在持有 m.mu 时调用，组装对外只读副本。
func (m *Manager) snapshotLocked(j *Job) Snapshot {
	s := Snapshot{
		ID:        j.id,
		Status:    j.status,
		Progress:  j.progress,
		Total:     len(j.req.Times),
		Error:     j.failure,
		CreatedAt: j.createdAt.Format(time.RFC3339Nano),
	}
	if !j.completedAt.IsZero() {
		s.CompletedAt = j.completedAt.Format(time.RFC3339Nano)
	}
	if j.status == StatusCompleted {
		// 复制一份切片，避免调用方改动内部点列。
		s.Points = append([]Point(nil), j.points...)
	}
	return s
}

// snapshot 在加锁状态下取作业快照。
func (m *Manager) snapshotOf(j *Job) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(j)
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
