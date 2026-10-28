package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"

	"env-vault/pkg/logger"
	"env-vault/pkg/permission"
)

type strategy struct {
	key   string
	check func(context.Context, permission.Context, permission.Node) (permission.Decision, error)
}

type nilContext struct{ context.Context }

func (s *strategy) Key() string { return s.key }
func (s *strategy) Check(ctx context.Context, input permission.Context, node permission.Node) (permission.Decision, error) {
	if s.check != nil {
		return s.check(ctx, input, node)
	}
	return permission.Decision{Outcome: permission.Allow}, nil
}

type configuredStrategy struct {
	strategy
	order    int
	validate func(permission.Node) error
}

func (s *configuredStrategy) Order() int { return s.order }
func (s *configuredStrategy) Validate(n permission.Node) error {
	if s.validate != nil {
		return s.validate(n)
	}
	return nil
}

func order(n int) *int { return &n }

func identity(_ context.Context, r permission.Request) (permission.Context, error) {
	return permission.Context{Subject: permission.Subject{Type: "user", ID: "tester"}, Request: r}, nil
}

func definition(nodes ...permission.Node) permission.ChainDefinition {
	return permission.ChainDefinition{Key: "document.read", ResourceType: "document", Action: "read", Nodes: nodes}
}

func build(t *testing.T, factory permission.ContextFactory, strategies []permission.Strategy, nodes ...permission.Node) *permission.Factory {
	t.Helper()
	f, err := permission.NewFactory(factory, strategies, []permission.ChainDefinition{definition(nodes...)})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func failure(t *testing.T, err error, kind permission.FailureKind, reason string) *permission.Failure {
	t.Helper()
	var result *permission.Failure
	if !errors.As(err, &result) || result == nil || result.Kind() != kind || result.ReasonCode() != reason {
		t.Fatalf("failure = %#v, want %s/%s", err, kind, reason)
	}
	return result
}

func TestRegistrationRejectsInvalidConfiguration(t *testing.T) {
	base := &strategy{key: "check"}
	baseNode := permission.Node{Key: "first", StrategyKey: "check"}
	for _, name := range []string{
		"nil factory", "typed nil factory", "no strategies", "nil strategy", "typed nil strategy", "empty strategy key",
		"duplicate strategy", "no chains", "empty chain key", "empty resource", "empty action", "empty chain",
		"duplicate chain", "duplicate route", "empty node key", "duplicate node", "unknown strategy", "nil option", "invalid options", "validator panic",
	} {
		t.Run(name, func(t *testing.T) {
			var contexts permission.ContextFactory = permission.ContextFactoryFunc(identity)
			strategies := []permission.Strategy{base}
			definitions := []permission.ChainDefinition{definition(baseNode)}
			switch name {
			case "nil factory":
				contexts = nil
			case "typed nil factory":
				contexts = permission.ContextFactoryFunc(nil)
			case "no strategies":
				strategies = nil
			case "nil strategy":
				strategies = []permission.Strategy{nil}
			case "typed nil strategy":
				strategies = []permission.Strategy{(*strategy)(nil)}
			case "empty strategy key":
				strategies = []permission.Strategy{&strategy{}}
			case "duplicate strategy":
				strategies = append(strategies, base)
			case "no chains":
				definitions = nil
			case "empty chain key":
				definitions[0].Key = " "
			case "empty resource":
				definitions[0].ResourceType = ""
			case "empty action":
				definitions[0].Action = ""
			case "empty chain":
				definitions[0].Nodes = nil
			case "duplicate chain":
				definitions = append(definitions, definitions[0])
			case "duplicate route":
				other := definitions[0]
				other.Key = "alias"
				definitions = append(definitions, other)
			case "empty node key":
				definitions[0].Nodes[0].Key = ""
			case "duplicate node":
				definitions[0].Nodes = append(definitions[0].Nodes, baseNode)
			case "unknown strategy":
				definitions[0].Nodes[0].StrategyKey = "missing"
			case "nil option":
				definitions[0].Nodes[0].Options = permission.Values{"x": (*int)(nil)}
			case "invalid options", "validator panic":
				strategies = []permission.Strategy{&configuredStrategy{strategy: *base, validate: func(permission.Node) error {
					if name == "validator panic" {
						panic("sensitive-data")
					}
					return errors.New("sensitive-data")
				}}}
			}
			f, err := permission.NewFactory(contexts, strategies, definitions)
			if err == nil || f != nil {
				t.Fatal("invalid registration must not produce a usable factory")
			}
		})
	}
}

func TestStableOrderAndConfigurationSnapshots(t *testing.T) {
	var calls []string
	validations := 0
	s := &configuredStrategy{strategy: strategy{key: "check"}, order: 20}
	s.check = func(_ context.Context, _ permission.Context, node permission.Node) (permission.Decision, error) {
		calls = append(calls, node.Key)
		if node.Key == "default" && node.Options["rule"] != "original" {
			t.Error("node options changed after registration or a prior check")
		}
		node.Options["rule"] = "changed by strategy"
		return permission.Decision{Outcome: permission.Allow}, nil
	}
	s.validate = func(n permission.Node) error { validations++; n.Options["rule"] = "changed by validator"; return nil }
	nodes := []permission.Node{
		{Key: "default", StrategyKey: "check", Options: permission.Values{"rule": "original"}},
		{Key: "negative", StrategyKey: "check", Order: order(-1), Options: permission.Values{}},
		{Key: "zero", StrategyKey: "check", Order: order(0), Options: permission.Values{}},
		{Key: "same", StrategyKey: "check", Order: order(20), Options: permission.Values{}},
	}
	f := build(t, permission.ContextFactoryFunc(identity), []permission.Strategy{s}, nodes...)
	*nodes[1].Order = 999
	nodes[0].Options["rule"] = "changed by caller"
	for i := 0; i < 2; i++ {
		if err := f.Check(context.Background(), "document.read", "doc-1"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"negative", "zero", "default", "same", "negative", "zero", "default", "same"}
	if !reflect.DeepEqual(calls, want) || validations != 4 {
		t.Fatalf("calls=%v validations=%d", calls, validations)
	}
}

func TestChainFailureStopsAllRemainingNodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision permission.Decision
		err      error
		kind     permission.FailureKind
		reason   string
	}{
		{"deny", permission.Decision{Outcome: permission.Deny, ReasonCode: "NOT_MEMBER", Message: "Not allowed"}, nil, permission.DeniedFailure, "NOT_MEMBER"},
		{"error decision", permission.Decision{Outcome: permission.Error, ReasonCode: "UNAVAILABLE", Message: "Unavailable"}, nil, permission.EvaluationFailure, "UNAVAILABLE"},
		{"empty decision", permission.Decision{}, nil, permission.EvaluationFailure, "INVALID_DECISION"},
		{"missing reason", permission.Decision{Outcome: permission.Deny}, nil, permission.EvaluationFailure, "INVALID_DECISION"},
		{"unknown outcome", permission.Decision{Outcome: "SKIP"}, nil, permission.EvaluationFailure, "INVALID_DECISION"},
		{"dependency error", permission.Decision{Outcome: permission.Allow}, errors.New("private response"), permission.EvaluationFailure, "STRATEGY_FAILED"},
		{"denial returned as error", permission.Decision{}, permission.Denied("DENIED", "Refused"), permission.EvaluationFailure, "STRATEGY_FAILED"},
		{"panic", permission.Decision{}, nil, permission.EvaluationFailure, "STRATEGY_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			s := &strategy{key: "check", check: func(_ context.Context, _ permission.Context, n permission.Node) (permission.Decision, error) {
				calls = append(calls, n.Key)
				if n.Key == "first" {
					return permission.Decision{Outcome: permission.Allow}, nil
				}
				if tc.name == "panic" {
					panic("private data")
				}
				return tc.decision, tc.err
			}}
			f := build(t, permission.ContextFactoryFunc(identity), []permission.Strategy{s},
				permission.Node{Key: "first", StrategyKey: "check"}, permission.Node{Key: "second", StrategyKey: "check"}, permission.Node{Key: "last", StrategyKey: "check"})
			got := failure(t, f.Check(context.Background(), "document.read", "doc-1"), tc.kind, tc.reason)
			if !reflect.DeepEqual(calls, []string{"first", "second"}) {
				t.Fatalf("chain did not stop: %v", calls)
			}
			if tc.err != nil && got.Cause() != tc.err {
				t.Error("internal cause must be retained")
			}
		})
	}
}

