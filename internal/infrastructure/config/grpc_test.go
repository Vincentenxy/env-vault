package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadGRPCDefaultsAndEnvironment 验证旧配置无需补充字段也能启动，环境变量可独立开启读取端口
func TestLoadGRPCDefaultsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 8090\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GRPC.Enabled || cfg.GRPC.ListenAddress() != "127.0.0.1:9090" {
		t.Fatalf("unexpected defaults: %+v", cfg.GRPC)
	}
	t.Setenv("GRPC_ENABLED", "true")
	t.Setenv("GRPC_ADDRESS", "0.0.0.0:19090")
	cfg, err = Load(dir)
	if err != nil || !cfg.GRPC.Enabled || cfg.GRPC.ListenAddress() != "0.0.0.0:19090" {
		t.Fatalf("environment override failed: %+v %v", cfg, err)
	}
}
