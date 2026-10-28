package main

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	"google.golang.org/grpc"
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

	// HTTP 与 gRPC 共享一次依赖组装，避免生成两套主密钥状态
	application, err := router.NewApplication(appCtx, cfg, db, redisClient)
	if err != nil {
		logger.L().Fatal("init router failed", zap.Error(err))
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: application.HTTP,
	}
	if application.GRPC != nil {
		listener, err := net.Listen("tcp", cfg.GRPC.ListenAddress())
		if err != nil {
			logger.L().Fatal("gRPC listen failed", zap.Error(err))
		}
		go func() {
			logger.L().Info("gRPC server started", zap.String("addr", listener.Addr().String()))
			if err := application.GRPC.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				logger.L().Fatal("gRPC serve failed", zap.Error(err))
			}
		}()
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
	// 两个协议并行停止接收新请求，共用同一个退出期限
	grpcStopped := make(chan error, 1)
	if application.GRPC != nil {
		go func() { grpcStopped <- application.GRPC.Shutdown(ctx) }()
	} else {
		grpcStopped <- nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		logger.L().Error("server shutdown failed", zap.Error(err))
	}
	if err := <-grpcStopped; err != nil {
		logger.L().Warn("gRPC shutdown deadline reached", zap.Error(err))
	}
	logger.L().Info("server exited")
}
