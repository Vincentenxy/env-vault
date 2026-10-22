package redis

import (
	"context"
	"net"
	"testing"
	"time"

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

func TestClientHonorsRequestDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Accept the connection but never answer Redis commands.
		<-stop
	}()
	defer func() {
		close(stop)
		_ = listener.Close()
		<-serverDone
	}()
	client, err := New(config.RedisConfig{
		Enabled: true, Mode: config.RedisModeSingle, Addrs: []string{listener.Addr().String()},
		MaxRetries: -1, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := client.Ping(ctx).Err(); err == nil {
		t.Fatal("unresponsive Redis must fail")
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("request deadline ignored: waited %v for Redis", elapsed)
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