func TestContextFactoryValidation(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		kind         permission.FailureKind
	}{
		{"no subject", "SUBJECT_REQUIRED", permission.AuthenticationFailure},
		{"resource changed", "TARGET_CHANGED", permission.EvaluationFailure},
		{"type changed", "TARGET_CHANGED", permission.EvaluationFailure},
		{"action changed", "TARGET_CHANGED", permission.EvaluationFailure},
		{"nil attribute", "INVALID_CONTEXT", permission.EvaluationFailure},
		{"error", "CONTEXT_FAILED", permission.EvaluationFailure},
		{"panic", "CONTEXT_PANIC", permission.EvaluationFailure},
		{"unauthenticated", "NO_SESSION", permission.AuthenticationFailure},
		{"denied", "MISSING_RESOURCE", permission.DeniedFailure},
		{"zero failure", "CONTEXT_FAILED", permission.EvaluationFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := permission.ContextFactoryFunc(func(ctx context.Context, request permission.Request) (permission.Context, error) {
				input, _ := identity(ctx, request)
				switch tc.name {
				case "no subject":
					input.Subject = permission.Subject{}
				case "resource changed":
					input.Request.Resource.ID = "another"
				case "type changed":
					input.Request.Resource.Type = "another"
				case "action changed":
					input.Request.Action = "write"
				case "nil attribute":
					input.Attributes = permission.Values{"manager": nil}
				case "error":
					return input, errors.New("SQL with private data")
				case "panic":
					panic("private data")
				case "unauthenticated":
					return input, permission.Unauthenticated("NO_SESSION", "Authentication required")
				case "denied":
					return input, fmt.Errorf("wrapped: %w", permission.Denied("MISSING_RESOURCE", "Not available"))
				case "zero failure":
					return input, &permission.Failure{}
				}
				return input, nil
			})
			called := false
			s := &strategy{key: "check", check: func(context.Context, permission.Context, permission.Node) (permission.Decision, error) {
				called = true
				return permission.Decision{Outcome: permission.Allow}, nil
			}}
			f := build(t, factory, []permission.Strategy{s}, permission.Node{Key: "first", StrategyKey: "check"})
			failure(t, f.Check(context.Background(), "document.read", "doc-1"), tc.kind, tc.reason)
			if called {
				t.Fatal("invalid context must not reach any strategy")
			}
		})
	}
}

