package masterkey

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"env-vault/pkg/response"
)

var (
	// ErrInvalidReadyAllowlist Ready 白名单包含不合法的请求方法或路径
	ErrInvalidReadyAllowlist = errors.New("invalid master key ready allowlist")
)

// requiredReadyRoutes 防止配置遗漏导致系统无法查询状态或提交分片
var requiredReadyRoutes = []allowedRouteKey{
	{method: http.MethodPost, path: "/api/v1/pub/auth/login"},
	{method: http.MethodGet, path: "/api/v1/masterKey/status"},
	{method: http.MethodPost, path: "/api/v1/masterKey/share"},
}

// AllowedRoute 表示主密钥未就绪时允许访问的单个接口
type AllowedRoute struct {
	Method string // HTTP 请求方法
	Path   string // 不包含查询参数的精确接口路径
}

// allowedRouteKey 是规范化后用于常量时间查询的白名单键
type allowedRouteKey struct {
	method string
	path   string
}

// NewReadyMiddleware 创建使用主密钥状态控制请求访问的全局中间件
func NewReadyMiddleware(manager *Manager, routes []AllowedRoute) (gin.HandlerFunc, error) {
	// Manager 是中间件唯一的就绪状态来源，缺失时不能创建中间件
	if manager == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidReadyAllowlist)
	}

	// 启动时一次性规范化白名单，避免每个请求重复解析配置
	allowlist := make(map[allowedRouteKey]struct{}, len(routes))
	for i, route := range routes {
		method := strings.ToUpper(strings.TrimSpace(route.Method))
		path := strings.TrimSpace(route.Path)
		if !isSupportedHTTPMethod(method) {
			return nil, fmt.Errorf("%w: route %d has unsupported method", ErrInvalidReadyAllowlist, i+1)
		}
		if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
			return nil, fmt.Errorf("%w: route %d has invalid path", ErrInvalidReadyAllowlist, i+1)
		}
		allowlist[allowedRouteKey{method: method, path: path}] = struct{}{}
	}
	// 状态和分片提交接口缺失会导致系统无法完成启动，因此必须显式配置
	for _, required := range requiredReadyRoutes {
		if _, exists := allowlist[required]; !exists {
			return nil, fmt.Errorf("%w: required route %s %s is missing", ErrInvalidReadyAllowlist, required.method, required.path)
		}
	}

	return func(c *gin.Context) {
		// 主密钥激活后直接放行，不再进行白名单查询
		if manager.Ready() {
			c.Next()
			return
		}

		// URL.Path 不包含查询参数，因此同一路径携带查询参数时仍可精确命中
		key := allowedRouteKey{method: c.Request.Method, path: c.Request.URL.Path}
		if _, allowed := allowlist[key]; allowed {
			c.Next()
			return
		}

		// 未就绪且不在白名单时使用统一业务响应并终止后续处理链
		response.Fail(c, -2, "系统启动中")
		c.Abort()
	}, nil
}

// isSupportedHTTPMethod 判断配置是否使用标准 HTTP 方法
func isSupportedHTTPMethod(method string) bool {
	// 白名单只接受标准 HTTP 方法，不支持通配符
	switch method {
	case http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodOptions,
		http.MethodTrace:
		return true
	default:
		return false
	}
}
