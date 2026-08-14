// Package redis 提供 Redis 客户端初始化能力（go-redis 封装）。
// 根据配置 mode 支持单机（single）与集群（cluster）两种模式，
// 统一返回 redis.UniversalClient 接口，上层无需关心具体实现。
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"env-vault/internal/infrastructure/config"
)

// New 按配置初始化 Redis 客户端
//   - enabled=false：返回 (nil, nil)，表示未启用 Redis
//   - mode=single：单机客户端，支持 db 选择
//   - mode=cluster：集群客户端（Redis Cluster 不支持分库，忽略 db 配置）
func New(cfg config.RedisConfig) (redis.UniversalClient, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("redis addrs is empty")
	}

	var client redis.UniversalClient
	switch cfg.Mode {
	case "", config.RedisModeSingle:
		client = redis.NewClient(&redis.Options{
			Addr:         cfg.Addrs[0],
			Password:     cfg.Password,
			DB:           cfg.Db,
			PoolSize:     cfg.PoolSize,
			MinIdleConns: cfg.MinIdleConns,
			MaxRetries:   cfg.MaxRetries,
			DialTimeout:  cfg.DialTimeout,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		})
	case config.RedisModeCluster:
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        cfg.Addrs,
			Password:     cfg.Password,
			PoolSize:     cfg.PoolSize,
			MinIdleConns: cfg.MinIdleConns,
			MaxRetries:   cfg.MaxRetries,
			DialTimeout:  cfg.DialTimeout,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		})
	default:
		return nil, fmt.Errorf("unsupported redis mode: %q", cfg.Mode)
	}

	// 启动预热：立即建立连接并验证可用性，失败则初始化直接报错
	if cfg.WarmUpOnStart {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("ping redis: %w", err)
		}
	}

	return client, nil
}
