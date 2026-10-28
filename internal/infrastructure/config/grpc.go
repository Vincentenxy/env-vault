package config

import "strings"

// DefaultGRPCAddress 是未配置监听地址时使用的本地调试端口
const DefaultGRPCAddress = "127.0.0.1:9090"

// GRPCConfig 集中管理应用读取端口，当前阶段未接入应用认证
type GRPCConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Address string `mapstructure:"address"`
}

// ListenAddress 对手动组装配置和空地址提供与配置加载一致的默认值
func (c GRPCConfig) ListenAddress() string {
	if address := strings.TrimSpace(c.Address); address != "" {
		return address
	}
	return DefaultGRPCAddress
}
