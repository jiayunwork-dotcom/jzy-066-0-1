package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"stefan-service/internal/profile"
)

func newTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	store, err := profile.NewStore(t.TempDir() + "/profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaults(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(store).Handler())
	return ts, ts.Close
}

func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, _ := http.NewRequest(method, ts.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		out = map[string]any{}
	}
	return resp.StatusCode, out
}

func TestHealthAndSeedProfile(t *testing.T) {
	ts, closeFn := newTestServer(t)
	defer closeFn()

	code, body := doJSON(t, ts, http.MethodGet, "/healthz", nil)
	if code != 200 || body["status"] != "ok" {
		t.Fatalf("healthz 异常：%d %v", code, body)
	}
	code, body = doJSON(t, ts, http.MethodGet, "/api/v1/profiles/ice-wall-minus20", nil)
	if code != 200 {
		t.Fatalf("预置冰层档取不到：%d %v", code, body)
	}
}

func TestForwardEndpoint(t *testing.T) {
	ts, closeFn := newTestServer(t)
	defer closeFn()

	// 引用预置档 + 1 小时。
	code, body := doJSON(t, ts, http.MethodPost, "/api/v1/stefan/forward", map[string]any{
		"profile": "ice-wall-minus20", "time": 3600,
	})
	if code != 200 {
		t.Fatalf("forward 失败：%d %v", code, body)
	}
	front := body["front"].(float64)
	if front < 0.02 || front > 0.05 {
		t.Errorf("冰层 1h 锋面 = %.4f m 不在厘米量级", front)
	}
	lam := body["lambda"].(map[string]any)
	if lam["rel_residual"].(float64) > 1e-9 {
		t.Errorf("精确分支相对残差过大：%v", lam["rel_residual"])
	}
	if body["wall_heat_flux"] == nil || body["wall_heat_flux"].(float64) <= 0 {
		t.Errorf("壁面热流应为正：%v", body["wall_heat_flux"])
	}
	ap := body["lambda_approx"].(map[string]any)
	if _, ok := ap["rel_residual"]; !ok {
		t.Errorf("对照近似分支必须随响应返回")
	}

	// t=0：锋面零、热流 null、附说明。
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/stefan/forward", map[string]any{
		"profile": "ice-wall-minus20", "time": 0,
	})
	if code != 200 {
		t.Fatalf("t=0 forward 失败：%d %v", code, body)
	}
	if body["front"].(float64) != 0 {
		t.Errorf("t=0 锋面应为 0")
	}
	if body["wall_heat_flux"] != nil {
		t.Errorf("t=0 热流应序列化为 null")
	}
}

func TestForwardValidationErrors(t *testing.T) {
	ts, closeFn := newTestServer(t)
	defer closeFn()

	// 内联非法物性：壁温不低于凝固点。
	code, body := doJSON(t, ts, http.MethodPost, "/api/v1/stefan/forward", map[string]any{
		"params": map[string]any{
			"c": 2100, "lf": 334000, "k": 2.22, "alpha": 1.15e-6,
			"tf": 0, "tw": 10,
		},
		"time": 100,
	})
	if code != http.StatusBadRequest || body["code"] != "invalid_parameter" || body["field"] != "tw" {
		t.Fatalf("期望 tw 字段校验错误，得到 %d %v", code, body)
	}

	// 热扩散率非正。
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/stefan/forward", map[string]any{
		"params": map[string]any{
			"c": 2100, "lf": 334000, "k": 2.22, "alpha": 0,
			"tf": 0, "tw": -10,
		},
		"time": 100,
	})
	if code != http.StatusBadRequest || body["field"] != "alpha" {
		t.Fatalf("期望 alpha 字段错误，得到 %d %v", code, body)
	}

	// 既给 profile 又给 params。
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/stefan/forward", map[string]any{
		"profile": "ice-wall-minus20",
		"params":  map[string]any{"c": 1, "lf": 1, "k": 1, "alpha": 1, "tf": 1, "tw": 0},
		"time":    1,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("互斥参数应 400，得到 %d %v", code, body)
	}

	// 不存在的档名。
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/stefan/forward", map[string]any{
		"profile": "nope", "time": 1,
	})
	if code != http.StatusNotFound || body["code"] != "profile_not_found" {
		t.Fatalf("不存在档应 404，得到 %d %v", code, body)
	}
}

