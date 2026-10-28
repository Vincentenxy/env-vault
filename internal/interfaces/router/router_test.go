package router

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	infraauth "env-vault/internal/infrastructure/auth"
	"env-vault/internal/infrastructure/config"
)

// TestPersonalTokenRoutesIndependentOfLoginMode 使用完整路由组装验证公司认证模式不会丢失业务接口
func TestPersonalTokenRoutesIndependentOfLoginMode(t *testing.T) {
	privateKey, publicKey, err := infraauth.GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, localEnabled := range []bool{true, false} {
		name := "companyOnly"
		if localEnabled {
			name = "localAndCompany"
		}
		t.Run(name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			// 系统启动会异步预热用户缓存，测试只提供空用户集
			mock.ExpectQuery(`SELECT .* FROM "user_info"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			cfg := &config.Config{
				Server: config.ServerConfig{Mode: gin.TestMode},
				Security: config.SecurityConfig{ReadyAllowlist: []config.ReadyAllowRouteConfig{
					{Method: "POST", Path: "/api/v1/pub/auth/login"},
					{Method: "GET", Path: "/api/v1/masterKey/status"},
					{Method: "POST", Path: "/api/v1/masterKey/share"},
				}},
				Auth: config.AuthConfig{
					Local: config.LocalAuthConfig{
						Enabled: localEnabled, Issuer: "env-vault", Audience: "env-vault-web",
						KeyID: "local-key", PrivateKey: string(privateKey), PublicKey: string(publicKey),
						AccessTokenTTL: 2 * time.Hour,
					},
					Company: config.CompanyAuthConfig{
						Enabled: true, Issuer: "company", Audience: "company-web", PublicKey: string(publicKey),
					},
				},
			}
			r, err := New(context.Background(), cfg, db, nil)
			if err != nil {
				t.Fatal(err)
			}
			routes := make(map[string]bool)
			for _, route := range r.Routes() {
				routes[route.Method+" "+route.Path] = true
			}
			for _, operation := range []string{"list", "create", "delete"} {
				path := "POST /api/v1/user/token/" + operation
				if !routes[path] {
					t.Errorf("missing route %s", path)
				}
			}
			if routes["POST /api/v1/pub/auth/login"] != localEnabled {
				t.Error("password login route must follow auth.local.enabled")
			}
			for _, operation := range []string{"encrypt", "decrypt"} {
				if !routes["POST /api/v1/crypto/"+operation] {
					t.Errorf("missing crypto route %s", operation)
				}
				for _, prefix := range []string{"/api/v1/user/crypto/", "/api/v1/pub/crypto/"} {
					if routes["POST "+prefix+operation] {
						t.Errorf("unexpected crypto route %s%s", prefix, operation)
					}
				}
			}
			if !routes["POST /api/v1/audit/global/list"] || !routes["POST /api/v1/audit/list"] {
				t.Error("both global and single-resource audit routes must remain available")
			}
			if routes["POST /api/v1/pub/audit/global/list"] {
				t.Error("global audit must not expose a public route")
			}
			// sqlmock 的 rowsWereClosed 不支持并发读取；通过连接池状态等待查询归还连接。
			deadline := time.Now().Add(time.Second)
			for {
				err = mock.ExpectationsWereMet()
				if (err == nil && sqlDB.Stats().InUse == 0) || time.Now().After(deadline) {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			if sqlDB.Stats().InUse != 0 {
				t.Fatal("user cache warm-up query did not release its connection")
			}
		})
	}
}
