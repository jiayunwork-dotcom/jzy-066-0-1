// Package server 组装 HTTP 路由：正向核算、时间反求、工况档管理、
// 可取消推进作业。它只做协议适配，物理计算全部在内核包。
package server

import (
	"errors"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"

	"stefan-service/internal/front"
	"stefan-service/internal/jobs"
	"stefan-service/internal/profile"
	"stefan-service/internal/reverse"
	"stefan-service/internal/stefan"
)

// Server 持有路由处理依赖。
type Server struct {
	profiles *profile.Store
	jobs     *jobs.Manager
}

// New 创建装配好的 Server（不注册路由，路由由 Handler 暴露以便测试）。
func New(profiles *profile.Store) *Server {
	return &Server{profiles: profiles, jobs: jobs.NewManager()}
}

// apiError 是所有 4xx/5xx 的统一错误结构，必带原因。
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func abortErr(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, apiError{Code: code, Message: msg})
}

// mapDomainError 把内核包错误翻译成 HTTP 响应。
func mapDomainError(c *gin.Context, err error) {
	var ve *stefan.ValidationError
	if errors.As(err, &ve) {
		c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
			Code: "invalid_parameter", Field: ve.Field, Message: ve.Reason,
		})
		return
	}
	switch {
	case errors.Is(err, stefan.ErrInvalidSte):
		abortErr(c, http.StatusBadRequest, "invalid_ste", err.Error())
	case errors.Is(err, front.ErrNegativeTime):
		abortErr(c, http.StatusBadRequest, "invalid_time", err.Error())
	case errors.Is(err, reverse.ErrNonPositiveThickness):
		abortErr(c, http.StatusBadRequest, "invalid_thickness", err.Error())
	case errors.Is(err, profile.ErrNotFound):
		abortErr(c, http.StatusNotFound, "profile_not_found", err.Error())
	case errors.Is(err, profile.ErrExists):
		abortErr(c, http.StatusConflict, "profile_exists", err.Error())
	case errors.Is(err, profile.ErrInvalidName):
		abortErr(c, http.StatusBadRequest, "invalid_profile_name", err.Error())
	case errors.Is(err, jobs.ErrNotFound):
		abortErr(c, http.StatusNotFound, "job_not_found", err.Error())
	default:
		abortErr(c, http.StatusBadRequest, "bad_request", err.Error())
	}
}

// Handler 构建 Gin 路由引擎。
func (s *Server) Handler() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "stefan-front"})
	})

	api := r.Group("/api/v1")
	{
		api.POST("/stefan/forward", s.handleForward)
		api.POST("/stefan/reverse", s.handleReverse)

		api.GET("/profiles", s.listProfiles)
		api.POST("/profiles", s.createProfile)
		api.GET("/profiles/:name", s.getProfile)
		api.DELETE("/profiles/:name", s.deleteProfile)

		api.POST("/jobs", s.startJob)
		api.GET("/jobs/:id", s.getJob)
		api.POST("/jobs/:id/cancel", s.cancelJob)
	}
	return r
}

// ---- 请求体 ----

type forwardReq struct {
	Profile string         `json:"profile"`
	Params  *stefan.Params `json:"params"`
	Time    float64        `json:"time"`
}

type reverseBody struct {
	Profile string         `json:"profile"`
	Params  *stefan.Params `json:"params"`
	S       float64        `json:"s"`
}

type profileReq struct {
	Name        string        `json:"name" binding:"required"`
	Description string        `json:"description"`
	Params      stefan.Params `json:"params"`
}

type jobReq struct {
	Profile   string         `json:"profile"`
	Params    *stefan.Params `json:"params"`
	T0        float64        `json:"t0"`
	T1        float64        `json:"t1"`
	Intervals int            `json:"intervals"`
}

// resolveParams 从请求中解析物性：要么内联 params，要么引用已建档名字。
func (s *Server) resolveParams(inline *stefan.Params, name string) (stefan.Params, error) {
	if inline != nil && name != "" {
		return stefan.Params{}, errors.New("params 与 profile 只能提供其一")
	}
	if inline != nil {
		return *inline, nil
	}
	if name == "" {
		return stefan.Params{}, errors.New("必须提供内联 params 或已建档的 profile 名字")
	}
	pr, err := s.profiles.Get(name)
	if err != nil {
		return stefan.Params{}, err
	}
	return pr.Params, nil
}

// ---- 响应 DTO（处理 +Inf 热流的 JSON 序列化）----

// floatOrNil 把非有限浮点（如 t=0 的理想无穷热流）序列化为 null。
func floatOrNil(v float64) *float64 {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return nil
	}
	return &v
}

type lambdaDTO struct {
	Lambda      float64 `json:"lambda"`
	Ste         float64 `json:"ste"`
	F           float64 `json:"f"`
	AbsResidual float64 `json:"abs_residual"`
	RelResidual float64 `json:"rel_residual"`
	Iterations  int     `json:"iterations"`
}

type forwardResp struct {
	Ste          float64   `json:"ste"`
	Lambda       lambdaDTO `json:"lambda"`
	LambdaApprox lambdaDTO `json:"lambda_approx"`
	// ApproxNote 明确：lambda_approx 只是小 Ste 对照分支，不得当精确根使用。
	ApproxNote   string   `json:"lambda_approx_note"`
	Time         float64  `json:"time"`
	Front        float64  `json:"front"`
	FrontSpeed   *float64 `json:"front_speed"`
	WallHeatFlux *float64 `json:"wall_heat_flux"`
	Note         string   `json:"wall_heat_flux_note,omitempty"`
}

func toLambdaDTO(l stefan.LambdaResult) lambdaDTO {
	return lambdaDTO{
		Lambda: l.Lambda, Ste: l.Ste, F: l.F,
		AbsResidual: l.AbsResidual, RelResidual: l.RelResidual,
		Iterations: l.Iterations,
	}
}
