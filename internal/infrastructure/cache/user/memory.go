// Package user 提供用户资料 Redis 缓存与进程内姓名缓存。
package user

import (
	"sync"

	userdomain "env-vault/internal/domain/user"
)

// MemoryNameCache 并发安全的进程内用户姓名缓存。
type MemoryNameCache struct {
	mu    sync.RWMutex
	names map[string]string
}

// NewMemoryNameCache 创建用户姓名缓存。
func NewMemoryNameCache() *MemoryNameCache {
	return &MemoryNameCache{names: make(map[string]string)}
}

// Get 按外部用户 ID 查询姓名。
func (c *MemoryNameCache) Get(userID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	name, ok := c.names[userID]
	return name, ok
}

// Set 写入单个用户姓名。
func (c *MemoryNameCache) Set(userID, nickname string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names[userID] = nickname
}

// Replace 使用数据库快照整体替换姓名缓存。
func (c *MemoryNameCache) Replace(users []*userdomain.User) {
	names := make(map[string]string, len(users))
	for _, user := range users {
		names[user.UserID] = user.Nickname
	}

	c.mu.Lock()
	c.names = names
	c.mu.Unlock()
}
