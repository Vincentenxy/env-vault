package router

import (
	"github.com/gin-gonic/gin"

	"env-vault/internal/interfaces/grpcserver"
)

// Application 共享 HTTP 和 gRPC 所需的业务依赖与主密钥生命周期
type Application struct {
	HTTP *gin.Engine
	GRPC *grpcserver.Server // 未启用 gRPC 时为 nil
}
