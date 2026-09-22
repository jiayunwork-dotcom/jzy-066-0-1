package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"stefanservice/internal/job"
	"stefanservice/internal/profile"
)

const iceBody = `{
  "c": 2100, "k": 2.22, "alpha": 1.15e-6, "lf": 334000,
  "tf": 273.15, "tw": 243.15, "time_s": 3600
}`

func newTestApp(t *testing.T) (http.Handler, *job.Manager) {
	t.Helper()
	store, err := profile.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureBuiltin(profile.IceProfile()); err != nil {
		t.Fatal(err)
	}
	jobs := job.NewManager()
	t.Cleanup(jobs.Shutdown)
	app := &App{Profiles: store, Jobs: jobs}
	return NewRouter(app), jobs
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("response not json: %v: %s", err, w.Body.String())
		}
	}
	return w.Code, out
}

func asFloat(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q missing in %v", key, m)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("key %q = %v not number", key, v)
	}
	return f
}

// TestForward_IceCentimeterPerHour 预置冰物性：壁温 -30℃，一小时厘米量级，
// 且精确根残差、热流齐备。
func TestForward_IceCentimeterPerHour(t *testing.T) {
	h, _ := newTestApp(t)
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/forward", iceBody)
	if code != http.StatusOK {
		t.Fatalf("code=%d body=%v", code, out)
	}
	s := asFloat(t, out, "front_m")
	if s < 0.01 || s > 0.10 {
		t.Fatalf("one-hour ice front %.4f m not in cm range", s)
	}
	root, ok := out["root"].(map[string]any)
	if !ok {
		t.Fatal("root missing")
	}
	if res, _ := root["residual"].(float64); math.Abs(res) > 1e-10 {
		t.Fatalf("residual %.3e", res)
	}
	if _, ok := root["approx_lambda"]; !ok {
		t.Fatal("approx branch must be reported")
	}
	q := asFloat(t, out, "wall_heat_flux_w_m2")
	if q <= 0 {
		t.Fatalf("heat flux %v must be positive (into material)", q)
	}
	// 锋面必须用精确 λ 而非近似 λ。
	lam := root["lambda"].(float64)
	want := 2 * lam * math.Sqrt(1.15e-6*3600)
	if math.Abs(s-want) > 1e-15 {
		t.Fatal("front not computed from exact lambda")
	}
}

// TestForward_ZeroTime t=0：锋面恒为零，热流字段为 null。
func TestForward_ZeroTime(t *testing.T) {
	h, _ := newTestApp(t)
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/forward",
		`{"c":2100,"k":2.22,"alpha":1.15e-6,"lf":334000,"tf":273.15,"tw":243.15,"time_s":0}`)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if asFloat(t, out, "front_m") != 0 {
		t.Fatalf("front at t=0 = %v", out["front_m"])
	}
	if out["wall_heat_flux_w_m2"] != nil {
		t.Fatalf("heat flux at t=0 must be null, got %v", out["wall_heat_flux_w_m2"])
	}
}

// TestForward_RejectInvalidCases 非法物性在解方程之前被拦下并带回原因。
func TestForward_RejectInvalidCases(t *testing.T) {
	h, _ := newTestApp(t)
	base := `{"c":2100,"k":2.22,"alpha":%v,"lf":%v,"tf":273.15,"tw":%v,"time_s":60}`
	cases := []struct {
		body string
		code string
	}{
		{fmt.Sprintf(base, 0, 334000, 243.15), "alpha_not_positive"},
		{fmt.Sprintf(base, -1e-6, 334000, 243.15), "alpha_not_positive"},
		{fmt.Sprintf(base, 1.15e-6, 0, 243.15), "lf_not_positive"},
		{fmt.Sprintf(base, 1.15e-6, 334000, 273.15), "wall_not_below_freeze"},
		{fmt.Sprintf(base, 1.15e-6, 334000, 300), "wall_not_below_freeze"},
	}
	for _, tc := range cases {
		code, out := doJSON(t, h, http.MethodPost, "/api/v1/forward", tc.body)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("want 422, got %d for %s", code, tc.body)
		}
		if out["code"] != tc.code {
			t.Fatalf("code=%v want %s (msg=%v)", out["code"], tc.code, out["message"])
		}
	}
	// 坏 JSON 走 400。
	if code, _ := doJSON(t, h, http.MethodPost, "/api/v1/forward", "{not json"); code != http.StatusBadRequest {
		t.Fatalf("bad json code = %d", code)
	}
}

// TestForward_SqrtTimeRelation t→4t 锋面加倍（跨请求）。
func TestForward_SqrtTimeRelation(t *testing.T) {
	h, _ := newTestApp(t)
	mk := func(ts float64) string {
		return fmt.Sprintf(`{"c":2100,"k":2.22,"alpha":1.15e-6,"lf":334000,"tf":273.15,"tw":253.15,"time_s":%g}`, ts)
	}
	_, o1 := doJSON(t, h, http.MethodPost, "/api/v1/forward", mk(900))
	_, o4 := doJSON(t, h, http.MethodPost, "/api/v1/forward", mk(3600))
	ratio := asFloat(t, o4, "front_m") / asFloat(t, o1, "front_m")
	if math.Abs(ratio-2) > 1e-12 {
		t.Fatalf("ratio = %.12f, want 2", ratio)
	}
}

