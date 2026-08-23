// Package application 应用层：编排领域对象完成用例，定义应用服务与 DTO。
package application

import (
	"context"
	"strings"
)

// NicknameResolver 提供按外部用户 ID 查询用户姓名的能力。
type NicknameResolver interface {
	GetNickname(ctx context.Context, userID string) (string, error)
}

// ResolveNickname 查询用户姓名；未配置、用户 ID 为空或查询失败时返回空字符串。
func ResolveNickname(ctx context.Context, resolver NicknameResolver, userID string) string {
	userID = strings.TrimSpace(userID)
	if resolver == nil || userID == "" {
		return ""
	}
	name, err := resolver.GetNickname(ctx, userID)
	if err != nil {
		return ""
	}
	return name
}
