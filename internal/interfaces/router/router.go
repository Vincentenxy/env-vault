package router

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	redislib "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	auditapp "env-vault/internal/application/audit"
	authapp "env-vault/internal/application/auth"
	envapp "env-vault/internal/application/environment"
	folderapp "env-vault/internal/application/folder"
	orgapp "env-vault/internal/application/organization"
	personalapp "env-vault/internal/application/personalsecret"
	projapp "env-vault/internal/application/project"
	secretapp "env-vault/internal/application/secret"
	tenantapp "env-vault/internal/application/tenant"
	userapp "env-vault/internal/application/user"
	tokenapp "env-vault/internal/application/useraccesstoken"
	infraauth "env-vault/internal/infrastructure/auth"
	usercache "env-vault/internal/infrastructure/cache/user"
	"env-vault/internal/infrastructure/config"
	auditrepo "env-vault/internal/infrastructure/persistence/audit"
	envrepo "env-vault/internal/infrastructure/persistence/environment"
	folderrepo "env-vault/internal/infrastructure/persistence/folder"
	orgrepo "env-vault/internal/infrastructure/persistence/organization"
	personalrepo "env-vault/internal/infrastructure/persistence/personalsecret"
	projrepo "env-vault/internal/infrastructure/persistence/project"
	secretrepo "env-vault/internal/infrastructure/persistence/secret"
	tenantrepo "env-vault/internal/infrastructure/persistence/tenant"
	userrepo "env-vault/internal/infrastructure/persistence/user"
	tokenrepo "env-vault/internal/infrastructure/persistence/useraccesstoken"
	"env-vault/internal/interfaces/handler"
	"env-vault/internal/interfaces/middleware"
	"env-vault/internal/masterkey"
	"env-vault/pkg/logger"
)