func TestInvalidCallsAndCancellation(t *testing.T) {
	called := false
	f := build(t, permission.ContextFactoryFunc(func(ctx context.Context, request permission.Request) (permission.Context, error) {
		called = true
		return identity(ctx, request)
	}), []permission.Strategy{&strategy{key: "check"}}, permission.Node{Key: "first", StrategyKey: "check"})
	failure(t, f.Check(nil, "document.read", "doc-1"), permission.EvaluationFailure, "INVALID_RUNTIME")
	failure(t, f.Check((*nilContext)(nil), "document.read", "doc-1"), permission.EvaluationFailure, "INVALID_RUNTIME")
	failure(t, (*permission.Factory)(nil).Check(context.Background(), "document.read", "doc-1"), permission.EvaluationFailure, "INVALID_RUNTIME")
	failure(t, f.Check(context.Background(), "missing", "doc-1"), permission.EvaluationFailure, "CHAIN_NOT_FOUND")
	for _, id := range []string{"", " ", "*"} {
		failure(t, f.Check(context.Background(), "document.read", id), permission.EvaluationFailure, "INVALID_REQUEST")
	}
	failure(t, f.Check(context.Background(), "document.read", "doc-1", nil, nil), permission.EvaluationFailure, "INVALID_REQUEST")
	failure(t, f.Check(context.Background(), "document.read", "doc-1", permission.Values{"x": nil}), permission.EvaluationFailure, "INVALID_PARAMETERS")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failure(t, f.Check(ctx, "document.read", "doc-1"), permission.EvaluationFailure, "CONTEXT_CANCELED")
	if called {
		t.Fatal("invalid or canceled calls must not load context")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	s := &strategy{key: "cancel", check: func(context.Context, permission.Context, permission.Node) (permission.Decision, error) {
		cancel()
		return permission.Decision{Outcome: permission.Allow}, nil
	}}
	f = build(t, permission.ContextFactoryFunc(identity), []permission.Strategy{s}, permission.Node{Key: "last", StrategyKey: "cancel"})
	failure(t, f.Check(ctx, "document.read", "doc-1"), permission.EvaluationFailure, "CONTEXT_CANCELED")
}

func TestConcurrentChecksKeepSubjectsAndFactsIsolated(t *testing.T) {
	type subjectKey struct{}
	contexts := permission.ContextFactoryFunc(func(ctx context.Context, request permission.Request) (permission.Context, error) {
		id := ctx.Value(subjectKey{}).(string)
		return permission.Context{Subject: permission.Subject{Type: "user", ID: id}, Request: request, Attributes: permission.Values{"owner": id}}, nil
	})
	s := &strategy{key: "check", check: func(_ context.Context, input permission.Context, node permission.Node) (permission.Decision, error) {
		if input.Attributes["owner"] != input.Subject.ID || input.Request.Resource.ID != "doc-"+input.Subject.ID || input.Request.Parameters["owner"] != "untrusted" || node.Options["mode"] != "original" {
			return permission.Decision{}, errors.New("request data leaked or was overwritten")
		}
		input.Attributes["owner"] = "mutated"
		input.Request.Parameters["owner"] = "mutated"
		node.Options["mode"] = "mutated"
		return permission.Decision{Outcome: permission.Allow}, nil
	}}
	chain := definition(permission.Node{Key: "one", StrategyKey: "check", Options: permission.Values{"mode": "original"}}, permission.Node{Key: "two", StrategyKey: "check", Options: permission.Values{"mode": "original"}})
	other := chain
	other.Key, other.Action = "document.write", "write"
	f, err := permission.NewFactory(contexts, []permission.Strategy{s}, []permission.ChainDefinition{chain, other})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprint(i)
			ctx := context.WithValue(context.Background(), subjectKey{}, id)
			params := permission.Values{"owner": "untrusted"}
			for _, keyword := range []string{"document.read", "document.write"} {
				if err := f.Check(ctx, keyword, "doc-"+id, params); err != nil {
					t.Error(err)
				}
			}
			if params["owner"] != "untrusted" {
				t.Error("caller parameters mutated")
			}
		}()
	}
	wg.Wait()
}

