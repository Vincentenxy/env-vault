package masterkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"env-vault/pkg/response"
)

func TestNewReadyMiddlewareRejectsInvalidConfiguration(t *testing.T) {
	// 无 Manager、未知方法和非绝对路径都必须在启动阶段报错
	tests := []struct {
		name    string
		manager *Manager
		routes  []AllowedRoute
	}{
		{name: "nil manager", routes: readyTestRoutes()},
		{name: "missing required routes", manager: NewManager(), routes: []AllowedRoute{{Method: http.MethodGet, Path: "/health"}}},
		{name: "unsupported method", manager: NewManager(), routes: readyTestRoutes(AllowedRoute{Method: "ANY", Path: "/health"})},
		{name: "relative path", manager: NewManager(), routes: readyTestRoutes(AllowedRoute{Method: http.MethodGet, Path: "health"})},
		{name: "query in path", manager: NewManager(), routes: readyTestRoutes(AllowedRoute{Method: http.MethodGet, Path: "/health?full=true"})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewReadyMiddleware(tt.manager, tt.routes)
			if !errors.Is(err, ErrInvalidReadyAllowlist) {
				t.Fatalf("NewReadyMiddleware error = %v, want ErrInvalidReadyAllowlist", err)
			}
		})
	}
}

func TestReadyMiddlewareBlocksRequestWhileNotReady(t *testing.T) {
	// 普通接口在主密钥未就绪时不能进入后续 Handler
	manager := NewManager()
	engine, handlerCalled := newReadyTestEngine(t, manager, nil)

	responseRecorder := performReadyRequest(engine, http.MethodPost, "/api/v1/secret/list")
	if responseRecorder.Code != http.StatusOK || *handlerCalled {
		t.Fatalf("unexpected HTTP status=%d handlerCalled=%v", responseRecorder.Code, *handlerCalled)
	}
	var body response.Response
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != -2 || body.Message != "系统启动中" || body.Data != nil {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestReadyMiddlewareUsesExactMethodAndPath(t *testing.T) {
	// 只有方法和路径同时匹配才允许通过，查询参数不参与路径匹配
	manager := NewManager()
	engine, handlerCalled := newReadyTestEngine(t, manager, []AllowedRoute{
		{Method: "get", Path: "/api/v1/pub/health"},
	})

	allowed := performReadyRequest(engine, http.MethodGet, "/api/v1/pub/health?full=true")
	if allowed.Code != http.StatusNoContent || !*handlerCalled {
		t.Fatalf("allowed request status=%d handlerCalled=%v", allowed.Code, *handlerCalled)
	}

	*handlerCalled = false
	wrongMethod := performReadyRequest(engine, http.MethodPost, "/api/v1/pub/health")
	if wrongMethod.Code != http.StatusOK || *handlerCalled {
		t.Fatalf("wrong method status=%d handlerCalled=%v", wrongMethod.Code, *handlerCalled)
	}

	*handlerCalled = false
	wrongPath := performReadyRequest(engine, http.MethodGet, "/api/v1/pub/health/detail")
	if wrongPath.Code != http.StatusOK || *handlerCalled {
		t.Fatalf("wrong path status=%d handlerCalled=%v", wrongPath.Code, *handlerCalled)
	}
}

func TestReadyMiddlewareObservesActivationWithoutRestart(t *testing.T) {
	// 同一个中间件实例必须在 Manager 激活后立即放行普通接口
	manager := NewManager()
	engine, handlerCalled := newReadyTestEngine(t, manager, nil)

	blocked := performReadyRequest(engine, http.MethodGet, "/api/v1/secret/list")
	if blocked.Code != http.StatusOK || *handlerCalled {
		t.Fatalf("blocked request status=%d handlerCalled=%v", blocked.Code, *handlerCalled)
	}

	if err := manager.LoadConfigFallback(true, testKeyBase64); err != nil {
		t.Fatalf("LoadConfigFallback: %v", err)
	}
	allowed := performReadyRequest(engine, http.MethodGet, "/api/v1/secret/list")
	if allowed.Code != http.StatusNoContent || !*handlerCalled {
		t.Fatalf("allowed request status=%d handlerCalled=%v", allowed.Code, *handlerCalled)
	}
}

func TestReadyMiddlewareBootstrapFlow(t *testing.T) {
	// 白名单接口完成分片恢复后，同一个 Router 立即开放普通接口
	manager := NewManager()
	readyMiddleware, err := NewReadyMiddleware(manager, readyTestRoutes())
	if err != nil {
		t.Fatalf("NewReadyMiddleware: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(readyMiddleware)
	v1 := engine.Group("/api/v1")
	RegisterRoutes(v1, manager)
	engine.GET("/api/v1/protected", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	statusResponse := performReadyRequest(engine, http.MethodGet, "/api/v1/masterKey/status")
	if statusResponse.Code != http.StatusOK || manager.Ready() {
		t.Fatalf("status request code=%d ready=%v", statusResponse.Code, manager.Ready())
	}

	tokens := createShareTokens(t, []byte("12345678901234567890123456789012"), uuid.New())
	for index, token := range []string{tokens[3], tokens[0], tokens[4]} {
		payload, err := json.Marshal(SubmitShareRequest{Share: token})
		if err != nil {
			t.Fatalf("encode share request: %v", err)
		}
		shareResponse := performReadyRequestWithBody(engine, http.MethodPost, "/api/v1/masterKey/share", bytes.NewReader(payload))
		if shareResponse.Code != http.StatusOK {
			t.Fatalf("share %d request code=%d body=%s", index+1, shareResponse.Code, shareResponse.Body.String())
		}
	}
	if !manager.Ready() {
		t.Fatal("manager must be ready after three shares")
	}

	protectedResponse := performReadyRequest(engine, http.MethodGet, "/api/v1/protected")
	if protectedResponse.Code != http.StatusNoContent {
		t.Fatalf("protected request code=%d", protectedResponse.Code)
	}
}

func newReadyTestEngine(t *testing.T, manager *Manager, routes []AllowedRoute) (*gin.Engine, *bool) {
	t.Helper()
	readyMiddleware, err := NewReadyMiddleware(manager, readyTestRoutes(routes...))
	if err != nil {
		t.Fatalf("NewReadyMiddleware: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(readyMiddleware)
	handlerCalled := false
	engine.Any("/*path", func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})
	return engine, &handlerCalled
}

func performReadyRequest(engine *gin.Engine, method, path string) *httptest.ResponseRecorder {
	return performReadyRequestWithBody(engine, method, path, nil)
}

func performReadyRequestWithBody(engine *gin.Engine, method, path string, body io.Reader) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(responseRecorder, request)
	return responseRecorder
}

func readyTestRoutes(extra ...AllowedRoute) []AllowedRoute {
	// 每个有效测试配置都包含系统登录和完成启动所需的接口
	routes := []AllowedRoute{
		{Method: http.MethodPost, Path: "/api/v1/pub/auth/login"},
		{Method: http.MethodGet, Path: "/api/v1/masterKey/status"},
		{Method: http.MethodPost, Path: "/api/v1/masterKey/share"},
	}
	return append(routes, extra...)
}
