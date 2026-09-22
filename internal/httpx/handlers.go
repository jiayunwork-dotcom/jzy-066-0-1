package httpx

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"stefanservice/internal/front"
	"stefanservice/internal/job"
	"stefanservice/internal/profile"
)

// mapCaseError 把工况/时刻校验错误映射成 HTTP 422 与稳定错误码，
// 错误原因原样带回给调用方。
func mapCaseError(err error) (int, string) {
	switch {
	case errors.Is(err, front.ErrNonPositiveAlpha):
		return http.StatusUnprocessableEntity, "alpha_not_positive"
	case errors.Is(err, front.ErrNonPositiveLf):
		return http.StatusUnprocessableEntity, "lf_not_positive"
	case errors.Is(err, front.ErrNonPositiveC):
		return http.StatusUnprocessableEntity, "c_not_positive"
	case errors.Is(err, front.ErrNonPositiveK):
		return http.StatusUnprocessableEntity, "k_not_positive"
	case errors.Is(err, front.ErrWallNotBelowFreeze):
		return http.StatusUnprocessableEntity, "wall_not_below_freeze"
	case errors.Is(err, front.ErrNonFinite):
		return http.StatusUnprocessableEntity, "non_finite_parameter"
	case errors.Is(err, front.ErrNegativeTime):
		return http.StatusUnprocessableEntity, "negative_time"
	case errors.Is(err, front.ErrNegativeDepth):
		return http.StatusUnprocessableEntity, "negative_depth"
	default:
		return http.StatusBadRequest, "bad_request"
	}
}

func (a *App) abortDomainError(c *gin.Context, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		fail(c, ae.status, ae.code, ae.msg)
		return
	}
	status, code := mapCaseError(err)
	fail(c, status, code, err.Error())
}

// ---- 正向核算 -----------------------------------------------------------

type forwardRequest struct {
	caseInput
	TimeSeconds float64 `json:"time_s"`
}

type rootView struct {
	Lambda         float64 `json:"lambda"`
	Residual       float64 `json:"residual"`
	ResidualRatio  float64 `json:"residual_over_ste"`
	Iterations     int     `json:"iterations"`
	Ste            float64 `json:"ste"`
	ApproxLambda   float64 `json:"approx_lambda"`
	ApproxResidual float64 `json:"approx_residual"`
	ApproxNote     string  `json:"approx_note"`
}

func toRootView(r front.Result) rootView {
	note := "精确分支：lambda 由超越方程数值求根；approx_lambda=sqrt(Ste/2) 仅为小 Ste 对照，未参与锋面计算"
	ratio := 0.0
	if r.Ste != 0 {
		ratio = r.Root.Residual / r.Ste
	}
	return rootView{
		Lambda:         r.Root.Lambda,
		Residual:       r.Root.Residual,
		ResidualRatio:  ratio,
		Iterations:     r.Root.Iterations,
		Ste:            r.Ste,
		ApproxLambda:   r.Root.Approx,
		ApproxResidual: r.Root.ApproxError,
		ApproxNote:     note,
	}
}

func (a *App) forward(c *gin.Context) {
	var req forwardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON 或缺字段: "+err.Error())
		return
	}
	cs, source, err := a.resolveCase(req.caseInput)
	if err != nil {
		a.abortDomainError(c, err)
		return
	}
	res, err := front.Evaluate(cs, req.TimeSeconds)
	if err != nil {
		a.abortDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"source":               source,
		"units":                gin.H{"front": "m", "wall_heat_flux": "W/m^2", "cumulative_heat": "J/m^2", "time": "s"},
		"ste":                  res.Ste,
		"root":                 toRootView(res),
		"time_s":               req.TimeSeconds,
		"front_m":              res.Front,
		"wall_heat_flux_w_m2":  res.WallHeatFlux,
		"heat_extraction_w_m2": res.HeatOutFlux,
		"cumulative_heat_j_m2": res.HeatPerArea,
		"approx_front_m":       res.ApproxFront,
		"approx_quality":       res.ApproxQuality,
		"note":                 res.Note,
	})
}

// ---- 反向核算 -----------------------------------------------------------

type inverseRequest struct {
	caseInput
	TargetFrontM float64 `json:"target_front_m"`
}

