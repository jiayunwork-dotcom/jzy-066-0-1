package profile

import (
	"errors"
	"path/filepath"
	"testing"

	"stefan-service/internal/stefan"
)

func ice() stefan.Params {
	return stefan.Params{C: 2100, Lf: 334e3, K: 2.22, Alpha: 1.15e-6, Tf: 0, Tw: -20}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestPutGetDeleteRoundTrip 建档、凭名取回、覆盖拦截、删除全链路。
func TestPutGetDeleteRoundTrip(t *testing.T) {
	store := newStore(t)
	pr := Profile{Name: "case-a", Description: "d", Params: ice()}
	if err := store.Put(pr, false); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("case-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Params.Alpha != ice().Alpha {
		t.Errorf("取回物性与建档不符：%+v", got.Params)
	}
	if err := store.Put(pr, false); !errors.Is(err, ErrExists) {
		t.Errorf("重名创建应返回 ErrExists，得到 %v", err)
	}
	if err := store.Put(pr, true); err != nil {
		t.Errorf("显式覆盖应成功：%v", err)
	}
	if _, err := store.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("缺失档应返回 ErrNotFound，得到 %v", err)
	}
	if err := store.Delete("case-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("case-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后仍可取回")
	}
	if err := store.Delete("case-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除不存在档应返回 ErrNotFound")
	}
}

// TestPersistenceAcrossReopen 落盘后重新打开，档仍在。
func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "profiles.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(Profile{Name: "冰", Params: ice()}, false); err != nil {
		t.Fatal(err)
	}
	store2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store2.Get("冰"); err != nil {
		t.Fatalf("重开后工况档丢失：%v", err)
	}
}

func TestRejectInvalid(t *testing.T) {
	store := newStore(t)
	if err := store.Put(Profile{Name: "bad/name", Params: ice()}, false); !errors.Is(err, ErrInvalidName) {
		t.Errorf("非法名字应拒绝，得到 %v", err)
	}
	bad := ice()
	bad.Tw = 1
	if err := store.Put(Profile{Name: "bad-params", Params: bad}, false); err == nil {
		t.Errorf("壁温高于凝固点的档应被拒绝")
	}
}

// TestSeedDefaults 预置冰层算例存在且厘米量级自洽。
func TestSeedDefaults(t *testing.T) {
	store := newStore(t)
	if err := store.SeedDefaults(); err != nil {
		t.Fatal(err)
	}
	defs := DefaultProfiles()
	if len(defs) != 1 || defs[0].Name != "ice-wall-minus20" {
		t.Fatalf("预置档异常：%+v", defs)
	}
	pr, err := store.Get("ice-wall-minus20")
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.Params.Validate(); err != nil {
		t.Fatalf("预置档物性不合法：%v", err)
	}
	// 幂等：再次 seed 不覆盖已有内容。
	pr.Description = "changed"
	_ = store.Put(pr, true)
	if err := store.SeedDefaults(); err != nil {
		t.Fatal(err)
	}
	again, _ := store.Get("ice-wall-minus20")
	if again.Description != "changed" {
		t.Errorf("SeedDefaults 不应覆盖已有档")
	}
}
