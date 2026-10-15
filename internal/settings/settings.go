// Package settings 保存运行设置与角色分配，文件在 ~/.autocoder/settings.json。
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/luckySpro/AutoCoder/internal/roles"
)

// 并发上限（DESIGN §11：默认 3，最多 5）。
const (
	DefaultConcurrency = 3
	MaxConcurrency     = 5
)

// Settings 是界面"角色与运行"页可改的全部内容。
type Settings struct {
	Concurrency    int  `json:"concurrency"`
	AutoMerge      bool `json:"autoMerge"`
	MaxAttempts    int  `json:"maxAttempts"`
	IdleMinutes    int  `json:"idleMinutes"`    // 连续无输出、无文件变化多久判定为卡住
	TimeoutMinutes int  `json:"timeoutMinutes"` // 单次尝试最长时间
	Paused         bool `json:"paused"`         // 暂停派发：不再启动新任务，运行中的跑完
	// Assignments 为空表示使用推荐分配。
	Assignments []roles.Assignment `json:"assignments"`
}

// Default 返回默认设置。
func Default() Settings {
	return Settings{Concurrency: DefaultConcurrency, AutoMerge: true, MaxAttempts: 3, IdleMinutes: 10, TimeoutMinutes: 45}
}

// Normalize 把越界的值收回合法范围。
func (s Settings) Normalize() Settings {
	d := Default()
	if s.Concurrency < 1 {
		s.Concurrency = d.Concurrency
	}
	s.Concurrency = min(s.Concurrency, MaxConcurrency)
	if s.MaxAttempts < 1 || s.MaxAttempts > 10 {
		s.MaxAttempts = d.MaxAttempts
	}
	if s.IdleMinutes < 1 || s.IdleMinutes > 120 {
		s.IdleMinutes = d.IdleMinutes
	}
	if s.TimeoutMinutes < 5 || s.TimeoutMinutes > 480 {
		s.TimeoutMinutes = d.TimeoutMinutes
	}
	return s
}

// File 是带锁的设置文件。
type File struct {
	Path string
	mu   sync.Mutex
	cur  *Settings
}

// Get 返回当前设置；文件不存在时返回默认值。
func (f *File) Get() Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur != nil {
		return *f.cur
	}
	s := Default()
	if data, err := os.ReadFile(f.Path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	s = s.Normalize()
	f.cur = &s
	return s
}

// Put 校验并保存。
func (f *File) Put(s Settings) (Settings, error) {
	if s.Concurrency > MaxConcurrency {
		return s, errors.New("同时运行的工具最多 5 个")
	}
	for _, a := range s.Assignments {
		if a.ToolID == "" {
			return s, errors.New("角色分配缺少工具")
		}
	}
	s = s.Normalize()
	f.mu.Lock()
	defer f.mu.Unlock()
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return s, err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return s, err
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		return s, err
	}
	f.cur = &s
	return s, nil
}
