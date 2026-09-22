// Package profile 实现按名字建档/取回的物性工况持久化。
//
// 存储为容器内单个 JSON 文件，写入走“临时文件 + rename”原子替换；
// 内存中的映射由互斥锁保护，HTTP 多请求并发读写安全。
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"stefan-service/internal/stefan"
)

// Profile 是一份命名工况档。
type Profile struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Params      stefan.Params `json:"params"`
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_\-.\p{Han}]{1,64}$`)

// ErrInvalidName 表示工况档名字不合法。
var ErrInvalidName = errors.New("工况名长度需在 1~64 之间，仅允许字母、数字、下划线、连字符、点与中文")

// ErrNotFound 表示名字下没有工况档。
var ErrNotFound = errors.New("工况档不存在")

// ErrExists 表示同名工况档已存在（创建场景）。
var ErrExists = errors.New("同名工况档已存在")

// Store 是工况档存储。
type Store struct {
	mu   sync.RWMutex
	path string
	data map[string]Profile
}

// NewStore 打开（不存在则创建）path 处的工况档文件。
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, data: map[string]Profile{}}
	buf, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(buf) > 0 {
			var list []Profile
			if err := json.Unmarshal(buf, &list); err != nil {
				return nil, fmt.Errorf("工况档文件损坏：%w", err)
			}
			for _, pr := range list {
				s.data[pr.Name] = pr
			}
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s, nil
}

// SeedDefaults 在存储为空时写入预置工况档。
func (s *Store) SeedDefaults() error {
	s.mu.Lock()
	empty := len(s.data) == 0
	s.mu.Unlock()
	if !empty {
		return nil
	}
	for _, pr := range DefaultProfiles() {
		if err := s.Put(pr, false); err != nil {
			return err
		}
	}
	return nil
}

// Put 建档或覆盖（overwrite=false 时重名报错）。物性在落盘前强校验。
func (s *Store) Put(pr Profile, overwrite bool) error {
	if !nameRe.MatchString(pr.Name) {
		return ErrInvalidName
	}
	if err := pr.Params.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[pr.Name]; ok && !overwrite {
		return ErrExists
	}
	s.data[pr.Name] = pr
	return s.flushLocked()
}

// Get 按名字取回工况档。
func (s *Store) Get(name string) (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pr, ok := s.data[name]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return pr, nil
}

// Delete 删除工况档；不存在返回 ErrNotFound。
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[name]; !ok {
		return ErrNotFound
	}
	delete(s.data, name)
	return s.flushLocked()
}

// List 返回按名字排序的全部工况档。
func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, 0, len(s.data))
	for _, pr := range s.data {
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// flushLocked 把内存映射原子写回磁盘；调用方需持写锁。
func (s *Store) flushLocked() error {
	list := make([]Profile, 0, len(s.data))
	for _, pr := range s.data {
		list = append(list, pr)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	buf, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".profiles-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}
