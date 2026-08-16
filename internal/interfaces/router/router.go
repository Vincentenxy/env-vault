package router

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	envapp "env-vault/internal/application/environment"
	folderapp "env-vault/internal/application/folder"
	orgapp "env-vault/internal/application/organization"
	projapp "env-vault/internal/application/project"
	tenantapp "env-vault/internal/application/tenant"
	"env-vault/internal/infrastructure/config"
	envrepo "env-vault/internal/infrastructure/persistence/environment"
	folderrepo "env-vault/internal/infrastructure/persistence/folder"
	orgrepo "env-vault/internal/infrastructure/persistence/organization"
	projrepo "env-vault/internal/infrastructure/persistence/project"
	tenantrepo "env-vault/internal/infrastructure/persistence/tenant"
	"env-vault/internal/interfaces/handler"
	"env-vault/internal/interfaces/middleware"
)

// New 初始化 gin 引擎并注册路由
// 路由规范：/api/[版本]/[pub]/...
//   - /api/v1/pub/... 无认证接口，可随意调用
//   - /api/v1/...     需认证接口，挂载 JWT 认证中间件
func New(cfg *config.Config, db *gorm.DB) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)

	r := gin.New()
	r.Use(middleware.RequestID(), middleware.GinLogger(), gin.Recovery())

	// 依赖组装（DDD 各层）
	tenantRepo := tenantrepo.NewRepository(db)
	tenantSvc := tenantapp.NewService(tenantRepo)

	orgRepo := orgrepo.NewRepository(db)
	orgSvc := orgapp.NewService(orgRepo)

	projRepo := projrepo.NewRepository(db)
	projSvc := projapp.NewService(projRepo)

	envRepo := envrepo.NewRepository(db)
	envSvc := envapp.NewService(envRepo)

	folderRepo := folderrepo.NewRepository(db)
	folderSvc := folderapp.NewService(folderRepo, envRepo)

	healthHandler := handler.NewHealthHandler()
	userHandler := handler.NewUserHandler()
	tenantHandler := handler.NewTenantHandler(tenantSvc)
	orgHandler := handler.NewOrganizationHandler(orgSvc)
	projectHandler := handler.NewProjectHandler(projSvc)
	environmentHandler := handler.NewEnvironmentHandler(envSvc)
	folderHandler := handler.NewFolderHandler(folderSvc)

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

		// 租户管理（带参数统一 POST）
		tenantGroup := auth.Group("/tenant")
		{
			tenantGroup.POST("/create", tenantHandler.Create)
			tenantGroup.POST("/update", tenantHandler.Update)
			tenantGroup.POST("/delete", tenantHandler.Delete)
			tenantGroup.POST("/info", tenantHandler.Detail)
			tenantGroup.POST("/list", tenantHandler.List)
		}

		// 组织管理（带参数统一 POST）
		orgGroup := auth.Group("/org")
		{
			orgGroup.POST("/create", orgHandler.Create)
			orgGroup.POST("/update", orgHandler.Update)
			orgGroup.POST("/delete", orgHandler.Delete)
			orgGroup.POST("/info", orgHandler.Detail)
			orgGroup.POST("/list", orgHandler.List)
		}

		// 项目管理（带参数统一 POST）
		projectGroup := auth.Group("/project")
		{
			projectGroup.POST("/create", projectHandler.Create)
			projectGroup.POST("/update", projectHandler.Update)
			projectGroup.POST("/delete", projectHandler.Delete)
			projectGroup.POST("/info", projectHandler.Detail)
			projectGroup.POST("/list", projectHandler.List)
		}

		// 环境管理（带参数统一 POST）
		environmentGroup := auth.Group("/environment")
		{
			environmentGroup.POST("/create", environmentHandler.Create)
			environmentGroup.POST("/update", environmentHandler.Update)
			environmentGroup.POST("/delete", environmentHandler.Delete)
			environmentGroup.POST("/info", environmentHandler.Detail)
			environmentGroup.POST("/list", environmentHandler.List)
		}

		// 文件夹管理（带参数统一 POST）
		folderGroup := auth.Group("/folder")
		{
			folderGroup.POST("/create", folderHandler.Create)
			folderGroup.POST("/update", folderHandler.Update)
			folderGroup.POST("/delete", folderHandler.Delete)
			folderGroup.POST("/info", folderHandler.Detail)
			folderGroup.POST("/list", folderHandler.List)
		}
	}

	return r, nil
}
