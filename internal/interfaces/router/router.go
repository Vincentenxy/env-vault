package router

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	redislib "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	authapp "env-vault/internal/application/auth"
	envapp "env-vault/internal/application/environment"
	folderapp "env-vault/internal/application/folder"
	orgapp "env-vault/internal/application/organization"
	projapp "env-vault/internal/application/project"
	secretapp "env-vault/internal/application/secret"
	tenantapp "env-vault/internal/application/tenant"
	userapp "env-vault/internal/application/user"
	infraauth "env-vault/internal/infrastructure/auth"
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
	"env-vault/internal/masterkey"
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
	projRepo := projrepo.NewRepository(db)
	envRepo := envrepo.NewRepository(db)
	folderRepo := folderrepo.NewRepository(db)
	userRepo := userrepo.NewRepository(db)
	userProfileCache := usercache.NewRedisProfileCache(redisClient, cfg.Redis.KeyPrefix)
	userBlockStatusCache := usercache.NewRedisBlockStatusCache(redisClient, cfg.Redis.KeyPrefix)
	userNameCache := usercache.NewMemoryNameCache()
	userSvc := userapp.NewService(
		userRepo,
		userProfileCache,
		userNameCache,
		userapp.WithBlockStatusCache(userBlockStatusCache),
		userapp.WithAllocationRepositories(tenantRepo, orgRepo, projRepo),
	)

	passwordHasher, err := infraauth.NewPasswordHasher()
	if err != nil {
		return nil, fmt.Errorf("initialize local password hasher: %w", err)
	}
	jwtProviders := make([]middleware.JWTProvider, 0, 2)
	var localAuthSvc authapp.IService
	if cfg.Auth.Local.Enabled {
		privateMaterial, err := infraauth.LoadKeyMaterial(cfg.Auth.Local.PrivateKey, cfg.Auth.Local.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load local JWT private key: %w", err)
		}
		privateKey, err := infraauth.ParseRSAPrivateKey(privateMaterial)
		if err != nil {
			return nil, fmt.Errorf("parse local JWT private key: %w", err)
		}
		publicMaterial, err := infraauth.LoadKeyMaterial(cfg.Auth.Local.PublicKey, cfg.Auth.Local.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load local JWT public key: %w", err)
		}
		publicKey, err := infraauth.ParseRSAPublicKey(publicMaterial)
		if err != nil {
			return nil, fmt.Errorf("parse local JWT public key: %w", err)
		}
		if !privateKey.PublicKey.Equal(publicKey) {
			return nil, fmt.Errorf("local JWT private and public keys do not match")
		}
		issuer, err := infraauth.NewJWTIssuer(
			privateKey,
			cfg.Auth.Local.Issuer,
			cfg.Auth.Local.Audience,
			cfg.Auth.Local.KeyID,
			cfg.Auth.Local.AccessTokenTTL,
		)
		if err != nil {
			return nil, err
		}
		localAuthSvc = authapp.NewService(userRepo, passwordHasher, issuer)
		jwtProviders = append(jwtProviders, middleware.JWTProvider{
			Issuer: cfg.Auth.Local.Issuer, Audience: cfg.Auth.Local.Audience,
			KeyID: cfg.Auth.Local.KeyID, PublicKey: string(publicMaterial),
		})
	}
	if cfg.Auth.Company.Enabled {
		publicMaterial, err := infraauth.LoadKeyMaterial(cfg.Auth.Company.PublicKey, cfg.Auth.Company.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load company JWT public key: %w", err)
		}
		jwtProviders = append(jwtProviders, middleware.JWTProvider{
			Issuer: cfg.Auth.Company.Issuer, Audience: cfg.Auth.Company.Audience,
			KeyID: cfg.Auth.Company.KeyID, PublicKey: string(publicMaterial),
		})
	}

	tenantSvc := tenantapp.NewService(tenantRepo, orgRepo, userSvc)
	orgSvc := orgapp.NewService(orgRepo, userSvc)
	projSvc := projapp.NewService(
		projRepo,
		projapp.WithEnvironmentRepository(envRepo),
		projapp.WithNicknameResolver(userSvc),
	)
	folderSvc := folderapp.NewService(folderRepo, envRepo, userSvc)

	// 主密钥未激活时仍允许 HTTP 服务完成启动
	masterKeyManager := masterkey.NewManager()
	if err := masterKeyManager.LoadConfigFallback(cfg.Security.AllowConfigKeyFallback, cfg.Security.EncryptionKey); err != nil {
		return nil, err
	}
	readyMiddleware, err := masterkey.NewReadyMiddleware(masterKeyManager, readyAllowedRoutes(cfg.Security.ReadyAllowlist))
	if err != nil {
		return nil, err
	}
	// Ready 检查位于具体路由和 JWT 认证之前
	r.Use(readyMiddleware)
	secretRepo := secretrepo.NewRepository(db)
	envSvc := envapp.NewService(
		envRepo,
		envapp.WithResourceClone(folderRepo, secretRepo, masterKeyManager),
	)
	secretSvc := secretapp.NewService(secretRepo, folderRepo, envRepo, masterKeyManager, userSvc)

	healthHandler := handler.NewHealthHandler()
	var authHandler *handler.AuthHandler
	if localAuthSvc != nil {
		authHandler = handler.NewAuthHandler(localAuthSvc)
	}
	userHandler := handler.NewUserHandler(userSvc)
	tenantHandler := handler.NewTenantHandler(tenantSvc)
	orgHandler := handler.NewOrganizationHandler(orgSvc)
	projectHandler := handler.NewProjectHandler(projSvc)
	environmentHandler := handler.NewEnvironmentHandler(envSvc)
	folderHandler := handler.NewFolderHandler(folderSvc)
	secretHandler := handler.NewSecretHandler(secretSvc)

	// 初始化 JWT 认证中间件（加载配置中的公钥）
	authMiddleware, err := middleware.Auth(jwtProviders, userSvc)
	if err != nil {
		return nil, err
	}

	v1 := r.Group("/api/v1")
	{
		// 无认证接口分组
		pub := v1.Group("/pub")
		pub.GET("/health", healthHandler.Ping)
		if authHandler != nil {
			pub.POST("/auth/login", authHandler.Login)
		}

		// 需认证接口分组
		auth := v1.Group("", authMiddleware)
		masterkey.RegisterRoutes(auth, masterKeyManager)
		authGroup := auth.Group("/auth")
		{
			authGroup.GET("/me", userHandler.Me)
		}

		userGroup := auth.Group("/user")
		{
			userGroup.POST("/update", userHandler.Update)
			userGroup.POST("/list", userHandler.List)
			userGroup.POST("/allocate", userHandler.Allocate)
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
			secretGroup.POST("/history/batch", secretHandler.BatchHistory)
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

// readyAllowedRoutes 将安全配置转换为主密钥模块的白名单模型
func readyAllowedRoutes(configured []config.ReadyAllowRouteConfig) []masterkey.AllowedRoute {
	// 配置层 DTO 只在 Router 组装阶段转换为主密钥模块的路由模型
	routes := make([]masterkey.AllowedRoute, 0, len(configured))
	for _, route := range configured {
		routes = append(routes, masterkey.AllowedRoute{
			Method: route.Method,
			Path:   route.Path,
		})
	}
	return routes
}
