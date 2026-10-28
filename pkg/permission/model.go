// Package permission provides a registered strategy factory and ordered,
// all-required permission chains. Business rules and identity loading are supplied
// by the application; the package provides no implicit allow strategy.
package permission

import "context"

const DefaultOrder = 100

type Resource struct {
	Type string
	ID   string
}

type Subject struct {
	Type string
	ID   string
}

// Values are copied at boundaries. Nested values must be immutable or independent
// snapshots supplied by the caller. Never put tokens or secret values here.
type Values map[string]any

// Keep extension data out of incidental formatted diagnostics.
func (Values) String() string   { return "<permission values>" }
func (Values) GoString() string { return "<permission values>" }

type Request struct {
	Resource   Resource
	Action     string
	Parameters Values
}

type Context struct {
	Subject    Subject
	Request    Request
	Attributes Values
}

type Outcome string

const (
	Allow Outcome = "ALLOW"
	Deny  Outcome = "DENY"
	Error Outcome = "ERROR"
)

// DENY and ERROR require a stable reason code and a sanitized public message.
// A zero Decision is invalid, not permission to proceed.
type Decision struct {
	Outcome    Outcome
	ReasonCode string
	Message    string
}

type Node struct {
	Key         string
	StrategyKey string
	Order       *int // nil inherits the strategy order; zero and negative are valid.
	Options     Values
}

// Key is a server-defined business keyword, not a client-selected strategy.
// Each resource type/action pair must have exactly one chain definition.
type ChainDefinition struct {
	Key          string
	ResourceType string
	Action       string
	Nodes        []Node
}

// Strategies must be safe for concurrent reuse and must honor ctx cancellation.
// A returned error (including a permission Failure) is an evaluation failure,
// never a DENY decision. Use Decision{Outcome: Deny, ...} for a normal refusal.
type Strategy interface {
	Key() string
	Check(ctx context.Context, input Context, node Node) (Decision, error)
}

// OrderedStrategy and NodeValidator are optional startup-only extensions.
type OrderedStrategy interface {
	Order() int
}

type NodeValidator interface {
	Validate(node Node) error
}

// ContextFactory must obtain a trusted authenticated subject and verify resource
// facts. Parameters are untrusted input and must not overwrite trusted attributes.
// It must preserve the request's resource and action, and honor ctx cancellation.
type ContextFactory interface {
	Create(ctx context.Context, request Request) (Context, error)
}

type ContextFactoryFunc func(context.Context, Request) (Context, error)

func (f ContextFactoryFunc) Create(ctx context.Context, request Request) (Context, error) {
	return f(ctx, request)
}
