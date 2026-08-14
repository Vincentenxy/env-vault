// Package postgres 提供 PostgreSQL 连接初始化能力（GORM 封装）。
package postgres

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"env-vault/internal/infrastructure/config"
)

// New 按配置初始化 PostgreSQL 连接池
func New(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("database host is empty")
	}

	// 由分离字段拼接 DSN（keyword/value 形式，password 自动转义）
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SslMode)
	if cfg.ConnectTimeout > 0 {
		dsn += fmt.Sprintf(" connect_timeout=%d", int(cfg.ConnectTimeout.Seconds()))
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		// 项目统一通过代码层面显式记录 SQL 日志，GORM 日志降级为 Warn
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}
