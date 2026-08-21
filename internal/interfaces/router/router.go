package router

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	redislib "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	envapp "env-vault/internal/application/environment"
	folderapp "env-vault/internal/application/folder"
	orgapp "env-vault/internal/application/organization"
	projapp "env-vault/internal/application/project"
	secretapp "env-vault/internal/application/secret"
	tenantapp "env-vault/internal/application/tenant"
	userapp "env-vault/internal/application/user"
	usercache "env-vault/internal/infrastructure/cache/user"
	"env-vault/internal/infrastructure/config"
	envrepo "env-vault/internal/infrastructure/persistence/environment"
	folderrepo "env-vault/internal/infrastructure/persistence/folder"
	orgrepo "env-vault/internal/infrastructure/persistence/organization"
	projrepo "env-vault/internal/infrastructure/persistence/project"
	secretrepo "env-vault/internal/infrastructure/persistence/secret"
	tenantrepo "env-vault/internal/infrastructure/persistence/tenant"
	userrepo "env-vault/internal/infrastructure/persistence/user"
	"env-vault/internal/interfaces/handler"
	"env-vault/internal/interfaces/middleware"
	"env-vault/pkg/crypto"
	"env-vault/pkg/logger"
)

// New 初始化 gin 引擎并注册路由
// 路由规范：/api/[版本]/[pub]/...
//   - /api/v1/pub/... 无认证接口，可随意调用
//   - /api/v1/...     需认证接口，挂载 JWT 认证中间件
func New(cfg *config.Config, db *gorm.DB, redisClient redislib.UniversalClient) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)

	r := gin.New()
	r.Use(middleware.RequestID(), middleware.GinLogger(), gin.Recovery())

	// 依赖组装（DDD 各层）
	tenantRepo := tenantrepo.NewRepository(db)
	orgRepo := orgrepo.NewRepository(db)
	tenantSvc := tenantapp.NewService(tenantRepo, orgRepo)
	orgSvc := orgapp.NewService(orgRepo)

	projRepo := projrepo.NewRepository(db)
	envRepo := envrepo.NewRepository(db)
	projSvc := projapp.NewService(projRepo, envRepo)
	envSvc := envapp.NewService(envRepo)

	folderRepo := folderrepo.NewRepository(db)
	folderSvc := folderapp.NewService(folderRepo, envRepo)

	// secret value 加解密器（私钥来自配置 security.encryption_key）
	cipher, err := crypto.New(cfg.Security.EncryptionKey)
	if err != nil {
		return nil, err
	}
	secretRepo := secretrepo.NewRepository(db)
	secretSvc := secretapp.NewService(secretRepo, folderRepo, envRepo, cipher)

	userRepo := userrepo.NewRepository(db)
	userProfileCache := usercache.NewRedisProfileCache(redisClient, cfg.Redis.KeyPrefix)
	userNameCache := usercache.NewMemoryNameCache()
	userSvc := userapp.NewService(userRepo, userProfileCache, userNameCache)

	healthHandler := handler.NewHealthHandler()
	userHandler := handler.NewUserHandler(userSvc)
	tenantHandler := handler.NewTenantHandler(tenantSvc)
	orgHandler := handler.NewOrganizationHandler(orgSvc)
	projectHandler := handler.NewProjectHandler(projSvc)
	environmentHandler := handler.NewEnvironmentHandler(envSvc)
	folderHandler := handler.NewFolderHandler(folderSvc)
	secretHandler := handler.NewSecretHandler(secretSvc)

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
		userGroup := auth.Group("/user")
		{
			userGroup.POST("/update", userHandler.Update)
		}

		// 租户管理（带参数统一 POST）
		tenantGroup := auth.Group("/tenant")
		{
			tenantGroup.POST("/create", tenantHandler.Create)
			tenantGroup.POST("/update", tenantHandler.Update)
			tenantGroup.POST("/delete", tenantHandler.Delete)
			tenantGroup.POST("/info", tenantHandler.Detail)
			tenantGroup.POST("/list", tenantHandler.List)
			tenantGroup.GET("/withOrgProject", tenantHandler.WithOrgProject)
		}

		// 组织管理（带参数统一 POST）
		orgGroup := auth.Group("/org")
		{
			orgGroup.POST("/create", orgHandler.Create)
			orgGroup.POST("/update", orgHandler.Update)
			orgGroup.POST("/delete", orgHandler.Delete)
			orgGroup.POST("/info", orgHandler.Detail)
			orgGroup.POST("/list", orgHandler.List)
			orgGroup.GET("/withProject", orgHandler.WithProject)
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
		environmentGroup := auth.Group("/env")
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

		// 密钥管理（带参数统一 POST）
		secretGroup := auth.Group("/secret")
		{
			secretGroup.POST("/create", secretHandler.Create)
			secretGroup.POST("/update", secretHandler.Update)
			secretGroup.POST("/list", secretHandler.List)
			secretGroup.POST("/info", secretHandler.Detail)
			secretGroup.POST("/history", secretHandler.History)
			secretGroup.POST("/delete", secretHandler.Delete)
		}
	}

	go warmUpUsers(userSvc)
	return r, nil
}

func warmUpUsers(svc userapp.IService) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count, err := svc.WarmUp(ctx)
	if err != nil {
		logger.Error(ctx, "warm up user cache failed", zap.Int("count", count), zap.Error(err))
		return
	}
	logger.Info(ctx, "user cache warmed up", zap.Int("count", count))
}
