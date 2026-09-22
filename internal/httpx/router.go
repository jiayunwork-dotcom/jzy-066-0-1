// Package httpx 装配一维 Stefan 凝固锋面核算服务的 HTTP 接口（Gin）。
//
// 本包只做协议适配：绑定、合并工况档、调用 front/profile/job 内核、
// 统一错误结构。物理计算与求根不在本包发生。
package httpx

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"stefanservice/internal/front"
	"stefanservice/internal/job"
	"stefanservice/internal/profile"
)

// App 持有路由处理器依赖的存储与作业管理器。
type App struct {
	Profiles *profile.Store
	Jobs     *job.Manager
}

// NewRouter 构建带统一错误处理与全部路由的 gin 引擎。
func NewRouter(app *App) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api/v1")
	{
		api.GET("/healthz", app.health)

		api.POST("/forward", app.forward)
		api.POST("/inverse", app.inverse)

		api.GET("/profiles", app.listProfiles)
		api.POST("/profiles", app.createProfile)
		api.GET("/profiles/:name", app.getProfile)
		api.PUT("/profiles/:name", app.updateProfile)
		api.DELETE("/profiles/:name", app.deleteProfile)

		api.POST("/series", app.submitSeries)
		api.GET("/series/:id", app.getSeries)
		api.POST("/series/:id/cancel", app.cancelSeries)
	}
	r.NoRoute(notFound)
	return r
}

func (a *App) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "stefan-front"})
}

func notFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, ErrorBody{
		Code:    "not_found",
		Message: "接口不存在",
	})
}

// ErrorBody 是所有错误响应的统一结构。
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func fail(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, ErrorBody{Code: code, Message: msg})
}

// caseInput 允许所有字段缺省（配合工况档引用，按指针覆盖）。
type caseInput struct {
	Profile string   `json:"profile"`
	C       *float64 `json:"c"`
	K       *float64 `json:"k"`
	Alpha   *float64 `json:"alpha"`
	Lf      *float64 `json:"lf"`
	Tf      *float64 `json:"tf"`
	Tw      *float64 `json:"tw"`
}

func (in caseInput) hasAny() bool {
	return in.C != nil || in.K != nil || in.Alpha != nil ||
		in.Lf != nil || in.Tf != nil || in.Tw != nil
}

// resolveCase 以引用的工况档为底本，逐字段用请求中显式给出的值覆盖。
func (a *App) resolveCase(in caseInput) (front.Case, string, error) {
	cs := front.Case{}
	source := "inline"
	if in.Profile != "" {
		p, err := a.Profiles.Get(in.Profile)
		if err != nil {
			if errors.Is(err, profile.ErrNotFound) {
				return cs, "", &apiError{http.StatusNotFound, "profile_not_found", "工况档不存在: " + in.Profile}
			}
			return cs, "", err
		}
		cs = p.Case
		source = p.Name
	}
	if in.C != nil {
		cs.C = *in.C
	}
	if in.K != nil {
		cs.K = *in.K
	}
	if in.Alpha != nil {
		cs.Alpha = *in.Alpha
	}
	if in.Lf != nil {
		cs.Lf = *in.Lf
	}
	if in.Tf != nil {
		cs.Tf = *in.Tf
	}
	if in.Tw != nil {
		cs.Tw = *in.Tw
	}
	return cs, source, nil
}

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }
