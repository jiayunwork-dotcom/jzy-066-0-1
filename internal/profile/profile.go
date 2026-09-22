// Package profile 实现按名字建档的工况持久化：内存中受互斥锁保护的
// 注册表 + 容器内 JSON 文件落盘（写临时文件后原子 rename）。
//
// 服务启动时预置一组“冰层”算例（壁温远低于零度，一小时凝固厚度在
// 厘米量级），拉起即可核对。
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"stefanservice/internal/front"
)

var (
	ErrNotFound  = errors.New("profile: 工况档不存在")
	ErrExists    = errors.New("profile: 同名工况档已存在")
	ErrEmptyName = errors.New("profile: 工况名不能为空")
)

// Profile 是一个命名工况档。
type Profile struct {
	Name      string     `json:"name"`
	Case      front.Case `json:"case"`
	CreatedAt string     `json:"created_at"`
	Builtin   bool       `json:"builtin"`
}

// Store 是工况档的持久化注册表。
type Store struct {
	mu       sync.RWMutex
	dir      string
	profiles map[string]Profile
}

// NewStore 打开（必要时创建）dir 下的持久化存储并载入已有工况档。
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("profile: 创建存储目录失败: %w", err)
	}
	s := &Store{dir: dir, profiles: make(map[string]Profile)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) path(name string) string {
	return filepath.Join(s.dir, name+".json")
}

// load 从目录中读回全部工况档（每个文件独立，损坏文件不阻断其余档）。
func (s *Store) load() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("profile: 读取存储目录失败: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var p Profile
		if err := json.Unmarshal(data, &p); err != nil || p.Name == "" {
			continue
		}
		s.profiles[p.Name] = p
	}
	return nil
}

// persistLocked 必须在持有写锁时调用：先写临时文件再原子改名。
func (s *Store) persistLocked(p Profile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	final := s.path(p.Name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func (s *Store) deleteFileLocked(name string) { _ = os.Remove(s.path(name)) }

// Create 新建工况档；同名已存在返回 ErrExists；物性在落盘前严格校验。
func (s *Store) Create(p Profile) error {
	if p.Name == "" {
		return ErrEmptyName
	}
	if err := p.Case.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[p.Name]; ok {
		return ErrExists
	}
	if err := s.persistLocked(p); err != nil {
		return err
	}
	s.profiles[p.Name] = p
	return nil
}

// Update 覆盖已有工况档（用于重算前修改物性）。
func (s *Store) Update(p Profile) error {
	if p.Name == "" {
		return ErrEmptyName
	}
	if err := p.Case.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.profiles[p.Name]; ok {
		p.Builtin = old.Builtin // 内置标记不允许通过更新抹掉
	}
	if err := s.persistLocked(p); err != nil {
		return err
	}
	s.profiles[p.Name] = p
	return nil
}

// Get 凭名字取回工况档；不存在返回 ErrNotFound。
// 返回副本，调用方的修改不会渗透到注册表。
func (s *Store) Get(name string) (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[name]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return p, nil
}

// Delete 删除工况档。
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[name]; !ok {
		return ErrNotFound
	}
	delete(s.profiles, name)
	s.deleteFileLocked(name)
	return nil
}

// List 按名字排序返回全部工况档副本。
func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EnsureBuiltin 预置内置工况档（已存在则保留用户数据，不覆盖）。
func (s *Store) EnsureBuiltin(p Profile) error {
	p.Builtin = true
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[p.Name]; ok {
		return nil
	}
	if err := s.persistLocked(p); err != nil {
		return err
	}
	s.profiles[p.Name] = p
	return nil
}

// IceProfile 是预置冰层算例：壁温 -30 ℃，一小时凝固厚度约 3.8 cm。
//
// 物性（常压附近的冰，SI 单位）：
//
//	c=2100 J/(kg·K), k=2.22 W/(m·K), α=1.15e-6 m²/s, Lf=334 kJ/kg。
func IceProfile() Profile {
	return Profile{
		Name:    "ice-wall-minus30c",
		Builtin: true,
		Case: front.Case{
			C:     2100,
			K:     2.22,
			Alpha: 1.15e-6,
			Lf:    334e3,
			Tf:    273.15, // 0 ℃
			Tw:    243.15, // -30 ℃
		},
	}
}