// TestInverse_RoundTrip 反向求时再核对锋面。
func TestInverse_RoundTrip(t *testing.T) {
	h, _ := newTestApp(t)
	body := `{"c":2100,"k":2.22,"alpha":1.15e-6,"lf":334000,"tf":273.15,"tw":243.15,"target_front_m":0.05}`
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/inverse", body)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	ts := asFloat(t, out, "time_s")
	if ts <= 0 {
		t.Fatalf("time = %v", ts)
	}
	if math.Abs(asFloat(t, out, "verified_front_m")-0.05) > 1e-10 {
		t.Fatalf("verified front %v", out["verified_front_m"])
	}
	// 非法厚度被拦截。
	code, out = doJSON(t, h, http.MethodPost, "/api/v1/inverse",
		`{"c":2100,"k":2.22,"alpha":1.15e-6,"lf":334000,"tf":273.15,"tw":243.15,"target_front_m":-0.01}`)
	if code != 422 || out["code"] != "negative_depth" {
		t.Fatalf("inverse negative depth: %d %v", code, out)
	}
}

// TestProfiles_CRUD 建档、取回、重算、删除全链路。
func TestProfiles_CRUD(t *testing.T) {
	h, _ := newTestApp(t)

	code, list := doJSON(t, h, http.MethodGet, "/api/v1/profiles", "")
	if code != 200 {
		t.Fatal(list)
	}
	if profs := list["profiles"].([]any); len(profs) != 1 {
		t.Fatalf("expected only builtin ice profile, got %d", len(profs))
	}

	create := `{"name":"steel-shell","c":500,"k":40,"alpha":1.1e-5,"lf":270000,"tf":1808,"tw":1200}`
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/profiles", create)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, out)
	}
	code, out = doJSON(t, h, http.MethodGet, "/api/v1/profiles/steel-shell", "")
	if code != 200 {
		t.Fatalf("get %d %v", code, out)
	}
	if cs := out["case"].(map[string]any); cs["alpha"].(float64) != 1.1e-5 {
		t.Fatalf("alpha %v", cs["alpha"])
	}

	// 凭名字直接做正向核算。
	fwd := `{"profile":"steel-shell","time_s":600}`
	code, fout := doJSON(t, h, http.MethodPost, "/api/v1/forward", fwd)
	if code != 200 {
		t.Fatalf("profile forward %d %v", code, fout)
	}
	if fout["source"] != "steel-shell" {
		t.Fatalf("source = %v", fout["source"])
	}

	// 覆盖壁温：profile 底本 + 字段覆盖。
	fwd2 := `{"profile":"steel-shell","tw":1000,"time_s":600}`
	_, fout2 := doJSON(t, h, http.MethodPost, "/api/v1/forward", fwd2)
	if asFloat(t, fout2, "front_m") <= asFloat(t, fout, "front_m") {
		t.Fatal("deeper subcooling must advance further")
	}

	// 建档时非法物性拒绝。
	bad := `{"name":"bad","c":500,"k":40,"alpha":0,"lf":270000,"tf":1808,"tw":1200}`
	if code, out = doJSON(t, h, http.MethodPost, "/api/v1/profiles", bad); code != 422 {
		t.Fatalf("bad profile code=%d body=%v", code, out)
	}

	// 引用不存在的工况档。
	if code, out = doJSON(t, h, http.MethodPost, "/api/v1/forward", `{"profile":"ghost","time_s":1}`); code != 404 {
		t.Fatalf("missing profile code=%d body=%v", code, out)
	}

	// 更新与删除。
	upd := `{"name":"steel-shell","c":500,"k":40,"alpha":1.2e-5,"lf":270000,"tf":1808,"tw":1200}`
	if code, out = doJSON(t, h, http.MethodPut, "/api/v1/profiles/steel-shell", upd); code != 200 {
		t.Fatalf("update %d %v", code, out)
	}
	if code, _ = doJSON(t, h, http.MethodDelete, "/api/v1/profiles/steel-shell", ""); code != 200 {
		t.Fatalf("delete code=%d", code)
	}
	if code, _ = doJSON(t, h, http.MethodGet, "/api/v1/profiles/steel-shell", ""); code != 404 {
		t.Fatalf("get after delete code=%d", code)
	}
}

// TestBuiltinProfileUsable 内置冰层档凭名字即可算出厘米量级结果。
func TestBuiltinProfileUsable(t *testing.T) {
	h, _ := newTestApp(t)
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/forward",
		`{"profile":"ice-wall-minus30c","time_s":3600}`)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	s := asFloat(t, out, "front_m")
	if s < 0.01 || s > 0.10 {
		t.Fatalf("builtin ice front %.4f m not cm-scale", s)
	}
}

