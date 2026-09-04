package redis

import (
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"env-vault/internal/infrastructure/config"
)

func TestNewSentinelClient(t *testing.T) {
	client, err := New(config.RedisConfig{
		Enabled:          true,
		Mode:             config.RedisModeSentinel,
		Addrs:            []string{"127.0.0.1:26379"},
		MasterName:       "mymaster",
		Username:         "redis-user",
		Password:         "redis-password",
		SentinelUsername: "sentinel-user",
		SentinelPassword: "sentinel-password",
		Db:               2,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	redisClient, ok := client.(*goredis.Client)
	if !ok {
		t.Fatalf("New() client type = %T, want *redis.Client", client)
	}
	options := redisClient.Options()
	if options.Addr != "FailoverClient" || options.Username != "redis-user" || options.Password != "redis-password" || options.DB != 2 {
		t.Fatalf("unexpected sentinel data-node options: %+v", options)
	}
}

func TestNewSentinelClientRequiresMasterName(t *testing.T) {
	client, err := New(config.RedisConfig{
		Enabled: true,
		Mode:    config.RedisModeSentinel,
		Addrs:   []string{"127.0.0.1:26379"},
	})
	if err == nil {
		_ = client.Close()
		t.Fatal("New() error = nil, want missing master_name error")
	}
}
