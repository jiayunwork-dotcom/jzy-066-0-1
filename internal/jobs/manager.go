// Package jobs 管理长时间锋面推进序列的异步可取消作业。
//
// 每个作业持有独立 context；取消时底层 front.ComputeSeries 立即返回
// context.Canceled，作业进入 canceled 状态，已算出的半成品点列
// 不会作为结果对外暴露。
package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"stefan-service/internal/front"
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

// Job 是一个推进序列作业的快照。
type Job struct {
	ID        string              `json:"id"`
	Spec      front.SeriesSpec    `json:"spec"`
	Status    Status              `json:"status"`
	CreatedAt time.Time           `json:"created_at"`
	StartedAt *time.Time          `json:"started_at,omitempty"`
	EndedAt   *time.Time          `json:"ended_at,omitempty"`
	Result    *front.SeriesResult `json:"result,omitempty"`
	Error     string              `json:"error,omitempty"`
}

// ErrNotFound 表示作业不存在。
var ErrNotFound = errors.New("作业不存在")

// Manager 管理作业生命周期，并发安全。
type Manager struct {
	seq  int64
	jobs sync.Map // id -> *jobEntry
}

type jobEntry struct {
	mu     sync.RWMutex
	cancel context.CancelFunc
	job    Job
}

// NewManager 创建作业管理器。
func NewManager() *Manager {
	return &Manager{}
}

// Start 提交一个序列作业并立即在独立 goroutine 中执行。
// 参数校验同步完成：非法 spec 直接返回错误，不会产生作业。
func (m *Manager) Start(spec front.SeriesSpec) (Job, error) {
	if err := spec.Validate(); err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	id := fmt.Sprintf("job-%06d", atomic.AddInt64(&m.seq, 1))
	now := time.Now().UTC()
	e := &jobEntry{
		cancel: cancel,
		job: Job{
			ID:        id,
			Spec:      spec,
			Status:    StatusQueued,
			CreatedAt: now,
		},
	}
	m.jobs.Store(id, e)

	go m.run(ctx, e, spec)
	return e.snapshot(), nil
}

// run 在 goroutine 中推进作业状态机。
func (m *Manager) run(ctx context.Context, e *jobEntry, spec front.SeriesSpec) {
	now := time.Now().UTC()
	e.mu.Lock()
	e.job.Status = StatusRunning
	e.job.StartedAt = &now
	e.mu.Unlock()

	res, err := front.ComputeSeries(ctx, spec)
	end := time.Now().UTC()

	e.mu.Lock()
	defer e.mu.Unlock()
	e.job.EndedAt = &end
	switch {
	case errors.Is(err, context.Canceled):
		e.job.Status = StatusCanceled
		// 半成品点列绝不挂到 Result 上。
		e.job.Result = nil
		e.job.Error = "作业已被取消，中间结果不返回"
	case err != nil:
		e.job.Status = StatusFailed
		e.job.Error = err.Error()
	default:
		e.job.Status = StatusCompleted
		r := res
		e.job.Result = &r
	}
}

// Get 取作业快照。
func (m *Manager) Get(id string) (Job, error) {
	v, ok := m.jobs.Load(id)
	if !ok {
		return Job{}, ErrNotFound
	}
	return v.(*jobEntry).snapshot(), nil
}

// Cancel 请求取消作业；对已结束的作业返回错误。
func (m *Manager) Cancel(id string) error {
	v, ok := m.jobs.Load(id)
	if !ok {
		return ErrNotFound
	}
	e := v.(*jobEntry)
	e.mu.RLock()
	st := e.job.Status
	e.mu.RUnlock()
	if st == StatusCompleted || st == StatusCanceled || st == StatusFailed {
		return fmt.Errorf("作业已处于 %s 状态，无法取消", st)
	}
	e.cancel()
	return nil
}

func (e *jobEntry) snapshot() Job {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.job
}