// TestSeries_HappyPath 时间序列作业完整返回。
func TestSeries_HappyPath(t *testing.T) {
	h, _ := newTestApp(t)
	body := `{"c":2100,"k":2.22,"alpha":1.15e-6,"lf":334000,"tf":273.15,"tw":243.15,
	          "times_s":[0,900,3600]}`
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/series", body)
	if code != http.StatusAccepted {
		t.Fatalf("submit %d %v", code, out)
	}
	id := out["id"].(string)

	// 轮询到 completed。
	var final map[string]any
	for i := 0; i < 200; i++ {
		_, cur := doJSON(t, h, http.MethodGet, "/api/v1/series/"+id, "")
		if cur["status"] == "completed" || cur["status"] == "failed" || cur["status"] == "canceled" {
			final = cur
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if final == nil {
		t.Fatal("series never finished")
	}
	if final["status"] != "completed" {
		t.Fatalf("status %v err %v", final["status"], final["error"])
	}
	pts := final["points"].([]any)
	if len(pts) != 3 {
		t.Fatalf("points %d", len(pts))
	}
	if pts[0].(map[string]any)["front_m"].(float64) != 0 {
		t.Fatal("t=0 point must be zero")
	}
	s900 := pts[1].(map[string]any)["front_m"].(float64)
	s3600 := pts[2].(map[string]any)["front_m"].(float64)
	if math.Abs(s3600/(2*s900)-1) > 1e-12 {
		t.Fatalf("series sqrt(t) broken: %v %v", s900, s3600)
	}
}

// TestSeries_Cancel 取消后状态 canceled 且绝不返回半列点。
func TestSeries_Cancel(t *testing.T) {
	h, _ := newTestApp(t)
	times := make([]float64, 500_000)
	raw, _ := json.Marshal(map[string]any{
		"c": 2100.0, "k": 2.22, "alpha": 1.15e-6, "lf": 334000.0,
		"tf": 273.15, "tw": 243.15, "times_s": times,
	})
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/series", string(raw))
	if code != 202 {
		t.Fatalf("%d %v", code, out)
	}
	id := out["id"].(string)

	// 等到 running/有进展再取消。
	for i := 0; i < 200; i++ {
		_, cur := doJSON(t, h, http.MethodGet, "/api/v1/series/"+id, "")
		if cur["progress"].(float64) > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	code, cout := doJSON(t, h, http.MethodPost, "/api/v1/series/"+id+"/cancel", "")
	if code != 200 {
		t.Fatalf("cancel %d", code)
	}
	var final map[string]any
	for i := 0; i < 200; i++ {
		_, cur := doJSON(t, h, http.MethodGet, "/api/v1/series/"+id, "")
		if cur["status"] == "canceled" || cur["status"] == "completed" {
			final = cur
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if final["status"] != "canceled" {
		t.Fatalf("status = %v", final["status"])
	}
	if _, present := final["points"]; present {
		t.Fatalf("canceled job must omit points, got %v", final["points"])
	}
	_ = cout
}

// TestSeries_Invalid 非法工况不创建作业。
func TestSeries_Invalid(t *testing.T) {
	h, _ := newTestApp(t)
	code, out := doJSON(t, h, http.MethodPost, "/api/v1/series",
		`{"c":2100,"k":2.22,"alpha":1.15e-6,"lf":334000,"tf":273.15,"tw":273.15,"times_s":[1,2]}`)
	if code != 422 || out["code"] != "wall_not_below_freeze" {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ = doJSON(t, h, http.MethodGet, "/api/v1/series/unknown", ""); code != 404 {
		t.Fatalf("unknown job code=%d", code)
	}
}

// TestForward_ParallelIsolation 并发不同温差请求，各自 λ/锋面严格对应自己的工况。
func TestForward_ParallelIsolation(t *testing.T) {
	h, _ := newTestApp(t)
	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tw := 273.15 - float64(1+i) // 每请求 1..24 K 过冷
			body := fmt.Sprintf(`{"c":2100,"k":2.22,"alpha":1.15e-6,"lf":334000,"tf":273.15,"tw":%g,"time_s":1800}`, tw)
			code, out := doJSON(t, h, http.MethodPost, "/api/v1/forward", body)
			if code != 200 {
				errs <- fmt.Errorf("req %d code %d", i, code)
				return
			}
			lam := out["root"].(map[string]any)["lambda"].(float64)
			front := asFloat(t, out, "front_m")
			want := 2 * lam * math.Sqrt(1.15e-6*1800)
			if math.Abs(front-want) > 1e-12 {
				errs <- fmt.Errorf("req %d front mismatch", i)
				return
			}
			// 温差越大锋面越深；这里只校验每个响应与自身 λ 自洽，
			// 单调性由顺序单测 TestDeeperWithSubcooling 钉死。
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