// New 初始化 gin 引擎并注册路由
// 路由规范：/api/[版本]/[pub]/...
//   - /api/v1/pub/... 无认证接口，可随意调用
//   - /api/v1/...     需认证接口，挂载 JWT 认证中间件
func New(ctx context.Context, cfg *config.Config, db *gorm.DB, redisClient redislib.UniversalClient) (*gin.Engine, error) {
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
	auditRepo := auditrepo.NewRepository(db)
	auditSvc := auditapp.NewService(auditRepo, userSvc)
	userSvc.WithAuditRecorder(auditSvc)

	passwordHasher, err := infraauth.NewPasswordHasher()
	if err != nil {
		return nil, fmt.Errorf("initialize local password hasher: %w", err)
	}
	jwtProviders := make([]middleware.JWTProvider, 0, 2)
	var localAuthSvc authapp.IService
	var personalTokenIssuer *infraauth.JWTIssuer
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
		personalTokenIssuer = issuer
		localAuthSvc = authapp.NewService(userRepo, passwordHasher, issuer).WithAuditRecorder(auditSvc)
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

	tenantSvc := tenantapp.NewService(tenantRepo, orgRepo, userSvc).WithAuditRecorder(auditSvc)
	orgSvc := orgapp.NewService(orgRepo, userSvc).WithAuditRecorder(auditSvc)
	projSvc := projapp.NewService(
		projRepo,
		projapp.WithEnvironmentRepository(envRepo),
		projapp.WithNicknameResolver(userSvc),
		projapp.WithManagerEligibilityChecker(userRepo),
	).WithAuditRecorder(auditSvc)
	folderSvc := folderapp.NewService(folderRepo, envRepo, userSvc).WithAuditRecorder(auditSvc)

	// 主密钥未激活时仍允许 HTTP 服务完成启动
	masterKeyManager := masterkey.NewManager()
	if err := masterKeyManager.LoadConfigFallback(cfg.Security.AllowConfigKeyFallback, cfg.Security.EncryptionKey); err != nil {
		return nil, err
	}
	peerRecovery, err := newMasterKeyPeerRecovery(masterKeyManager, cfg.Security)
	if err != nil {
		return nil, err
	}
	readyMiddleware, err := masterkey.NewReadyMiddleware(masterKeyManager, readyAllowedRoutes(cfg.Security.ReadyAllowlist))
	if err != nil {
		return nil, err
	}
	// Ready 检查位于具体路由和 JWT 认证之前
	r.Use(readyMiddleware)
	secretRepo := secretrepo.NewRepository(db)
	personalSecretRepo := personalrepo.NewRepository(db)
	var personalTokenSvc *tokenapp.Service
	if personalTokenIssuer != nil {
		personalTokenRepo := tokenrepo.NewRepository(db)
		personalTokenSvc = tokenapp.NewService(personalTokenRepo, userSvc, personalTokenIssuer, masterKeyManager).
			WithAuditRecorder(auditSvc)
	}
	envSvc := envapp.NewService(
		envRepo,
		envapp.WithResourceClone(folderRepo, secretRepo, masterKeyManager),
	).WithAuditRecorder(auditSvc)
	secretSvc := secretapp.NewService(secretRepo, folderRepo, envRepo, masterKeyManager, userSvc).
		WithAuditRecorder(auditSvc)
	personalSecretSvc := personalapp.NewService(personalSecretRepo, userSvc, masterKeyManager).
		WithAuditRecorder(auditSvc)

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
	personalSecretHandler := handler.NewPersonalSecretHandler(personalSecretSvc)
	var personalTokenHandler *handler.UserAccessTokenHandler
	if personalTokenSvc != nil {
		personalTokenHandler = handler.NewUserAccessTokenHandler(personalTokenSvc)
	}
	auditHandler := handler.NewAuditHandler(auditSvc)

	// 初始化 JWT 认证中间件（加载配置中的公钥）
	personalTokenCheckers := make([]middleware.PersonalTokenChecker, 0, 1)
	if personalTokenSvc != nil {
		personalTokenCheckers = append(personalTokenCheckers, personalTokenSvc)
	}
	authMiddleware, err := middleware.AuthWithAudit(jwtProviders, userSvc, auditSvc, personalTokenCheckers...)
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
		masterkey.RegisterRoutes(auth, masterKeyManager, auditSvc)
		authGroup := auth.Group("/auth")
		{
			authGroup.GET("/me", userHandler.Me)
		}

		userGroup := auth.Group("/user")
		{
			userGroup.POST("/update", userHandler.Update)
			userGroup.POST("/list", userHandler.List)
			// TODO(permission): 权限中心接入后，为用户管理路由挂载 user:manage 授权中间件。
			userGroup.POST("/manage/list", userHandler.ManageList)
			userGroup.POST("/manage/update", userHandler.ManageUpdate)
			userGroup.POST("/allocate", userHandler.Allocate)

			personalSecretGroup := userGroup.Group("/secret")
			{
				personalSecretGroup.POST("/create", personalSecretHandler.Create)
				personalSecretGroup.POST("/update", personalSecretHandler.Update)
				personalSecretGroup.POST("/delete", personalSecretHandler.Delete)
				personalSecretGroup.POST("/list", personalSecretHandler.List)
				// TODO(permission): 权限中心接入后，为该路由挂载 user:manage 授权中间件。
				personalSecretGroup.POST("/manage/list", personalSecretHandler.ManageList)
				personalSecretGroup.POST("/reveal", personalSecretHandler.Reveal)
				personalSecretGroup.POST("/history", personalSecretHandler.History)
				personalSecretGroup.POST("/history/reveal", personalSecretHandler.RevealHistory)
			}

			if personalTokenHandler != nil {
				personalTokenGroup := userGroup.Group("/token")
				{
					personalTokenGroup.POST("/create", personalTokenHandler.Create)
					personalTokenGroup.POST("/list", personalTokenHandler.List)
					personalTokenGroup.POST("/delete", personalTokenHandler.Delete)
				}
			}
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

		// 业务审计日志是跨资源独立模块；本期先由 Secret 写入并在密钥页面查询。
		auditGroup := auth.Group("/audit")
		{
			auditGroup.POST("/list", auditHandler.List)
		}
	}

	// 集群内部主密钥传输不使用用户 JWT，单独挂载内部令牌校验。
	// 未配置 security.master_key_peer_token 时该接口保持禁用，避免意外暴露密钥传输能力。
	internal := r.Group("/internal/v1")
	masterkey.RegisterInternalRoutes(internal, masterKeyManager, cfg.Security.MasterKeyPeerToken, auditSvc)

	if cfg.Security.MasterKeyPeerRecovery.Enabled {
		go runMasterKeyPeerRecovery(ctx, peerRecovery)
	}
	go warmUpUsers(userSvc)
	return r, nil
}

func newMasterKeyPeerRecovery(manager *masterkey.Manager, security config.SecurityConfig) (*masterkey.PeerRecovery, error) {
	instanceID := ""
	if security.MasterKeyPeerRecovery.Enabled {
		instanceID = strings.TrimSpace(os.Getenv("POD_NAME"))
		if instanceID == "" {
			hostname, err := os.Hostname()
			if err != nil {
				return nil, fmt.Errorf("resolve master key peer instance ID: %w", err)
			}
			instanceID = hostname
		}
	}

	peer := security.MasterKeyPeerRecovery
	return masterkey.NewPeerRecovery(manager, masterkey.PeerRecoveryConfig{
		Enabled:              peer.Enabled,
		BaseURL:              peer.BaseURL,
		Token:                security.MasterKeyPeerToken,
		InstanceID:           instanceID,
		RequestTimeout:       peer.RequestTimeout,
		InitialRetryInterval: peer.InitialRetryInterval,
		MaxRetryInterval:     peer.MaxRetryInterval,
	})
}

func runMasterKeyPeerRecovery(ctx context.Context, recovery *masterkey.PeerRecovery) {
	if err := recovery.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error(ctx, "master key peer recovery stopped", zap.Error(err))
	}
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
