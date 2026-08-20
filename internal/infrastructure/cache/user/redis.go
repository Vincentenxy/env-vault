package user

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	redislib "github.com/redis/go-redis/v9"

	userdomain "env-vault/internal/domain/user"
)

const userInfoKey = "user:info"

// RedisProfileCache 使用 Redis Hash 保存用户资料，field 为外部 userId。
type RedisProfileCache struct {
	client redislib.UniversalClient
	key    string
}

type cachedUser struct {
	ID       uuid.UUID `json:"id"`
	UserID   string    `json:"userId"`
	Nickname string    `json:"nickname"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Phone    string    `json:"phone"`
	TenantID uuid.UUID `json:"tenantId"`
	OrgID    uuid.UUID `json:"orgId"`
	CreateBy string    `json:"createBy"`
	UpdateBy string    `json:"updateBy"`
	CreateAt time.Time `json:"createAt"`
	UpdateAt time.Time `json:"updateAt"`
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
		ID:       user.ID,
		UserID:   user.UserID,
		Nickname: user.Nickname,
		Username: user.Username,
		Email:    user.Email,
		Phone:    user.Phone,
		TenantID: user.TenantID,
		OrgID:    user.OrgID,
		CreateBy: user.CreateBy,
		UpdateBy: user.UpdateBy,
		CreateAt: user.CreateAt,
		UpdateAt: user.UpdateAt,
	}
}

func (u cachedUser) toDomain() *userdomain.User {
	return &userdomain.User{
		ID:       u.ID,
		UserID:   u.UserID,
		Nickname: u.Nickname,
		Username: u.Username,
		Email:    u.Email,
		Phone:    u.Phone,
		TenantID: u.TenantID,
		OrgID:    u.OrgID,
		CreateBy: u.CreateBy,
		UpdateBy: u.UpdateBy,
		CreateAt: u.CreateAt,
		UpdateAt: u.UpdateAt,
	}
}
