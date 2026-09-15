package search

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	auditdomain "env-vault/internal/domain/audit"
	"github.com/google/uuid"
)

type readerFunc func(context.Context, Input) (storedPage, error)

func (f readerFunc) read(ctx context.Context, in Input) (storedPage, error) { return f(ctx, in) }

type decryptFunc func(string) (string, error)

func (f decryptFunc) Decrypt(value string) (string, error) { return f(value) }

type auditStub struct {
	events []*auditdomain.Event
	err    error
}

func (s *auditStub) Record(_ context.Context, event *auditdomain.Event) error {
	s.events = append(s.events, event)
	return s.err
}
func (s *auditStub) RecordBatch(context.Context, []*auditdomain.Event) error { return nil }

func TestInputRules(t *testing.T) {
	project, folder := Scope{Type: "project", ID: uuid.New()}, Scope{Type: "folder", ID: uuid.New()}
	for _, test := range []struct {
		name, keyword string
		scopes        []Scope
		envs          []string
		want          error
	}{
		{"key substring", "DOMAIN", nil, nil, nil},
		{"chinese", "数据库", nil, nil, nil},
		{"browse", "", nil, nil, nil},
		{"two chars", "ab", nil, nil, ErrShortKeyword},
		{"two chinese", "密码", nil, nil, ErrShortKeyword},
		{"separated short tokens", "a_b_c", nil, nil, ErrShortKeyword},
		{"short scoped", "密码", []Scope{project}, []string{"dev"}, nil},
		{"short across folders", "%", []Scope{folder}, nil, nil},
		{"unknown scope", "DOMAIN", []Scope{{Type: "unknown", ID: uuid.New()}}, nil, ErrInvalidInput},
		{"nil scope ID", "DOMAIN", []Scope{{Type: "tenant"}}, nil, ErrInvalidInput},
		{"global environment", "DOMAIN", nil, []string{"prod"}, ErrEnvironment},
		{"empty environment", "DOMAIN", []Scope{project}, []string{" "}, ErrInvalidInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeInput(Input{Scopes: test.scopes, EnvList: test.envs, Keyword: test.keyword, PageNum: 1, PageSize: 20, UserID: "reader"})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
	in, err := normalizeInput(Input{Scopes: []Scope{project, project}, EnvList: []string{"dev", " dev "}, Keyword: "  DOMAIN ", PageNum: 1, PageSize: 20, UserID: "reader"})
	if err != nil || len(in.Scopes) != 1 || len(in.EnvList) != 1 || in.Keyword != "DOMAIN" {
		t.Fatalf("unexpected normalization: %+v %v", in, err)
	}
}

func TestSearchDoesNotReturnPartialPlaintextAndAuditsSafely(t *testing.T) {
	audit := &auditStub{}
	decryptCalls := 0
	svc := &Service{timeout: time.Second, audit: audit,
		repo: readerFunc(func(context.Context, Input) (storedPage, error) {
			return storedPage{Total: 3, Groups: []storedGroup{{Secret: Secret{GroupID: uuid.New()}, Environments: []storedValue{{Ciphertext: "first"}, {Ciphertext: "bad"}}}}}, nil
		}),
		cipher: decryptFunc(func(value string) (string, error) {
			decryptCalls++
			if value == "bad" {
				return "", errors.New("sensitive-ciphertext")
			}
			return "sensitive-plaintext", nil
		}),
	}
	result, err := svc.Search(context.Background(), Input{Keyword: "sensitive-query", PageNum: 1, PageSize: 1, UserID: "reader"})
	if !errors.Is(err, ErrDecrypt) || len(result.List) != 0 || decryptCalls != 2 {
		t.Fatalf("partial result or wrong error: %+v %v", result, err)
	}
	if len(audit.events) != 1 || audit.events[0].ResultCode != auditdomain.ResultFailure {
		t.Fatal("missing failure audit")
	}
	encoded, _ := json.Marshal(audit.events)
	if strings.Contains(string(encoded), "sensitive-") {
		t.Fatal("audit exposed query or secret")
	}
	// 成功读取后审计失败同样不能把本页值交给调用者
	svc.cipher = decryptFunc(func(string) (string, error) { return "plaintext", nil })
	audit.err = errors.New("audit unavailable")
	result, err = svc.Search(context.Background(), Input{PageNum: 1, PageSize: 1, UserID: "reader"})
	if err == nil || len(result.List) != 0 {
		t.Fatal("audit failure returned plaintext")
	}
}

func TestSearchTimeoutAndCancellation(t *testing.T) {
	svc := &Service{timeout: 10 * time.Millisecond, repo: readerFunc(func(ctx context.Context, _ Input) (storedPage, error) { <-ctx.Done(); return storedPage{}, ctx.Err() })}
	_, err := svc.Search(context.Background(), Input{PageNum: 1, PageSize: 20, UserID: "reader"})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout not translated: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.Search(ctx, Input{PageNum: 1, PageSize: 20, UserID: "reader"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}
