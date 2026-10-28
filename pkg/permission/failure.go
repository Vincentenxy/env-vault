package permission

import "errors"

type FailureKind string

const (
	AuthenticationFailure FailureKind = "authentication"
	DeniedFailure         FailureKind = "denied"
	EvaluationFailure     FailureKind = "evaluation"
)

// Failure keeps internal causes separate from its safe public message. It does
// not unwrap the cause: a strategy error must not masquerade as a normal denial.
type Failure struct {
	kind       FailureKind
	reasonCode string
	message    string
	cause      error
}

func (e *Failure) Error() string      { return e.message }
func (e *Failure) Kind() FailureKind  { return e.kind }
func (e *Failure) ReasonCode() string { return e.reasonCode }
func (e *Failure) Cause() error       { return e.cause }

// Constructors are also available to context factories. Strategies return normal
// refusals as decisions, not these errors. Messages must not contain raw input.
func Unauthenticated(reasonCode, message string) *Failure {
	return newFailure(AuthenticationFailure, reasonCode, message, nil)
}

func Denied(reasonCode, message string) *Failure {
	return newFailure(DeniedFailure, reasonCode, message, nil)
}

func EvaluationError(reasonCode, message string, cause error) *Failure {
	return newFailure(EvaluationFailure, reasonCode, message, cause)
}

func newFailure(kind FailureKind, reasonCode, message string, cause error) *Failure {
	if blank(reasonCode) || blank(message) {
		return &Failure{kind: EvaluationFailure, reasonCode: "INVALID_FAILURE", message: "Permission evaluation failed", cause: cause}
	}
	return &Failure{kind: kind, reasonCode: reasonCode, message: message, cause: cause}
}

func evaluation(reason string, cause error) *Failure {
	return EvaluationError(reason, "Permission evaluation failed", cause)
}

func contextFailure(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) && failure != nil {
		switch failure.kind {
		case AuthenticationFailure, DeniedFailure, EvaluationFailure:
			if !blank(failure.reasonCode) && !blank(failure.message) {
				return failure
			}
		}
	}
	return evaluation("CONTEXT_FAILED", err)
}
