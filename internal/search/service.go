package search

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/page"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Service 对外只暴露 Search，将匹配、分页、解密和审计留在独立模块
type Service struct {
	repo    reader
	cipher  Decryptor
	audit   auditdomain.Recorder
	timeout time.Duration
}

// NewService 的超时值由统一配置模块提供，不影响其他接口的数据库超时
func NewService(db *gorm.DB, cipher Decryptor, tags TagReader, audit auditdomain.Recorder, timeout time.Duration) *Service {
	return &Service{repo: &repository{db: db, tags: tags}, cipher: cipher, audit: audit, timeout: timeout}
}

// Search 先查当前页元数据与密文，再解密本页，任何失败都不返回部分结果
func (s *Service) Search(ctx context.Context, in Input) (result page.Response[Secret], resultErr error) {
	in, err := normalizeInput(in)
	if err != nil {
		return result, err
	}
	started := time.Now()
	defer func() {
		if s.audit == nil {
			return
		}
		event := &auditdomain.Event{
			ActionCode: "secret.search", ResourceType: "secret", ResultCode: auditdomain.ResultSuccess,
			CreateBy: in.UserID,
			EventDetail: map[string]any{"scopeCount": len(in.Scopes), "environmentFilterCount": len(in.EnvList),
				"keywordLength": utf8.RuneCountInString(in.Keyword), "pageNum": in.PageNum, "pageSize": in.PageSize,
				"resultCount": len(result.List), "durationMs": time.Since(started).Milliseconds()},
		}
		if resultErr != nil {
			event.ResultCode = auditdomain.ResultFailure
			event.FailureCode = "search_failed"
			event.FailureReason = "secret search failed"
		}
		// 不记录关键词、值或底层错误文本，审计失败时也不放行明文结果
		if err := s.audit.Record(ctx, event); err != nil {
			result = page.Response[Secret]{}
			resultErr = errors.Join(resultErr, err)
		}
	}()
	queryCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	stored, err := s.repo.read(queryCtx, in)
	if err != nil {
		if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
			return result, ErrTimeout
		}
		return result, err
	}
	items := make([]Secret, 0, len(stored.Groups))
	for _, group := range stored.Groups {
		item := group.Secret
		item.Values = make([]EnvironmentValue, 0, len(group.Environments))
		for _, env := range group.Environments {
			if err := queryCtx.Err(); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return result, ErrTimeout
				}
				return result, err
			}
			value, err := s.cipher.Decrypt(env.Ciphertext)
			if err != nil {
				return result, ErrDecrypt
			}
			env.EnvironmentValue.Value = value
			item.Values = append(item.Values, env.EnvironmentValue)
		}
		items = append(items, item)
	}
	if err := queryCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return result, ErrTimeout
		}
		return result, err
	}
	return page.Response[Secret]{Total: stored.Total, List: items}, nil
}

// normalizeInput 仅归一化检索条件，分页默认值与上限仍只在 Handler 处理
func normalizeInput(in Input) (Input, error) {
	in.Keyword = strings.TrimSpace(in.Keyword)
	if strings.TrimSpace(in.UserID) == "" || in.PageNum < 1 || in.PageSize < 1 ||
		in.PageNum-1 > math.MaxInt/in.PageSize || utf8.RuneCountInString(in.Keyword) > 256 ||
		strings.ContainsRune(in.Keyword, 0) || len(in.Scopes) > 100 || len(in.EnvList) > 200 {
		return in, ErrInvalidInput
	}
	scopes := make([]Scope, 0, len(in.Scopes))
	seen := make(map[Scope]bool)
	narrow := len(in.Scopes) > 0
	for _, scope := range in.Scopes {
		if scope.ID == uuid.Nil {
			return in, ErrInvalidInput
		}
		switch scope.Type {
		case "tenant", "org":
			narrow = false
		case "project", "folder":
		default:
			return in, ErrInvalidInput
		}
		if !seen[scope] {
			scopes = append(scopes, scope)
			seen[scope] = true
		}
	}
	in.Scopes = scopes
	envs := make([]string, 0, len(in.EnvList))
	envSeen := make(map[string]bool)
	for _, env := range in.EnvList {
		env = strings.TrimSpace(env)
		if env == "" || strings.ContainsRune(env, 0) {
			return in, ErrInvalidInput
		}
		if !envSeen[env] {
			envs = append(envs, env)
			envSeen[env] = true
		}
	}
	in.EnvList = envs
	if !narrow && len(envs) > 0 {
		return in, ErrEnvironment
	}
	if in.Keyword != "" && !hasSearchTrigram(in.Keyword) && !narrow {
		return in, ErrShortKeyword
	}
	return in, nil
}

// 连续三个文字或数字才放行大范围包含查询，分隔符两边的短词不凑长度
// 这是保守的入口规则，不保证优化器对每个三字查询都选择 GIN 索引
func hasSearchTrigram(keyword string) bool {
	consecutive := 0
	for _, ch := range keyword {
		if unicode.IsLetter(ch) || unicode.IsNumber(ch) {
			consecutive++
			if consecutive >= 3 {
				return true
			}
		} else {
			consecutive = 0
		}
	}
	return false
}

// 使用独立的转义字符，确保 DB_HOST、100% 与用户输入的 ! 均为字面匹配
func containsPattern(keyword string) string {
	return "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(keyword) + "%"
}
