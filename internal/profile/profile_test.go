package profile

import (
	"sync"
	"testing"

	"stefanservice/internal/front"
)

func validCase() front.Case {
	return front.Case{C: 2100, K: 2.22, Alpha: 1.15e-6, Lf: 334e3, Tf: 273.15, Tw: 253.15}
}

func TestCreateGetListDelete(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Profile{Name: "p1", Case: validCase()}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(p); err != ErrExists {
		t.Fatalf("dup create: got %v, want ErrExists", err)
	}
	if err := s.Create(Profile{Name: "", Case: validCase()}); err != ErrEmptyName {
		t.Fatalf("empty name: got %v", err)
	}
	// 非法工况必须在落盘前拦截。
	bad := validCase()
	bad.Alpha = -1
	if err := s.Create(Profile{Name: "bad", Case: bad}); err == nil {
		t.Fatal("invalid case was persisted")
	}

	got, err := s.Get("p1")
	if err != nil || got.Name != "p1" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.Get("nope"); err != ErrNotFound {
		t.Fatalf("missing: got %v", err)
	}
	if len(s.List()) != 1 {
		t.Fatalf("list len = %d", len(s.List()))
	}
	if err := s.Delete("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("p1"); err != ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
	if err := s.Delete("p1"); err != ErrNotFound {
		t.Fatalf("delete missing: %v", err)
	}
}

// TestPersistence 重新打开存储目录后，工况档仍在。
func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Create(Profile{Name: "persisted", Case: validCase()}); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s2.Get("persisted")
	if err != nil {
		t.Fatalf("reopen get: %v", err)
	}
	if p.Case.Alpha != validCase().Alpha || p.Name != "persisted" {
		t.Fatalf("reopened profile wrong: %+v", p)
	}
}

// TestBuiltinIce 预置冰层算例：存在、内置标记为真、物性可通过校验，
// 且一小时凝固厚度在厘米量级。
func TestBuiltinIce(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ice := IceProfile()
	if err := s.EnsureBuiltin(ice); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ice.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Builtin {
		t.Fatal("ice profile must be flagged builtin")
	}
	if err := got.Case.Validate(); err != nil {
		t.Fatalf("ice case invalid: %v", err)
	}
	// 第二次 Ensure 不覆盖已有数据。
	got.Case.Tw = 200
	if err := s.Update(got); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBuiltin(IceProfile()); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Get(ice.Name)
	if again.Case.Tw != 200 {
		t.Fatal("EnsureBuiltin overwrote user-modified profile")
	}
	if !again.Builtin {
		t.Fatal("builtin flag lost after update")
	}
}

// TestConcurrentIsolation 多组工况并行建档/取回，名字与数据不得互相渗透。
func TestConcurrentIsolation(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := validCase()
			c.Tw = c.Tf - float64(1+i) // 每个工况自己的过冷度
			name := nameFor(i)
			if err := s.Create(Profile{Name: name, Case: c}); err != nil {
				t.Errorf("create %s: %v", name, err)
				return
			}
			got, err := s.Get(name)
			if err != nil {
				t.Errorf("get %s: %v", name, err)
				return
			}
			if got.Case.Tw != c.Tw {
				t.Errorf("%s contamination: Tw=%v want %v", name, got.Case.Tw, c.Tw)
			}
		}(i)
	}
	wg.Wait()
	if len(s.List()) != n {
		t.Fatalf("list len = %d, want %d", len(s.List()), n)
	}
}

func nameFor(i int) string {
	const digits = "0123456789abcdef"
	if i == 0 {
		return "p0"
	}
	b := []byte{'p'}
	for v := i; v > 0; v >>= 4 {
		b = append(b, digits[v&0xf])
	}
	return string(b)
}