func TestReverseEndpointRoundTrip(t *testing.T) {
	ts, closeFn := newTestServer(t)
	defer closeFn()

	code, body := doJSON(t, ts, http.MethodPost, "/api/v1/stefan/reverse", map[string]any{
		"profile": "ice-wall-minus20", "s": 0.05,
	})
	if code != 200 {
		t.Fatalf("reverse 失败：%d %v", code, body)
	}
	tm := body["time"].(float64)

	// 用反求时间正算，应回到 5cm。
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/stefan/forward", map[string]any{
		"profile": "ice-wall-minus20", "time": tm,
	})
	if code != 200 {
		t.Fatalf("forward 失败：%d %v", code, body)
	}
	front := body["front"].(float64)
	if d := (front - 0.05) / 0.05; d > 1e-9 || d < -1e-9 {
		t.Errorf("反求往返厚度偏差 %.2e", d)
	}

	// 非法厚度。
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/stefan/reverse", map[string]any{
		"profile": "ice-wall-minus20", "s": 0,
	})
	if code != http.StatusBadRequest || body["code"] != "invalid_thickness" {
		t.Fatalf("s=0 应被拒：%d %v", code, body)
	}
}

func TestProfileCRUDOverHTTP(t *testing.T) {
	ts, closeFn := newTestServer(t)
	defer closeFn()

	code, body := doJSON(t, ts, http.MethodPost, "/api/v1/profiles", map[string]any{
		"name":        "custom-ice",
		"description": "自定义",
		"params": map[string]any{
			"c": 2100, "lf": 334000, "k": 2.22, "alpha": 1.15e-6,
			"tf": 0, "tw": -30,
		},
	})
	if code != http.StatusCreated {
		t.Fatalf("建档失败：%d %v", code, body)
	}
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/profiles", map[string]any{
		"name": "custom-ice",
		"params": map[string]any{
			"c": 2100, "lf": 334000, "k": 2.22, "alpha": 1.15e-6,
			"tf": 0, "tw": -30,
		},
	})
	if code != http.StatusConflict {
		t.Fatalf("重名应 409：%d", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/profiles/custom-ice", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除应 204，得到 %d", resp.StatusCode)
	}
}

func TestJobLifecycle(t *testing.T) {
	ts, closeFn := newTestServer(t)
	defer closeFn()

	// 正常作业。
	code, body := doJSON(t, ts, http.MethodPost, "/api/v1/jobs", map[string]any{
		"profile": "ice-wall-minus20", "t0": 0, "t1": 3600, "intervals": 4,
	})
	if code != http.StatusAccepted {
		t.Fatalf("启动作业失败：%d %v", code, body)
	}
	id := body["id"].(string)

	var final map[string]any
	for i := 0; i < 200; i++ {
		code, final = doJSON(t, ts, http.MethodGet, "/api/v1/jobs/"+id, nil)
		if code != 200 {
			t.Fatalf("查询作业失败：%d", code)
		}
		if final["status"] == "completed" {
			break
		}
	}
	if final["status"] != "completed" {
		t.Fatalf("作业未完成：%v", final["status"])
	}
	if final["count"].(float64) != 5 {
		t.Errorf("应返回 5 个采样点，得到 %v", final["count"])
	}

	// 大型作业取消。
	code, body = doJSON(t, ts, http.MethodPost, "/api/v1/jobs", map[string]any{
		"profile": "ice-wall-minus20", "t0": 0, "t1": 86400, "intervals": 500000000,
	})
	if code != http.StatusAccepted {
		t.Fatalf("启动大作业失败：%d %v", code, body)
	}
	bigID := body["id"].(string)
	code, _ = doJSON(t, ts, http.MethodPost, "/api/v1/jobs/"+bigID+"/cancel", nil)
	if code != http.StatusOK {
		t.Fatalf("取消请求失败：%d", code)
	}
	var st map[string]any
	for i := 0; i < 200; i++ {
		_, st = doJSON(t, ts, http.MethodGet, "/api/v1/jobs/"+bigID, nil)
		if st["status"] == "canceled" {
			break
		}
	}
	if st["status"] != "canceled" {
		t.Fatalf("作业未进入 canceled：%v", st["status"])
	}
	if _, leaked := st["points"]; leaked {
		t.Fatalf("取消作业泄露了半成品点列")
	}
	if st["error"] == nil || st["error"] == "" {
		t.Fatalf("取消态应带原因说明")
	}
}
