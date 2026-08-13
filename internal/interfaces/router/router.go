package router

import (
	"github.com/gin-gonic/gin"

	"env-vault/internal/infrastructure/config"
	"env-vault/internal/interfaces/handler"
	"env-vault/internal/interfaces/middleware"
)

// New 初始化 gin 引擎并注册路由
// 路由规范：/api/[版本]/[pub]/...
//   - /api/v1/pub/... 无认证接口，可随意调用
//   - /api/v1/...     需认证接口，挂载 JWT 认证中间件
func New(cfg *config.Config) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)

	r := gin.New()
	r.Use(middleware.RequestID(), middleware.GinLogger(), gin.Recovery())

	healthHandler := handler.NewHealthHandler()
	userHandler := handler.NewUserHandler()

	// 初始化 JWT 认证中间件（加载配置中的公钥）
	authMiddleware, err := middleware.Auth(cfg.Auth.JwtPublicKey)
	if err != nil {
		return nil, err
	}

	v1 := r.Group("/api/v1")
	{
		// 无认证接口分组
		pub := v1.Group("/pub")
		pub.GET("/health", healthHandler.Ping)

		// 需认证接口分组
		auth := v1.Group("", authMiddleware)
		auth.GET("/user/profile", userHandler.Profile) // 临时验证接口，后续业务开发时移除或调整
	}

	return r, nil
}
