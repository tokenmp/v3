// Package settings 提供用户设置的内存存储。
//
// 用户设置（偏好计费方式、降级开关）当前无独立持久化层，先用进程内内存 map。
// 默认值：preferredBilling="coding"，fallbackEnabled=false。生产化后可替换为
// Auth DB 新表或独立 settings 服务，调用方接口不变。
package settings

import "sync"

// 默认值。新用户或未配置字段返回这些值。
const (
	DefaultPreferredBilling = "coding"
	DefaultFallbackEnabled  = false
)

// Settings 是单个用户的设置快照。
type Settings struct {
	PreferredBilling string
	FallbackEnabled  bool
}

// Store 是并发安全的内存用户设置存储。
type Store struct {
	mu sync.RWMutex
	m  map[string]Settings
}

// NewStore 创建内存设置存储。设置默认按用户返回默认值。
func NewStore() *Store {
	return &Store{m: make(map[string]Settings)}
}

// Get 返回用户设置，缺失时返回默认值（永不返回零值）。
func (s *Store) Get(userID string) Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.m[userID]; ok {
		return v
	}
	return Settings{PreferredBilling: DefaultPreferredBilling, FallbackEnabled: DefaultFallbackEnabled}
}

// Snapshot 用显式可选字段更新设置，支持把 FallbackEnabled 显式设为 false
// （bool 无法区分「未设置」与「设为 false」，故用指针表达可选意图）。
// preferredBilling 为 nil 或空串时不更新；fallbackEnabled 为 nil 时不更新。
func (s *Store) Snapshot(userID string, preferredBilling *string, fallbackEnabled *bool) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.m[userID]
	if !ok {
		cur = Settings{PreferredBilling: DefaultPreferredBilling, FallbackEnabled: DefaultFallbackEnabled}
	}
	if preferredBilling != nil && *preferredBilling != "" {
		cur.PreferredBilling = *preferredBilling
	}
	if fallbackEnabled != nil {
		cur.FallbackEnabled = *fallbackEnabled
	}
	s.m[userID] = cur
	return cur
}
