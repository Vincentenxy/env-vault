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

			body := performMasterKeyRequest(t, manager, http.MethodGet, "/api/v1/masterKey/status", nil)
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

func TestHTTPHandlerSubmitShare(t *testing.T) {
	// 使用乱序的三个有效 Token 验证接口逐份累计并完成系统激活
	manager := NewManager()
	tokens := createShareTokens(t, []byte("12345678901234567890123456789012"), uuid.New())
	for index, token := range []string{tokens[4], tokens[0]} {
		body := performMasterKeyRequest(t, manager, http.MethodPost, "/api/v1/masterKey/share", SubmitShareRequest{Share: token})
		var status StatusResponse
		if body.Code != 0 || json.Unmarshal(body.Data, &status) != nil {
			t.Fatalf("submit %d failed: %+v", index+1, body)
		}
		if status.Ready || status.SubmittedShares != index+1 || !status.CanSubmit {
			t.Fatalf("unexpected pending status: %+v", status)
		}
	}
	body := performMasterKeyRequest(t, manager, http.MethodPost, "/api/v1/masterKey/share", SubmitShareRequest{Share: tokens[2]})

	if body.Code != 0 {
		t.Fatalf("code = %d, msg = %q", body.Code, body.Msg)
	}
	var status StatusResponse
	if err := json.Unmarshal(body.Data, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Ready || status.Source != SourceShares || status.SubmittedShares != 0 || status.CanSubmit || !manager.Ready() {
		t.Fatalf("unexpected restored status: %+v", status)
	}
}

func TestHTTPHandlerSubmitShareErrors(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	firstSet := createShareTokens(t, key, uuid.New())
	secondSet := createShareTokens(t, key, uuid.New())

	tests := []struct {
		name    string
		body    SubmitShareRequest
		prepare func(*Manager)
		wantMsg string
	}{
		{name: "empty share", body: SubmitShareRequest{Share: " "}, wantMsg: "密钥分片不能为空"},
		{name: "invalid share", body: SubmitShareRequest{Share: "invalid"}, wantMsg: "密钥分片无效"},
		{
			name: "different sets", body: SubmitShareRequest{Share: secondSet[1]}, wantMsg: "密钥分片不属于同一批次",
			prepare: func(manager *Manager) {
				if err := manager.SubmitShare(firstSet[0]); err != nil {
					t.Fatalf("prepare first share: %v", err)
				}
			},
		},
		{
			name: "duplicate share", body: SubmitShareRequest{Share: firstSet[0]}, wantMsg: "密钥分片重复",
			prepare: func(manager *Manager) {
				if err := manager.SubmitShare(firstSet[0]); err != nil {
					t.Fatalf("prepare first share: %v", err)
				}
			},
		},
		{
			name: "already activated",
			body: SubmitShareRequest{Share: firstSet[0]},
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
			body := performMasterKeyRequest(t, manager, http.MethodPost, "/api/v1/masterKey/share", tt.body)
			if body.Code != -1 || body.Msg != tt.wantMsg {
				t.Fatalf("unexpected response: code=%d msg=%q", body.Code, body.Msg)
			}
		})
	}
}

func TestHTTPHandlerSubmitShareRejectsInvalidJSON(t *testing.T) {
	// JSON 解析失败时沿用项目统一的参数错误响应
	manager := NewManager()
	body := performMasterKeyRawRequest(t, manager, http.MethodPost, "/api/v1/masterKey/share", strings.NewReader("{invalid"))
	if body.Code != -1 || !strings.HasPrefix(body.Msg, "invalid params:") {
		t.Fatalf("unexpected response: code=%d msg=%q", body.Code, body.Msg)
	}
}

func TestHTTPHandlerSubmitShareRejectsOversizedBody(t *testing.T) {
	manager := NewManager()
	payload := `{"share":"` + strings.Repeat("A", maxShareRequestBodySize) + `"}`
	body := performMasterKeyRawRequest(t, manager, http.MethodPost, "/api/v1/masterKey/share", strings.NewReader(payload))
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
	v1 := engine.Group("/api/v1")
	RegisterRoutes(v1, manager)

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
