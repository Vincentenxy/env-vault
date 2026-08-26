package masterkey

import (
	"encoding/base64"
	"errors"
	"sync"
	"testing"
)

const testKeyBase64 = "Pk6V+TnUEZO6R8WOklCSrI/iM4QKHc55VQQrrptmVfk="

func TestManagerInitialState(t *testing.T) {
	// 新管理器必须在没有密钥的情况下安全返回未就绪
	manager := NewManager()

	if manager.Ready() {
		t.Fatal("new manager must not be ready")
	}
	if status := manager.Status(); status.Ready || status.Source != SourceUnknown {
		t.Fatalf("unexpected initial status: %+v", status)
	}
	if _, err := manager.Encrypt("secret"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Encrypt error = %v, want ErrNotReady", err)
	}
	if _, err := manager.Decrypt("ciphertext"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Decrypt error = %v, want ErrNotReady", err)
	}
}

func TestManagerActivateAndRoundTrip(t *testing.T) {
	// 激活后同时验证状态来源和实际加解密能力
	manager := NewManager()
	if err := manager.Activate(testKeyBase64, SourceConfig); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	status := manager.Status()
	if !status.Ready || status.Source != SourceConfig {
		t.Fatalf("unexpected active status: %+v", status)
	}

	ciphertext, err := manager.Encrypt("database-password")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plaintext, err := manager.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plaintext != "database-password" {
		t.Fatalf("Decrypt = %q, want %q", plaintext, "database-password")
	}
}

func TestManagerLoadConfigFallback(t *testing.T) {
	// 配置开关关闭、正常开启和错误密钥分别验证
	t.Run("disabled", func(t *testing.T) {
		manager := NewManager()
		if err := manager.LoadConfigFallback(false, "invalid-key"); err != nil {
			t.Fatalf("LoadConfigFallback: %v", err)
		}
		if manager.Ready() {
			t.Fatal("disabled config fallback must not activate manager")
		}
	})

	t.Run("enabled", func(t *testing.T) {
		manager := NewManager()
		if err := manager.LoadConfigFallback(true, testKeyBase64); err != nil {
			t.Fatalf("LoadConfigFallback: %v", err)
		}
		if status := manager.Status(); !status.Ready || status.Source != SourceConfig {
			t.Fatalf("unexpected config fallback status: %+v", status)
		}
	})

	t.Run("enabled with invalid key", func(t *testing.T) {
		manager := NewManager()
		if err := manager.LoadConfigFallback(true, "invalid-key"); err == nil {
			t.Fatal("LoadConfigFallback error = nil, want error")
		}
		if manager.Ready() {
			t.Fatal("invalid config key must not activate manager")
		}
	})
}

func TestManagerRejectsInvalidActivation(t *testing.T) {
	// 未知来源和非法 AES 密钥都不能改变就绪状态
	tests := []struct {
		name   string
		key    string
		source Source
		err    error
	}{
		{name: "unknown source", key: testKeyBase64, source: SourceUnknown, err: ErrInvalidSource},
		{name: "invalid key", key: "not-base64", source: SourceConfig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			err := manager.Activate(tt.key, tt.source)
			if err == nil {
				t.Fatal("Activate error = nil, want error")
			}
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Fatalf("Activate error = %v, want %v", err, tt.err)
			}
			if manager.Ready() {
				t.Fatal("manager must remain inactive")
			}
		})
	}
}

func TestManagerRejectsReplacement(t *testing.T) {
	// 第二次激活失败后必须继续使用第一次激活的密钥
	manager := NewManager()
	if err := manager.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("first Activate: %v", err)
	}

	ciphertext, err := manager.Encrypt("original-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	otherKey := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	if err := manager.Activate(otherKey, SourcePeer); !errors.Is(err, ErrAlreadyActivated) {
		t.Fatalf("second Activate error = %v, want ErrAlreadyActivated", err)
	}

	plaintext, err := manager.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt with original key: %v", err)
	}
	if plaintext != "original-value" {
		t.Fatalf("Decrypt = %q, want %q", plaintext, "original-value")
	}
	if source := manager.Status().Source; source != SourceShares {
		t.Fatalf("source = %q, want %q", source, SourceShares)
	}
}

func TestManagerAllowsOnlyOneConcurrentActivation(t *testing.T) {
	// 并发模拟多个管理员同时完成恢复后的激活请求
	const workers = 16
	manager := NewManager()
	start := make(chan struct{})
	results := make(chan error, workers)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- manager.Activate(testKeyBase64, SourceShares)
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	succeeded := 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrAlreadyActivated):
		default:
			t.Fatalf("Activate error = %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful activations = %d, want 1", succeeded)
	}
	if !manager.Ready() {
		t.Fatal("manager must be ready after activation")
	}
}
