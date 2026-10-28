package grpcserver

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	envvaultv1 "env-vault/api/envvault/v1"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/logger"
)

// Readiness 复用 HTTP 服务持有的同一个主密钥就绪状态
type Readiness interface {
	Ready() bool
}

// Server 管理 gRPC 协议注册和优雅退出，不自行创建数据库或主密钥实例
type Server struct {
	server *grpc.Server
}

// New 使用已组装的业务依赖创建 gRPC 服务，后续应用认证在 interceptor 中扩展
func New(reader SecretReader, readiness Readiness, recorder auditdomain.Recorder) (*Server, error) {
	if reader == nil || readiness == nil {
		return nil, errors.New("gRPC reader and readiness are required")
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(requestInterceptor(readiness)))
	envvaultv1.RegisterSecretServiceServer(server, &secretHandler{reader: reader, recorder: recorder})
	// 反射仅提供协议描述，方便 grpcurl 调试，不提供密钥读取之外的业务方法
	reflection.Register(server)
	return &Server{server: server}, nil
}

// Serve 使用调用方创建的监听器，监听失败时由主进程统一终止启动
func (s *Server) Serve(listener net.Listener) error {
	return s.server.Serve(listener)
}

// Shutdown 等待正在执行的读取完成，超过退出期限时强制关闭连接
func (s *Server) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.server.Stop()
		<-done
		return ctx.Err()
	}
}

// requestInterceptor 统一处理调用关联 ID、主密钥就绪、取消和安全访问日志
func requestInterceptor(readiness Readiness) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, resultErr error) {
		traceID := requestID(ctx)
		ctx = context.WithValue(ctx, logger.TraceIDKey, traceID)
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", traceID))
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				// panic 内容可能包含业务数据，访问日志仅记录安全的协议状态
				response = nil
				resultErr = status.Error(codes.Internal, "internal error")
				logger.Error(ctx, "gRPC handler panicked", zap.String("method", info.FullMethod))
			}
			logger.Info(ctx, "grpc access", zap.String("method", info.FullMethod),
				zap.String("status", status.Code(resultErr).String()), zap.Duration("cost", time.Since(started)))
		}()
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		if !readiness.Ready() {
			return nil, status.Error(codes.Unavailable, "系统启动中")
		}
		// TODO: 应用认证明确后，在业务读取前校验应用 Token 和目录、环境授权
		return handler(ctx, req)
	}
}

// requestID 仅接受有界的可打印关联 ID，其他请求生成新的 ID
func requestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-request-id"); len(values) == 1 {
			value := strings.TrimSpace(values[0])
			if value != "" && len(value) <= 128 && !strings.ContainsFunc(value, unicode.IsControl) {
				return value
			}
		}
	}
	return uuid.NewString()
}