func (a *App) inverse(c *gin.Context) {
	var req inverseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON: "+err.Error())
		return
	}
	cs, source, err := a.resolveCase(req.caseInput)
	if err != nil {
		a.abortDomainError(c, err)
		return
	}
	d, secs, err := front.TimeToReach(cs, req.TargetFrontM)
	if err != nil {
		a.abortDomainError(c, err)
		return
	}
	// 反求后立即正算复核，把对应的锋面位置一并带回。
	check, err := front.Evaluate(cs, secs)
	if err != nil {
		a.abortDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"source":           source,
		"units":            gin.H{"time": "s", "front": "m"},
		"target_front_m":   req.TargetFrontM,
		"time_s":           secs,
		"time_human":       d.Round(time.Millisecond).String(),
		"ste":              check.Ste,
		"root":             toRootView(check),
		"verified_front_m": check.Front,
	})
}

// ---- 工况档 -------------------------------------------------------------

type profileBody struct {
	Name string `json:"name"`
	front.Case
}

func (a *App) listProfiles(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"profiles": a.Profiles.List()})
}

func (a *App) createProfile(c *gin.Context) {
	var b profileBody
	if err := c.ShouldBindJSON(&b); err != nil {
		fail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	p := profile.Profile{Name: b.Name, Case: b.Case, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := a.Profiles.Create(p); err != nil {
		a.abortProfileError(c, err)
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (a *App) getProfile(c *gin.Context) {
	p, err := a.Profiles.Get(c.Param("name"))
	if err != nil {
		a.abortProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (a *App) updateProfile(c *gin.Context) {
	var b profileBody
	if err := c.ShouldBindJSON(&b); err != nil {
		fail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if b.Name == "" {
		b.Name = c.Param("name")
	}
	if b.Name != c.Param("name") {
		fail(c, http.StatusBadRequest, "name_mismatch", "路径名与请求体 name 不一致")
		return
	}
	p := profile.Profile{Name: b.Name, Case: b.Case, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := a.Profiles.Update(p); err != nil {
		a.abortProfileError(c, err)
		return
	}
	got, _ := a.Profiles.Get(p.Name)
	c.JSON(http.StatusOK, got)
}

func (a *App) deleteProfile(c *gin.Context) {
	if err := a.Profiles.Delete(c.Param("name")); err != nil {
		a.abortProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": c.Param("name")})
}

func (a *App) abortProfileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		fail(c, http.StatusNotFound, "profile_not_found", err.Error())
	case errors.Is(err, profile.ErrExists):
		fail(c, http.StatusConflict, "profile_exists", err.Error())
	case errors.Is(err, profile.ErrEmptyName):
		fail(c, http.StatusBadRequest, "empty_name", err.Error())
	default:
		sc, code := mapCaseError(err)
		fail(c, sc, code, err.Error())
	}
}

// ---- 时间序列作业 --------------------------------------------------------

type seriesBody struct {
	caseInput
	Times []float64 `json:"times_s"`
}

func (a *App) submitSeries(c *gin.Context) {
	var b seriesBody
	if err := c.ShouldBindJSON(&b); err != nil {
		fail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	cs, _, err := a.resolveCase(b.caseInput)
	if err != nil {
		a.abortDomainError(c, err)
		return
	}
	snap, err := a.Jobs.Submit(job.SeriesRequest{Case: cs, Times: b.Times})
	if err != nil {
		a.abortDomainError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, snap)
}

func (a *App) getSeries(c *gin.Context) {
	snap, err := a.Jobs.Get(c.Param("id"))
	if err != nil {
		if errors.Is(err, job.ErrJobNotFound) {
			fail(c, http.StatusNotFound, "job_not_found", err.Error())
			return
		}
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, snap)
}

func (a *App) cancelSeries(c *gin.Context) {
	if err := a.Jobs.Cancel(c.Param("id")); err != nil {
		if errors.Is(err, job.ErrJobNotFound) {
			fail(c, http.StatusNotFound, "job_not_found", err.Error())
			return
		}
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	snap, _ := a.Jobs.Get(c.Param("id"))
	c.JSON(http.StatusOK, snap)
}
