package permission

import (
	"context"
	"errors"
	"maps"
	"time"

	"go.uber.org/zap"

	"env-vault/pkg/logger"
)

// Check executes the chain registered for a server-owned business keyword.
// Supply zero or one parameter map. Success only authorizes this resource/action;
// it is not a list filter or authorization for other resources in the request.
func (f *Factory) Check(ctx context.Context, keyword, resourceID string, parameters ...Values) (err error) {
	start := time.Now()
	logCtx := ctx
	if nilValue(logCtx) {
		logCtx = nil
	}
	var subject Subject
	var request Request
	defer func() {
		fields := []zap.Field{
			zap.String("chainKey", keyword), zap.String("resourceId", resourceID),
			zap.String("resourceType", request.Resource.Type), zap.String("action", request.Action),
			zap.String("subjectType", subject.Type), zap.String("subjectId", subject.ID),
			zap.Duration("duration", time.Since(start)),
		}
		if err == nil {
			logger.Info(logCtx, "permission chain allowed", fields...)
			return
		}
		var failure *Failure
		if errors.As(err, &failure) && failure != nil {
			fields = append(fields, zap.String("reasonCode", failure.ReasonCode()), zap.String("failureKind", string(failure.Kind())))
		}
		logger.Warn(logCtx, "permission chain failed", fields...)
	}()
	if f == nil || nilValue(ctx) || nilValue(f.contexts) {
		return evaluation("INVALID_RUNTIME", nil)
	}
	if err := ctx.Err(); err != nil {
		return evaluation("CONTEXT_CANCELED", err)
	}
	chain, found := f.chains[keyword]
	if !found {
		return evaluation("CHAIN_NOT_FOUND", nil)
	}
	if blank(resourceID) || resourceID == "*" || len(parameters) > 1 {
		return evaluation("INVALID_REQUEST", nil)
	}
	request = Request{Resource: Resource{Type: chain.resourceType, ID: resourceID}, Action: chain.action}
	if len(parameters) == 1 {
		if !validValues(parameters[0]) {
			return evaluation("INVALID_PARAMETERS", nil)
		}
		request.Parameters = maps.Clone(parameters[0])
	}
	input, err, panicStack := createContext(ctx, f.contexts, copyRequest(request))
	if err != nil {
		failure := contextFailure(err)
		if failure.Kind() == EvaluationFailure {
			if panicStack.Key == "" {
				panicStack = zap.Stack("stack")
			}
			logger.Error(ctx, "permission context creation failed", zap.String("chainKey", keyword), zap.String("reasonCode", failure.ReasonCode()), panicStack)
		}
		return failure
	}
	if err := ctx.Err(); err != nil {
		return evaluation("CONTEXT_CANCELED", err)
	}
	if blank(input.Subject.Type) || blank(input.Subject.ID) {
		return Unauthenticated("SUBJECT_REQUIRED", "Authentication required")
	}
	subject = input.Subject
	if input.Request.Resource != request.Resource || input.Request.Action != request.Action {
		return evaluation("TARGET_CHANGED", nil)
	}
	if !validValues(input.Attributes) || !validValues(input.Request.Parameters) {
		return evaluation("INVALID_CONTEXT", nil)
	}
	input = copyContext(input)
	for _, node := range chain.nodes {
		if err := ctx.Err(); err != nil {
			return evaluation("CONTEXT_CANCELED", err)
		}
		start := time.Now()
		decision, checkErr, panicStack := checkStrategy(ctx, node.strategy, copyContext(input), copyNode(node.definition))
		fields := []zap.Field{
			zap.String("chainKey", keyword), zap.String("nodeKey", node.definition.Key),
			zap.String("strategyKey", node.definition.StrategyKey), zap.Int("order", node.order),
			zap.String("resourceType", input.Request.Resource.Type), zap.String("action", input.Request.Action),
			zap.String("subjectType", input.Subject.Type), zap.String("subjectId", input.Subject.ID),
			zap.Duration("duration", time.Since(start)),
		}
		if checkErr != nil {
			if panicStack.Key == "" {
				panicStack = zap.Stack("stack")
			}
			logger.Error(ctx, "permission strategy failed", append(fields, panicStack)...)
			return evaluation("STRATEGY_FAILED", checkErr)
		}
		if err := ctx.Err(); err != nil {
			return evaluation("CONTEXT_CANCELED", err)
		}
		switch decision.Outcome {
		case Allow:
			logger.Debug(ctx, "permission node allowed", fields...)
		case Deny, Error:
			if blank(decision.ReasonCode) || blank(decision.Message) {
				return evaluation("INVALID_DECISION", nil)
			}
			logger.Warn(ctx, "permission node rejected", append(fields, zap.String("reasonCode", decision.ReasonCode))...)
			if decision.Outcome == Deny {
				return Denied(decision.ReasonCode, decision.Message)
			}
			return EvaluationError(decision.ReasonCode, decision.Message, nil)
		default:
			return evaluation("INVALID_DECISION", nil)
		}
	}
	return nil
}

func createContext(ctx context.Context, factory ContextFactory, request Request) (input Context, err error, panicStack zap.Field) {
	defer func() {
		if value := recover(); value != nil {
			// Capture before recovery unwinds the extension's panic frames.
			panicStack = zap.Stack("stack")
			err = evaluation("CONTEXT_PANIC", panicCause(value))
		}
	}()
	input, err = factory.Create(ctx, request)
	return
}

func checkStrategy(ctx context.Context, strategy Strategy, input Context, node Node) (decision Decision, err error, panicStack zap.Field) {
	defer func() {
		if value := recover(); value != nil {
			panicStack = zap.Stack("stack")
			err = panicCause(value)
		}
	}()
	decision, err = strategy.Check(ctx, input, node)
	return
}

func panicCause(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return errors.New("permission extension panicked")
}
