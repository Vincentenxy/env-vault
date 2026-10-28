// Package biz contains Env Vault business strategies for permission chains.
package biz

import (
	"context"
	"errors"

	"env-vault/pkg/permission"
)

var ErrBackendNotImplemented = errors.New("backend permission check is not implemented")

// CheckRequest describes the authenticated subject and the operation to authorize.
// It is an internal contract, not the future backend's HTTP request format.
type CheckRequest struct {
	Subject  permission.Subject
	Resource permission.Resource
	Action   string
}

// CheckPermissionFunc must honor ctx and return an error when no reliable
// decision is available. A false result with no error means an explicit denial.
type CheckPermissionFunc func(ctx context.Context, request CheckRequest) (bool, error)

// CheckBackendPermission is the integration point for the backend permission API.
// Until that API is integrated, it always reports an evaluation failure.
func CheckBackendPermission(ctx context.Context, request CheckRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// TODO: Call the backend permission API with ctx and map its explicit result.
	return false, ErrBackendNotImplemented
}
