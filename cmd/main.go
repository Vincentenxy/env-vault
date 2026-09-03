package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"env-vault/internal/infrastructure/config"
	"env-vault/internal/infrastructure/postgres"
	"env-vault/internal/infrastructure/redis"
	"env-vault/internal/interfaces/router"
	"env-vault/pkg/logger"

	"go.uber.org/zap"
)

func main() {
	// 加载配置
	cfg, err := config.Load("./configs")
	if err != nil {
		logger.L().Fatal("load config failed", zap.Error(err))
	}

	// 初始化全局日志（全局只允许使用 pkg/logger）
	logger.Init(cfg.Server.Mode)
	defer logger.Sync()

	// 初始化 PostgreSQL
	db, err := postgres.New(cfg.Database)
	if err != nil {
		logger.L().Fatal("init postgres failed", zap.Error(err))
	}

	// 初始化 Redis（enabled=false 时返回 nil，不启用缓存）
	redisClient, err := redis.New(cfg.Redis)
	if err != nil {
		logger.L().Fatal("init redis failed", zap.Error(err))
	}
	if redisClient != nil {
		defer func() { _ = redisClient.Close() }()
		logger.L().Info("redis connected",
			zap.String("mode", cfg.Redis.Mode),
			zap.Strings("addrs", cfg.Redis.Addrs),
		)
	}

	// 应用生命周期用于统一取消 HTTP 服务和后台恢复任务
	appCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 初始化路由
	r, err := router.New(appCtx, cfg, db, redisClient)
	if err != nil {
		logger.L().Fatal("init router failed", zap.Error(err))
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: r,
	}

	// 异步启动 HTTP 服务
	go func() {
		logger.L().Info("server started",
			zap.String("name", cfg.App.Name),
			zap.String("version", cfg.App.Version),
			zap.String("addr", srv.Addr),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.L().Fatal("server listen failed", zap.Error(err))
		}
	}()

	// 优雅退出
	<-appCtx.Done()
	logger.L().Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.L().Fatal("server shutdown failed", zap.Error(err))
	}
	logger.L().Info("server exited")
}
