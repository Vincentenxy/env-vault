package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 全局配置结构体，字段采用小驼峰命名
type Config struct {
	Server ServerConfig `mapstructure:"server"`
	App    AppConfig    `mapstructure:"app"`
	Auth   AuthConfig   `mapstructure:"auth"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

// AppConfig 应用基础信息
type AppConfig struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
}

// AuthConfig 认证配置
type AuthConfig struct {
	JwtPublicKey string `mapstructure:"jwt-public-key"` // JWT 验签公钥（base64 DER 或 PEM 格式，RSA）
}

// Load 加载配置文件，path 为配置文件所在目录（如 ./configs）
// 支持通过环境变量覆盖，例如 SERVER_PORT=9090
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(path)

	// 环境变量覆盖：将 key 中的 "." 替换为 "_"
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}
