package masterkey

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type httpTestResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func TestHTTPHandlerGetStatus(t *testing.T) {
	// 未激活和配置激活两种状态都不能暴露密钥内容
	tests := []struct {
		name       string
		activate   bool
		wantReady  bool
		wantSource Source
	}{
		{name: "not ready", wantReady: false, wantSource: SourceUnknown},
		{name: "config ready", activate: true, wantReady: true, wantSource: SourceConfig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			if tt.activate {
				if err := manager.LoadConfigFallback(true, testKeyBase64); err != nil {
					t.Fatalf("LoadConfigFallback: %v", err)
				}
			}

			body := performMasterKeyRequest(t, manager, http.MethodGet, "/api/v1/pub/masterKey/status", nil)
			if body.Code != 0 {
				t.Fatalf("code = %d, want 0", body.Code)
			}
			var status StatusResponse
			if err := json.Unmarshal(body.Data, &status); err != nil {
				t.Fatalf("decode status: %v", err)
			}
			if status.Ready != tt.wantReady || status.Source != tt.wantSource {
				t.Fatalf("unexpected status: %+v", status)
			}
			if status.TotalShares != TotalShares || status.RequiredShares != RequiredShares {
				t.Fatalf("unexpected share settings: %+v", status)
			}
			if bytes.Contains(body.Data, []byte(testKeyBase64)) {
				t.Fatal("status response must not contain master key")
			}
		})
	}
}

func TestHTTPHandlerSubmitShares(t *testing.T) {
	// 使用乱序的三个有效 Token 验证接口可以完成系统激活
	manager := NewManager()
	tokens := createShareTokens(t, []byte("12345678901234567890123456789012"), uuid.New())
	body := performMasterKeyRequest(t, manager, http.MethodPost, "/api/v1/pub/masterKey/shares", SubmitSharesRequest{
		Shares: []string{tokens[4], tokens[0], tokens[2]},
	})

	if body.Code != 0 {
		t.Fatalf("code = %d, msg = %q", body.Code, body.Msg)
	}
	var status StatusResponse
	if err := json.Unmarshal(body.Data, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Ready || status.Source != SourceShares || !manager.Ready() {
		t.Fatalf("unexpected restored status: %+v", status)
	}
}

func TestHTTPHandlerSubmitSharesErrors(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	firstSet := createShareTokens(t, key, uuid.New())
	secondSet := createShareTokens(t, key, uuid.New())

	tests := []struct {
		name    string
		body    any
		prepare func(*Manager)
		wantMsg string
	}{
		{name: "wrong count", body: SubmitSharesRequest{Shares: firstSet[:2]}, wantMsg: "必须提交三份密钥分片"},
		{name: "empty share", body: SubmitSharesRequest{Shares: []string{firstSet[0], " ", firstSet[2]}}, wantMsg: "密钥分片不能为空"},
		{name: "invalid share", body: SubmitSharesRequest{Shares: []string{"invalid", firstSet[1], firstSet[2]}}, wantMsg: "密钥分片无效"},
		{name: "different sets", body: SubmitSharesRequest{Shares: []string{firstSet[0], firstSet[1], secondSet[2]}}, wantMsg: "密钥分片不属于同一批次"},
		{name: "duplicate share", body: SubmitSharesRequest{Shares: []string{firstSet[0], firstSet[0], firstSet[1]}}, wantMsg: "密钥分片重复"},
		{
			name: "already activated",
			body: SubmitSharesRequest{Shares: firstSet[:RequiredShares]},
			prepare: func(manager *Manager) {
				if err := manager.LoadConfigFallback(true, testKeyBase64); err != nil {
					t.Fatalf("LoadConfigFallback: %v", err)
				}
			},
			wantMsg: "系统主密钥已经激活",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			if tt.prepare != nil {
				tt.prepare(manager)
			}
			body := performMasterKeyRequest(t, manager, http.MethodPost, "/api/v1/pub/masterKey/shares", tt.body)
			if body.Code != -1 || body.Msg != tt.wantMsg {
				t.Fatalf("unexpected response: code=%d msg=%q", body.Code, body.Msg)
			}
		})
	}
}

func TestHTTPHandlerSubmitSharesRejectsInvalidJSON(t *testing.T) {
	// JSON 解析失败时沿用项目统一的参数错误响应
	manager := NewManager()
	body := performMasterKeyRawRequest(t, manager, http.MethodPost, "/api/v1/pub/masterKey/shares", strings.NewReader("{invalid"))
	if body.Code != -1 || !strings.HasPrefix(body.Msg, "invalid params:") {
		t.Fatalf("unexpected response: code=%d msg=%q", body.Code, body.Msg)
	}
}

func TestHTTPHandlerSubmitSharesRejectsOversizedBody(t *testing.T) {
	// 未认证接口必须在 JSON 解析阶段拒绝超过上限的请求体
	manager := NewManager()
	payload := `{"shares":["` + strings.Repeat("A", maxShareRequestBodySize) + `"]}`
	body := performMasterKeyRawRequest(t, manager, http.MethodPost, "/api/v1/pub/masterKey/shares", strings.NewReader(payload))
	if body.Code != -1 || !strings.Contains(body.Msg, "request body too large") {
		t.Fatalf("unexpected response: code=%d msg=%q", body.Code, body.Msg)
	}
}

// performMasterKeyRequest 将结构体编码为 JSON 后调用主密钥测试路由
func performMasterKeyRequest(t *testing.T, manager *Manager, method, path string, body any) httpTestResponse {
	t.Helper()
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		requestBody = bytes.NewReader(payload)
	}
	return performMasterKeyRawRequest(t, manager, method, path, requestBody)
}

// performMasterKeyRawRequest 创建独立 Gin 实例并返回统一响应结构
func performMasterKeyRawRequest(t *testing.T, manager *Manager, method, path string, body io.Reader) httpTestResponse {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	pub := engine.Group("/api/v1/pub")
	RegisterRoutes(pub, manager)

	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var responseBody httpTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return responseBody
}
