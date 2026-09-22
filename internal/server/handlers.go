package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"stefan-service/internal/front"
	"stefan-service/internal/jobs"
	"stefan-service/internal/profile"
	"stefan-service/internal/reverse"
)

// handleForward 正向核算：物性 + 时刻 → Ste、λ(含残差)、锋面位置、壁面热流。
func (s *Server) handleForward(c *gin.Context) {
	var req forwardReq
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid_body", "请求体解析失败："+err.Error())
		return
	}
	p, err := s.resolveParams(req.Params, req.Profile)
	if err != nil {
		mapDomainError(c, err)
		return
	}
	res, err := front.Compute(p, req.Time)
	if err != nil {
		mapDomainError(c, err)
		return
	}

	out := forwardResp{
		Ste:          res.Ste,
		Lambda:       toLambdaDTO(res.Lambda),
		LambdaApprox: toLambdaDTO(res.LambdaApprox),
		ApproxNote:   "lambda_approx 为小 Stefan 数近似 λ≈√(Ste/2) 的对照分支，仅在 Ste≪1 时可用，正式结果一律以 lambda（精确数值根）为准",
		Time:         res.Time,
		Front:        res.Front,
		FrontSpeed:   floatOrNil(res.FrontSpeed),
		WallHeatFlux: floatOrNil(res.WallHeatFlux),
	}
	if req.Time == 0 {
		out.Note = "t=0 为理想阶跃冷边界，理论热流与速度为 +∞，此处以 null 表示"
	}
	c.JSON(http.StatusOK, out)
}

// handleReverse 反向核算：目标厚度 → 所需时间。
func (s *Server) handleReverse(c *gin.Context) {
	var req reverseBody
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid_body", "请求体解析失败："+err.Error())
		return
	}
	p, err := s.resolveParams(req.Params, req.Profile)
	if err != nil {
		mapDomainError(c, err)
		return
	}
	res, err := reverse.Time(reverse.Request{Params: p, S: req.S})
	if err != nil {
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ste":         res.Ste,
		"lambda":      toLambdaDTO(res.Lambda),
		"s":           res.S,
		"time":        res.Time,
		"front_speed": res.FrontSpeed,
	})
}

// ---- 工况档 ----

func (s *Server) listProfiles(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"profiles": s.profiles.List()})
}

func (s *Server) createProfile(c *gin.Context) {
	var req profileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid_body", "请求体解析失败："+err.Error())
		return
	}
	pr := profile.Profile{Name: req.Name, Description: req.Description, Params: req.Params}
	if err := s.profiles.Put(pr, false); err != nil {
		// 重名时允许 PUT 语义？这里创建严格拒绝，覆盖走单独 handler 不必要，
		// 直接返回 409 提示。
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, pr)
}

func (s *Server) getProfile(c *gin.Context) {
	pr, err := s.profiles.Get(c.Param("name"))
	if err != nil {
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, pr)
}

func (s *Server) deleteProfile(c *gin.Context) {
	if err := s.profiles.Delete(c.Param("name")); err != nil {
		mapDomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- 作业 ----

func (s *Server) startJob(c *gin.Context) {
	var req jobReq
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid_body", "请求体解析失败："+err.Error())
		return
	}
	p, err := s.resolveParams(req.Params, req.Profile)
	if err != nil {
		mapDomainError(c, err)
		return
	}
	spec := front.SeriesSpec{Params: p, T0: req.T0, T1: req.T1, Intervals: req.Intervals}
	job, err := s.jobs.Start(spec)
	if err != nil {
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, jobView(job))
}

func (s *Server) getJob(c *gin.Context) {
	job, err := s.jobs.Get(c.Param("id"))
	if err != nil {
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, jobView(job))
}

func (s *Server) cancelJob(c *gin.Context) {
	if err := s.jobs.Cancel(c.Param("id")); err != nil {
		mapDomainError(c, err)
		return
	}
	job, _ := s.jobs.Get(c.Param("id"))
	c.JSON(http.StatusOK, jobView(job))
}

// pointDTO 序列化序列采样点，t=0 的 +Inf 热流输出为 null。
type pointDTO struct {
	Time         float64  `json:"time"`
	Front        float64  `json:"front"`
	WallHeatFlux *float64 `json:"wall_heat_flux"`
}

type jobViewDTO struct {
	ID        string           `json:"id"`
	Status    jobs.Status      `json:"status"`
	Spec      front.SeriesSpec `json:"spec"`
	CreatedAt string           `json:"created_at"`
	Error     string           `json:"error,omitempty"`
	Points    []pointDTO       `json:"points,omitempty"`
	Count     int              `json:"count,omitempty"`
}

func jobView(j jobs.Job) jobViewDTO {
	v := jobViewDTO{
		ID: j.ID, Status: j.Status, Spec: j.Spec,
		CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Error:     j.Error,
	}
	if j.Result != nil {
		v.Count = len(j.Result.Points)
		v.Points = make([]pointDTO, len(j.Result.Points))
		for i, pt := range j.Result.Points {
			v.Points[i] = pointDTO{
				Time:         pt.Time,
				Front:        pt.Front,
				WallHeatFlux: floatOrNil(pt.WallHeatFlux),
			}
		}
	}
	return v
}
