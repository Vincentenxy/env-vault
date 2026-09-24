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

// Service 集中维护密钥检索和标签候选，将分页、解密和审计留在模块内部
type Service struct {
	repo       reader
	tagOptions tagOptionReader
	cipher     Decryptor
	audit      auditdomain.Recorder
	timeout    time.Duration
}

// NewService 的超时值由统一配置模块提供，不影响其他接口的数据库超时
func NewService(db *gorm.DB, cipher Decryptor, tags TagReader, audit auditdomain.Recorder, timeout time.Duration) *Service {
	repo := &repository{db: db, tags: tags}
	return &Service{repo: repo, tagOptions: repo, cipher: cipher, audit: audit, timeout: timeout}
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
			EventDetail: map[string]any{"scopeCount": len(in.Scopes), "environmentFilterCount": len(in.EnvList), "tagFilterCount": len(in.TagIDs),
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

// ListTagOptions 使用独立分页查询标签元数据，不读取或解密密钥值
func (s *Service) ListTagOptions(ctx context.Context, in TagOptionInput) (page.Response[TagOption], error) {
	var result page.Response[TagOption]
	in, err := normalizeTagOptionInput(in)
	if err != nil {
		return result, err
	}
	if s.tagOptions == nil {
		return result, ErrInvalidInput
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	result, err = s.tagOptions.listTagOptions(queryCtx, in)
	if err != nil && errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
		return page.Response[TagOption]{}, ErrTimeout
	}
	return result, err
}

// normalizeInput 仅归一化检索条件，分页默认值与上限仍只在 Handler 处理
func normalizeInput(in Input) (Input, error) {
	in.Keyword = strings.TrimSpace(in.Keyword)
	if strings.TrimSpace(in.UserID) == "" || in.PageNum < 1 || in.PageSize < 1 ||
		in.PageNum-1 > math.MaxInt/in.PageSize || utf8.RuneCountInString(in.Keyword) > 256 ||
		strings.ContainsRune(in.Keyword, 0) || len(in.Scopes) > 100 || len(in.EnvList) > 200 || len(in.TagIDs) > 100 {
		return in, ErrInvalidInput
	}
	scopes, envs, narrow, err := normalizeScopeEnvironment(in.Scopes, in.EnvList)
	if err != nil {
		return in, err
	}
	in.Scopes, in.EnvList = scopes, envs
	tags := make([]uuid.UUID, 0, len(in.TagIDs))
	tagSeen := make(map[uuid.UUID]bool)
	for _, id := range in.TagIDs {
		if id == uuid.Nil {
			return in, ErrInvalidInput
		}
		if !tagSeen[id] {
			tags = append(tags, id)
			tagSeen[id] = true
		}
	}
	in.TagIDs = tags
	if in.Keyword != "" && !hasSearchTrigram(in.Keyword) && !narrow {
		return in, ErrShortKeyword
	}
	return in, nil
}

// normalizeTagOptionInput 允许标签候选使用短关键词，但范围和环境规则与密钥搜索一致
func normalizeTagOptionInput(in TagOptionInput) (TagOptionInput, error) {
	in.Keyword = strings.TrimSpace(in.Keyword)
	if strings.TrimSpace(in.UserID) == "" || in.PageNum < 1 || in.PageSize < 1 ||
		in.PageNum-1 > math.MaxInt/in.PageSize || utf8.RuneCountInString(in.Keyword) > 128 ||
		strings.ContainsRune(in.Keyword, 0) || len(in.Scopes) > 100 || len(in.EnvList) > 200 {
		return in, ErrInvalidInput
	}
	scopes, envs, _, err := normalizeScopeEnvironment(in.Scopes, in.EnvList)
	if err != nil {
		return in, err
	}
	in.Scopes, in.EnvList = scopes, envs
	return in, nil
}

// normalizeScopeEnvironment 统一去重结构条件，实际资源归属仍由仓储读取数据库校验
func normalizeScopeEnvironment(inputScopes []Scope, inputEnvs []string) ([]Scope, []string, bool, error) {
	scopes := make([]Scope, 0, len(inputScopes))
	seen := make(map[Scope]bool)
	narrow := len(inputScopes) > 0
	for _, scope := range inputScopes {
		if scope.ID == uuid.Nil {
			return nil, nil, false, ErrInvalidInput
		}
		switch scope.Type {
		case "tenant", "org":
			narrow = false
		case "project", "folder":
		default:
			return nil, nil, false, ErrInvalidInput
		}
		if !seen[scope] {
			scopes = append(scopes, scope)
			seen[scope] = true
		}
	}
	envs := make([]string, 0, len(inputEnvs))
	envSeen := make(map[string]bool)
	for _, env := range inputEnvs {
		env = strings.TrimSpace(env)
		if env == "" || strings.ContainsRune(env, 0) {
			return nil, nil, false, ErrInvalidInput
		}
		if !envSeen[env] {
			envs = append(envs, env)
			envSeen[env] = true
		}
	}
	if !narrow && len(envs) > 0 {
		return nil, nil, false, ErrEnvironment
	}
	return scopes, envs, narrow, nil
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
