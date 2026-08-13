package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"env-vault/internal/infrastructure/config"
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

	// 初始化路由
	r, err := router.New(cfg)
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
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.L().Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.L().Fatal("server shutdown failed", zap.Error(err))
	}
	logger.L().Info("server exited")
}