func TestFormattedModelsExcludeExtensionValues(t *testing.T) {
	values := permission.Values{"sensitive-key": "sensitive-value"}
	models := []any{
		values,
		permission.Request{Parameters: values},
		permission.Context{Request: permission.Request{Parameters: values}, Attributes: values},
		permission.Node{Options: values},
	}
	for _, model := range models {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, model), "sensitive") {
				t.Fatalf("%T exposed extension values in formatted diagnostics", model)
			}
		}
	}
}

func panicInContextFactory(context.Context, permission.Request) (permission.Context, error) {
	panic(errors.New("private-panic-marker"))
}

func panicInStrategy(context.Context, permission.Context, permission.Node) (permission.Decision, error) {
	panic("private-panic-marker")
}

func TestPanicLogsPreserveOrigin(t *testing.T) {
	const childEnv = "ENVVAULT_PERMISSION_PANIC_LOG_TEST"
	if os.Getenv(childEnv) == "1" {
		logger.Init("release")
		defer logger.Sync()
		for _, target := range []string{"context", "strategy"} {
			contexts := permission.ContextFactoryFunc(identity)
			s := &strategy{key: "check"}
			reason := "CONTEXT_PANIC"
			if target == "context" {
				contexts = panicInContextFactory
			} else {
				s.check = panicInStrategy
				reason = "STRATEGY_FAILED"
			}
			f := build(t, contexts, []permission.Strategy{s}, permission.Node{Key: "first", StrategyKey: "check"})
			ctx := context.WithValue(context.Background(), logger.TraceIDKey, "panic-review-trace")
			failure(t, f.Check(ctx, "document.read", "doc-1"), permission.EvaluationFailure, reason)
		}
		return
	}

	// Keep logger initialization and stdout capture isolated from other tests.
	cmd := exec.Command(os.Args[0], "-test.run=^TestPanicLogsPreserveOrigin$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("panic log probe failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "private-panic-marker") {
		t.Fatal("panic logs exposed the raw panic value")
	}
	want := map[string]string{
		"permission context creation failed": "panicInContextFactory",
		"permission strategy failed":         "panicInStrategy",
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var entry struct {
			Level   string `json:"level"`
			Message string `json:"msg"`
			Stack   string `json:"stack"`
			TraceID string `json:"traceId"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if origin, ok := want[entry.Message]; ok {
			if entry.Level != "ERROR" || entry.TraceID != "panic-review-trace" || !strings.Contains(entry.Stack, origin) {
				t.Errorf("panic log must retain trace ID and origin %q: %+v", origin, entry)
			}
			delete(want, entry.Message)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing panic logs: %v", want)
	}
}
