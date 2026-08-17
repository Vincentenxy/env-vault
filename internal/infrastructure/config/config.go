package config

import (
	"fmt"
	"strings"
	"time"

	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Config 全局配置结构体，yaml key 使用 viper 推荐的下划线命名
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	App      AppConfig      `mapstructure:"app"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Security SecurityConfig `mapstructure:"security"`
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
	JwtPublicKey string `mapstructure:"jwt_public_key"` // JWT 验签公钥（base64 DER 或 PEM 格式，RSA）
}

// SecurityConfig 安全相关配置
type SecurityConfig struct {
	EncryptionKey string `mapstructure:"encryption_key"` // secret value 加密私钥（AES-256-GCM，32 字节 base64 编码）
}

// DatabaseConfig PostgreSQL 配置
type DatabaseConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	Name            string        `mapstructure:"name"`
	SslMode         string        `mapstructure:"ssl_mode"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"` // 如 30m
	ConnectTimeout  time.Duration `mapstructure:"connect_timeout"`   // 如 5s
}

// Redis 运行模式
const (
	RedisModeSingle  = "single"
	RedisModeCluster = "cluster"
)

// RedisConfig Redis 配置，mode 决定使用单机还是集群客户端
type RedisConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	Mode          string        `mapstructure:"mode"`  // single / cluster
	Addrs         []string      `mapstructure:"addrs"` // 单机填一个地址，集群填多个节点地址
	Password      string        `mapstructure:"password"`
	Db            int           `mapstructure:"db"`         // 仅单机模式生效（集群不支持分库）
	KeyPrefix     string        `mapstructure:"key_prefix"` // 业务 key 统一前缀，供缓存封装使用
	WarmUpOnStart bool          `mapstructure:"warm_up_on_start"`
	PoolSize      int           `mapstructure:"pool_size"`
	MinIdleConns  int           `mapstructure:"min_idle_conns"`
	MaxRetries    int           `mapstructure:"max_retries"`
	DialTimeout   time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout   time.Duration `mapstructure:"read_timeout"`
	WriteTimeout  time.Duration `mapstructure:"write_timeout"`
}

// Load 加载配置文件，path 为配置文件所在目录（如 ./configs）
// 支持通过环境变量覆盖，例如 SERVER_PORT=9090、DATABASE_HOST=127.0.0.1
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
	// viper 默认解码不支持 "30m" 之类的时长字符串，追加 StringToTimeDuration 解码钩子
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.TextUnmarshallerHookFunc(),
	))); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}
