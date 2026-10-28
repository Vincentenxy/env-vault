package permission

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
)

type compiledNode struct {
	definition Node
	strategy   Strategy
	order      int
}

type compiledChain struct {
	resourceType string
	action       string
	nodes        []compiledNode
}

// Factory is immutable after construction and safe for concurrent checks when
// its supplied strategies and context factory are also concurrency-safe.
type Factory struct {
	contexts ContextFactory
	chains   map[string]compiledChain
}

// NewFactory validates all definitions and compiles stable execution order once.
// It neither registers global state nor attaches rules to existing endpoints.
func NewFactory(contexts ContextFactory, strategies []Strategy, definitions []ChainDefinition) (factory *Factory, err error) {
	defer func() {
		if recover() != nil {
			factory, err = nil, errors.New("permission registration panicked")
		}
	}()
	if nilValue(contexts) || len(strategies) == 0 || len(definitions) == 0 {
		return nil, errors.New("permission context factory, strategies and chains are required")
	}
	type registration struct {
		strategy Strategy
		order    int
	}
	registered := make(map[string]registration, len(strategies))
	for _, strategy := range strategies {
		if nilValue(strategy) {
			return nil, errors.New("nil permission strategy")
		}
		key := strategy.Key()
		if blank(key) {
			return nil, errors.New("empty permission strategy key")
		}
		if _, exists := registered[key]; exists {
			return nil, fmt.Errorf("duplicate permission strategy %q", key)
		}
		order := DefaultOrder
		if ordered, ok := strategy.(OrderedStrategy); ok {
			order = ordered.Order()
		}
		registered[key] = registration{strategy, order}
	}
	chains := make(map[string]compiledChain, len(definitions))
	type route struct{ resourceType, action string }
	routes := make(map[route]bool, len(definitions))
	for _, definition := range definitions {
		if blank(definition.Key) || blank(definition.ResourceType) || blank(definition.Action) || len(definition.Nodes) == 0 {
			return nil, errors.New("permission chain requires key, resource type, action and nodes")
		}
		if _, exists := chains[definition.Key]; exists {
			return nil, fmt.Errorf("duplicate permission chain %q", definition.Key)
		}
		target := route{definition.ResourceType, definition.Action}
		if routes[target] {
			return nil, fmt.Errorf("duplicate permission route for %q and %q", target.resourceType, target.action)
		}
		routes[target] = true
		chain := compiledChain{resourceType: target.resourceType, action: target.action}
		nodeKeys := make(map[string]bool, len(definition.Nodes))
		for _, node := range definition.Nodes {
			if blank(node.Key) || nodeKeys[node.Key] {
				return nil, fmt.Errorf("empty or duplicate node key in chain %q", definition.Key)
			}
			nodeKeys[node.Key] = true
			entry, exists := registered[node.StrategyKey]
			if !exists {
				return nil, fmt.Errorf("unknown strategy %q in chain %q", node.StrategyKey, definition.Key)
			}
			if !validValues(node.Options) {
				return nil, fmt.Errorf("invalid options in node %q", node.Key)
			}
			node = copyNode(node)
			order := entry.order
			if node.Order != nil {
				order = *node.Order
			}
			if validator, ok := entry.strategy.(NodeValidator); ok {
				if err := validator.Validate(copyNode(node)); err != nil {
					// Do not echo options or a validator's potentially sensitive error.
					return nil, fmt.Errorf("invalid permission options for chain %q node %q", definition.Key, node.Key)
				}
			}
			chain.nodes = append(chain.nodes, compiledNode{node, entry.strategy, order})
		}
		sort.SliceStable(chain.nodes, func(i, j int) bool { return chain.nodes[i].order < chain.nodes[j].order })
		chains[definition.Key] = chain
	}
	return &Factory{contexts: contexts, chains: chains}, nil
}

func blank(value string) bool { return strings.TrimSpace(value) == "" }

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}

func validValues(values Values) bool {
	for key, value := range values {
		if blank(key) || nilValue(value) {
			return false
		}
	}
	return true
}

func copyNode(node Node) Node {
	node.Options = maps.Clone(node.Options)
	if node.Order != nil {
		order := *node.Order
		node.Order = &order
	}
	return node
}

func copyRequest(request Request) Request {
	request.Parameters = maps.Clone(request.Parameters)
	return request
}

func copyContext(input Context) Context {
	input.Request = copyRequest(input.Request)
	input.Attributes = maps.Clone(input.Attributes)
	return input
}
