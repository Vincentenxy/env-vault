package user

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	redislib "github.com/redis/go-redis/v9"

	userdomain "env-vault/internal/domain/user"
)

const (
	userInfoKey        = "user:info"
	userBlockStatusKey = "user:blocked"
)

// RedisProfileCache 使用 Redis Hash 保存用户资料，field 为外部 userId。
type RedisProfileCache struct {
	client redislib.UniversalClient
	key    string
}

type cachedUser struct {
	ID        uuid.UUID `json:"id"`
	UserID    string    `json:"userId"`
	Nickname  string    `json:"nickname"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	TenantID  uuid.UUID `json:"tenantId"`
	OrgID     uuid.UUID `json:"orgId"`
	IsBlocked bool      `json:"isBlocked"`
	CreateBy  string    `json:"createBy"`
	UpdateBy  string    `json:"updateBy"`
	CreateAt  time.Time `json:"createAt"`
	UpdateAt  time.Time `json:"updateAt"`
}

// RedisBlockStatusCache 使用独立 Hash 保存用户锁定状态，避免认证时读取完整资料。
type RedisBlockStatusCache struct {
	client redislib.UniversalClient
	key    string
}

// NewRedisProfileCache 创建用户 Redis 缓存。client 为 nil 时缓存自动降级为空实现。
func NewRedisProfileCache(client redislib.UniversalClient, keyPrefix string) *RedisProfileCache {
	prefix := strings.Trim(strings.TrimSpace(keyPrefix), ":")
	key := userInfoKey
	if prefix != "" {
		key = prefix + ":" + userInfoKey
	}
	return &RedisProfileCache{client: client, key: key}
}

// NewRedisBlockStatusCache 创建用户锁定状态缓存。
func NewRedisBlockStatusCache(client redislib.UniversalClient, keyPrefix string) *RedisBlockStatusCache {
	prefix := strings.Trim(strings.TrimSpace(keyPrefix), ":")
	key := userBlockStatusKey
	if prefix != "" {
		key = prefix + ":" + userBlockStatusKey
	}
	return &RedisBlockStatusCache{client: client, key: key}
}

// Get 查询用户资料，缓存未命中返回 nil, nil。
func (c *RedisProfileCache) Get(ctx context.Context, userID string) (*userdomain.User, error) {
	if c.client == nil {
		return nil, nil
	}
	value, err := c.client.HGet(ctx, c.key, userID).Result()
	if err == redislib.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var cached cachedUser
	if err := json.Unmarshal([]byte(value), &cached); err != nil {
		return nil, err
	}
	return cached.toDomain(), nil
}

// Set 写入单个用户资料。
func (c *RedisProfileCache) Set(ctx context.Context, user *userdomain.User) error {
	if c.client == nil {
		return nil
	}
	value, err := json.Marshal(toCachedUser(user))
	if err != nil {
		return err
	}
	return c.client.HSet(ctx, c.key, user.UserID, value).Err()
}

// Replace 使用数据库快照整体替换 Redis 用户资料。
func (c *RedisProfileCache) Replace(ctx context.Context, users []*userdomain.User) error {
	if c.client == nil {
		return nil
	}

	values := make(map[string]any, len(users))
	for _, user := range users {
		value, err := json.Marshal(toCachedUser(user))
		if err != nil {
			return err
		}
		values[user.UserID] = value
	}

	pipe := c.client.TxPipeline()
	pipe.Del(ctx, c.key)
	if len(values) > 0 {
		pipe.HSet(ctx, c.key, values)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func toCachedUser(user *userdomain.User) cachedUser {
	return cachedUser{
		ID:        user.ID,
		UserID:    user.UserID,
		Nickname:  user.Nickname,
		Username:  user.Username,
		Email:     user.Email,
		Phone:     user.Phone,
		TenantID:  user.TenantID,
		OrgID:     user.OrgID,
		IsBlocked: user.IsBlocked,
		CreateBy:  user.CreateBy,
		UpdateBy:  user.UpdateBy,
		CreateAt:  user.CreateAt,
		UpdateAt:  user.UpdateAt,
	}
}

func (u cachedUser) toDomain() *userdomain.User {
	return &userdomain.User{
		ID:        u.ID,
		UserID:    u.UserID,
		Nickname:  u.Nickname,
		Username:  u.Username,
		Email:     u.Email,
		Phone:     u.Phone,
		TenantID:  u.TenantID,
		OrgID:     u.OrgID,
		IsBlocked: u.IsBlocked,
		CreateBy:  u.CreateBy,
		UpdateBy:  u.UpdateBy,
		CreateAt:  u.CreateAt,
		UpdateAt:  u.UpdateAt,
	}
}

// Get 查询用户锁定状态。Redis 中没有该用户时 found=false。
func (c *RedisBlockStatusCache) Get(ctx context.Context, userID string) (bool, bool, error) {
	if c.client == nil {
		return false, false, nil
	}
	value, err := c.client.HGet(ctx, c.key, userID).Result()
	if err == redislib.Nil {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	blocked, err := strconv.ParseBool(value)
	if err != nil {
		return false, false, err
	}
	return blocked, true, nil
}

// Set 写入单个用户锁定状态，为后续锁定更新接口预留。
func (c *RedisBlockStatusCache) Set(ctx context.Context, userID string, blocked bool) error {
	if c.client == nil {
		return nil
	}
	return c.client.HSet(ctx, c.key, userID, strconv.FormatBool(blocked)).Err()
}

// Replace 使用数据库快照整体替换 Redis 用户锁定状态。
func (c *RedisBlockStatusCache) Replace(ctx context.Context, users []*userdomain.User) error {
	if c.client == nil {
		return nil
	}
	values := make(map[string]any, len(users))
	for _, user := range users {
		values[user.UserID] = strconv.FormatBool(user.IsBlocked)
	}
	pipe := c.client.TxPipeline()
	pipe.Del(ctx, c.key)
	if len(values) > 0 {
		pipe.HSet(ctx, c.key, values)
	}
	_, err := pipe.Exec(ctx)
	return err
}
