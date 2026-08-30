// Package secret 密钥应用层：用例编排与 DTO。
//
// 核心约定：业务上"一个 secret"物理上展开为每个环境各一条记录（value 各不相同），
// 共享同一 group_id。创建时按 folderGroupId + envId 定位各环境下 folder 的 id 落库，
// 查询/删除均按 group_id 聚合操作（与 folder_info 业务组模式一致）。
package secret

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	app "env-vault/internal/application"
	auditdomain "env-vault/internal/domain/audit"
	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
	secretdomain "env-vault/internal/domain/secret"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam         = errors.New("invalid param")
	ErrNotFound             = errors.New("secret not found")
	ErrFolderNotFound       = errors.New("folder not found")
	ErrEnvNotFound          = errors.New("env not found under folder")
	ErrKeyExists            = errors.New("secret key already exists under folder")
	ErrKeyPatternMismatch   = errors.New("secret key does not match folder key pattern")
	ErrFolderPatternInvalid = errors.New("folder key pattern is invalid or inconsistent")
	ErrDecrypt              = errors.New("decrypt secret value failed")
	ErrSecretNotUnderGroup  = errors.New("secret not under group")
	ErrSecretNotUnderFolder = ErrSecretNotUnderGroup
	ErrVersionConflict      = secretdomain.ErrVersionConflict
)

const initialCommitMsg = "initial version"

// ValueItemInput 创建时单个环境下的值入参
type ValueItemInput struct {
	EnvID uuid.UUID
	Value string // 明文，入库前加密
}

// CreateItemInput 创建时单个 secret 的入参
type CreateItemInput struct {
	FolderGroupID uuid.UUID
	Key           string
	Remark        string
	Values        []ValueItemInput
}

// CreateInput 批量创建 secrets 入参
type CreateInput struct {
	SecretList []CreateItemInput
	BatchID    uuid.UUID
}

// UpdateValueInput 更新时单个环境下的值入参
type UpdateValueInput struct {
	SecretID uuid.UUID // secret_info.id，唯一定位具体环境实例
	EnvCode  string    // 透传字段，不参与业务逻辑
	FolderID uuid.UUID // 透传字段，不参与业务逻辑
	Value    string    // 明文，入库前加密
}

// UpdateItemInput 更新时单个 secret 的入参
type UpdateItemInput struct {
	GroupID   uuid.UUID // secret 业务组 ID（必填）
	Key       string    // 透传展示字段，不参与业务校验
	Remark    string    // 非空时整组 secret 同步更新 remark
	CommitMsg string
	Values    []UpdateValueInput // 非空时按 secretId 逐条更新实际变化的 value
}

// UpdateInput 批量更新 secrets 入参（整请求单事务）
type UpdateInput struct {
	BatchID   uuid.UUID
	CommitMsg string
	Secrets   []UpdateItemInput
}

// SecretValueView 单个环境下的值视图（解密后，key 为 env code，无需 envId）
type SecretValueView struct {
	SecretID  uuid.UUID
	FolderID  uuid.UUID
	Value     string
	Version   int
	ValueType string
	UpdateAt  time.Time
}

// HistoryInput 历史查询入参，查询优先级 groupId > secretId > batchId。
// EnvList 为空查询全部环境；UserID 预留给后续环境权限过滤。
type HistoryInput struct {
	SecretID uuid.UUID
	BatchID  uuid.UUID
	GroupID  uuid.UUID
	EnvList  []string
	UserID   string
	PageNum  int
	PageSize int
}

// ListInput secrets 列表查询入参。
// FolderGroupID 非空时保留原有按 folder 业务组查询模式；否则使用项目 + folderCode + envList + keyList 模式。
type ListInput struct {
	FolderGroupID uuid.UUID
	ProjectID     uuid.UUID
	FolderCode    string
	EnvList       []string
	KeyList       []string
}

// HistoryView 解密后的 value 历史版本
type HistoryView struct {
	ID           uuid.UUID
	SecretID     uuid.UUID
	BatchID      uuid.UUID
	GroupID      uuid.UUID
	FolderID     uuid.UUID
	EnvCode      string
	Value        string
	ValueType    string
	Version      int
	CommitMsg    string
	CreateBy     string
	CreateByName string
	CreateAt     time.Time
}

// HistoryPage 单个查询维度下的历史分页。
type HistoryPage struct {
	Total       int64
	HistoryList []HistoryView
}

// BatchHistoryView 一次批次中一个逻辑 Secret 的修改结果。
type BatchHistoryView struct {
	GroupID  uuid.UUID
	Key      string
	Remark   string
	Versions map[uuid.UUID]HistoryView
}

// HistoryResult 历史查询结果。不同查询模式只填充自身对应的字段。
type HistoryResult struct {
	Total                int64
	HistoryList          []HistoryView
	EnvironmentHistories map[uuid.UUID]HistoryPage
	BatchHistories       []BatchHistoryView
}

// SecretView 一个 secret 的聚合视图（跨环境），查询接口统一返回结构
type SecretView struct {
	GroupID uuid.UUID
	Key     string
	Remark  string
	Values  map[string]SecretValueView // key: env code
}

// IService 密钥应用服务接口（handler 仅依赖接口，便于单测替换实现）
type IService interface {
	Create(ctx context.Context, in CreateInput, operator string) ([]*secretdomain.Secret, error)
	Update(ctx context.Context, in UpdateInput, operator string) error
	List(ctx context.Context, in ListInput) ([]SecretView, error)
	ListByFolder(ctx context.Context, folderGroupID uuid.UUID) ([]SecretView, error)
	GetByGroup(ctx context.Context, groupID uuid.UUID) (*SecretView, error)
	History(ctx context.Context, in HistoryInput) (*HistoryResult, error)
	Delete(ctx context.Context, groupID uuid.UUID, operator string) error
}

// valueCryptor 定义 Secret 应用服务所需的动态加解密能力
type valueCryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// Service 密钥应用服务实现（依赖密钥/文件夹/环境仓储与加解密器）
type Service struct {
	repo          secretdomain.Repository
	folderRepo    folderdomain.Repository
	envRepo       envdomain.Repository
	cipher        valueCryptor
	nameResolver  app.NicknameResolver
	auditRecorder auditdomain.Recorder
}

// NewService 创建密钥应用服务
func NewService(repo secretdomain.Repository, folderRepo folderdomain.Repository, envRepo envdomain.Repository, cipher valueCryptor, resolvers ...app.NicknameResolver) *Service {
	var resolver app.NicknameResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &Service{repo: repo, folderRepo: folderRepo, envRepo: envRepo, cipher: cipher, nameResolver: resolver}
}

// WithAuditRecorder enables mandatory audit recording for Secret use cases.
func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 批量创建 secrets：每个 secret 至少包含一个非空环境值，已提交的空环境仍创建实例以便后续更新。
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (created []*secretdomain.Secret, resultErr error) {
	batchID := in.BatchID
	if batchID == uuid.Nil {
		batchID = uuid.New()
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if auditErr := s.recordCreateFailure(ctx, in, batchID, operator, resultErr); auditErr != nil {
			resultErr = errors.Join(resultErr, auditErr)
		}
	}()
	if len(in.SecretList) == 0 {
		return nil, ErrInvalidParam
	}

	now := time.Now()
	created = make([]*secretdomain.Secret, 0)
	resultErr = s.repo.WithTx(ctx, func(txCtx context.Context) error {
		for _, item := range in.SecretList {
			batch, err := s.createOne(txCtx, item, operator, now)
			if err != nil {
				return err
			}
			created = append(created, batch...)
		}

		histories := make([]*secretdomain.History, 0, len(created))
		for _, sec := range created {
			histories = append(histories, newHistory(sec, batchID, initialCommitMsg, operator, now))
		}
		if err := s.repo.CreateHistoryBatch(txCtx, histories); err != nil {
			return err
		}
		return s.recordCreateSuccess(txCtx, created, in, batchID, operator)
	})
	if resultErr != nil {
		return nil, resultErr
	}
	sort.Slice(created, func(i, j int) bool {
		if created[i].Key != created[j].Key {
			return created[i].Key < created[j].Key
		}
		if created[i].GroupID != created[j].GroupID {
			return created[i].GroupID.String() < created[j].GroupID.String()
		}
		if created[i].EnvCode != created[j].EnvCode {
			return created[i].EnvCode < created[j].EnvCode
		}
		return created[i].ID.String() < created[j].ID.String()
	})
	return created, nil
}

// createOne 创建单个 secret。
func (s *Service) createOne(ctx context.Context, item CreateItemInput, operator string, now time.Time) ([]*secretdomain.Secret, error) {
	if item.FolderGroupID == uuid.Nil || item.Key == "" || len(item.Values) == 0 {
		return nil, ErrInvalidParam
	}

	hasValue := false
	for _, value := range item.Values {
		if strings.TrimSpace(value.Value) != "" {
			hasValue = true
			break
		}
	}
	if !hasValue {
		return nil, ErrInvalidParam
	}

	// folder 业务组展开：全部环境实例
	folders, err := s.folderRepo.ListByGroupID(ctx, item.FolderGroupID)
	if err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return nil, ErrFolderNotFound
	}
	keyPattern := folders[0].KeyPattern
	for _, folder := range folders[1:] {
		if folder.KeyPattern != keyPattern {
			return nil, ErrFolderPatternInvalid
		}
	}
	matched, err := folderdomain.MatchKeyPattern(keyPattern, item.Key)
	if err != nil {
		return nil, ErrFolderPatternInvalid
	}
	if !matched {
		return nil, ErrKeyPatternMismatch
	}

	// envID -> folderID / envCode 映射（该环境下 folder 的具体 id 与 code，env.code 不可更新故可直接冗余落库）
	folderByEnv := make(map[uuid.UUID]uuid.UUID, len(folders))
	codeByEnv := make(map[uuid.UUID]string, len(folders))
	for _, f := range folders {
		folderByEnv[f.EnvID] = f.ID
		e, err := s.envRepo.GetByID(ctx, f.EnvID)
		if err != nil {
			return nil, err
		}
		if e == nil {
			return nil, ErrEnvNotFound
		}
		codeByEnv[f.EnvID] = e.Code
	}

	// 每个 value 的 envId 必须属于该 folder 组
	for _, v := range item.Values {
		if _, ok := folderByEnv[v.EnvID]; !ok {
			return nil, ErrEnvNotFound
		}
	}

	// 业务 folder 内 key 唯一校验
	folderIDs := make([]uuid.UUID, 0, len(folders))
	for _, f := range folders {
		folderIDs = append(folderIDs, f.ID)
	}
	existing, err := s.repo.GetByFolderIDsKey(ctx, folderIDs, item.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrKeyExists
	}

	// 同一 secret 的所有环境实例共享一个 group_id
	groupID := uuid.New()
	secrets := make([]*secretdomain.Secret, 0, len(item.Values))
	for _, v := range item.Values {
		ciphertext, err := s.cipher.Encrypt(v.Value)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, &secretdomain.Secret{
			ID:              uuid.New(),
			GroupID:         groupID,
			FolderID:        folderByEnv[v.EnvID],
			EnvCode:         codeByEnv[v.EnvID],
			Key:             item.Key,
			ValueCiphertext: ciphertext,
			Remark:          item.Remark,
			Version:         1,
			CreateBy:        operator,
			UpdateBy:        operator,
			CreateAt:        now,
			UpdateAt:        now,
		})
	}

	if err := s.repo.CreateBatch(ctx, secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}

// Update 批量更新 secrets（整请求单事务）：
//   - 按 groupId 定位 secret 业务组
//   - remark 非空：整组同步更新 remark（不影响 version）
//   - values 非空：按 secretId 精确定位记录，仅明文实际变化时更新 value/version+1 并写历史
//   - key 字段透传，不参与业务校验
//
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (resultErr error) {
	batchID := in.BatchID
	if batchID == uuid.Nil {
		batchID = uuid.New()
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if auditErr := s.recordUpdateFailure(ctx, in, batchID, operator, resultErr); auditErr != nil {
			resultErr = errors.Join(resultErr, auditErr)
		}
	}()
	if len(in.Secrets) == 0 {
		return ErrInvalidParam
	}

	seenSecretIDs := make(map[uuid.UUID]struct{})
	for _, item := range in.Secrets {
		if item.GroupID == uuid.Nil || effectiveCommitMsg(item.CommitMsg, in.CommitMsg) == "" {
			return ErrInvalidParam
		}
		for _, v := range item.Values {
			if v.SecretID == uuid.Nil {
				return ErrInvalidParam
			}
			if _, exists := seenSecretIDs[v.SecretID]; exists {
				return ErrInvalidParam
			}
			seenSecretIDs[v.SecretID] = struct{}{}
		}
	}

	resultErr = s.repo.WithTx(ctx, func(txCtx context.Context) error {
		now := time.Now()
		histories := make([]*secretdomain.History, 0)
		auditEvents := make([]*auditdomain.Event, 0, len(in.Secrets))
		for _, item := range in.Secrets {
			secrets, err := s.repo.ListByGroupID(txCtx, item.GroupID)
			if err != nil {
				return err
			}
			if len(secrets) == 0 {
				return ErrNotFound
			}

			secretByID := make(map[uuid.UUID]*secretdomain.Secret, len(secrets))
			for _, sec := range secrets {
				secretByID[sec.ID] = sec
			}

			changes := make([]auditdomain.Change, 0, len(item.Values)+1)
			versions := make(map[string]any)
			if item.Remark != "" && item.Remark != secrets[0].Remark {
				if _, err := s.repo.UpdateRemarkByGroupID(txCtx, item.GroupID, item.Remark, operator, now); err != nil {
					return err
				}
				changes = append(changes, auditdomain.Change{
					Field: "remark", Before: secrets[0].Remark, After: item.Remark, Redacted: false,
				})
			}

			if len(item.Values) > 0 {
				updates := make([]secretdomain.ValueUpdateItem, 0, len(item.Values))
				for _, v := range item.Values {
					sec, ok := secretByID[v.SecretID]
					if !ok {
						return ErrSecretNotUnderGroup
					}
					currentValue, err := s.cipher.Decrypt(sec.ValueCiphertext)
					if err != nil {
						return ErrDecrypt
					}
					if currentValue == v.Value {
						continue
					}
					ciphertext, err := s.cipher.Encrypt(v.Value)
					if err != nil {
						return err
					}
					updates = append(updates, secretdomain.ValueUpdateItem{
						ID:              sec.ID,
						ValueCiphertext: ciphertext,
						ExpectedVersion: sec.Version,
					})
					updated := *sec
					updated.ValueCiphertext = ciphertext
					updated.Version++
					histories = append(histories, newHistory(&updated, batchID, effectiveCommitMsg(item.CommitMsg, in.CommitMsg), operator, now))
					changes = append(changes, auditdomain.Change{
						Field: "values." + sec.EnvCode, Changed: true, Redacted: true,
					})
					versions[sec.EnvCode] = map[string]any{
						"before": sec.Version,
						"after":  updated.Version,
					}
				}
				if len(updates) > 0 {
					if err := s.repo.UpdateValueByIDs(txCtx, updates, operator, now); err != nil {
						return err
					}
				}
			}

			scopeID := secrets[0].FolderID.String()
			if s.auditRecorder != nil {
				folder, err := s.folderRepo.GetByID(txCtx, secrets[0].FolderID)
				if err != nil {
					return err
				}
				if folder != nil && folder.GroupID != uuid.Nil {
					scopeID = folder.GroupID.String()
				}
			}
			auditEvents = append(auditEvents, newSecretAuditEvent(
				auditActionUpdate, auditdomain.ResultSuccess, item.GroupID.String(), secrets[0].Key,
				"folder", scopeID, &batchID, operator, changes,
				map[string]any{
					"changed":          len(changes) > 0,
					"hasCommitMessage": true,
					"versions":         versions,
				},
			))
		}
		if len(histories) > 0 {
			if err := s.repo.CreateHistoryBatch(txCtx, histories); err != nil {
				return err
			}
		}
		return s.recordAudit(txCtx, auditEvents)
	})
	return resultErr
}

// History 按 secretId 分页、按 batchId 不分页，或按 groupId 对各环境分别分页查询历史。
// EnvList 为空时查询全部环境；非空时只查询指定环境。
func (s *Service) History(ctx context.Context, in HistoryInput) (result *HistoryResult, resultErr error) {
	var histories []*secretdomain.History
	var total int64
	in.EnvList = normalizeList(in.EnvList)
	in.UserID = strings.TrimSpace(in.UserID)

	switch {
	case in.GroupID != uuid.Nil:
		result, resultErr = s.historyByGroup(ctx, in)
	case in.SecretID != uuid.Nil:
		histories, total, resultErr = s.repo.ListHistoryBySecretID(ctx, secretdomain.HistoryPageFilter{
			SecretID: in.SecretID,
			EnvCodes: in.EnvList,
			UserID:   in.UserID,
			Offset:   (in.PageNum - 1) * in.PageSize,
			Limit:    in.PageSize,
		})
	case in.BatchID != uuid.Nil:
		// 特殊情况：batchId 表示一次提交批次，需要完整返回该批次记录，不使用分页参数。
		result, resultErr = s.historyByBatch(ctx, in)
	default:
		resultErr = ErrInvalidParam
	}
	if resultErr != nil {
		if auditErr := s.recordHistoryAudit(ctx, in, nil, resultErr); auditErr != nil {
			resultErr = errors.Join(resultErr, auditErr)
		}
		return nil, resultErr
	}

	if result == nil {
		var views []HistoryView
		views, resultErr = s.toHistoryViews(ctx, histories, &sync.Map{})
		if resultErr == nil {
			result = &HistoryResult{Total: total, HistoryList: views}
		}
	}
	if resultErr != nil {
		if auditErr := s.recordHistoryAudit(ctx, in, nil, resultErr); auditErr != nil {
			resultErr = errors.Join(resultErr, auditErr)
		}
		return nil, resultErr
	}
	if auditErr := s.recordHistoryAudit(ctx, in, result, nil); auditErr != nil {
		return nil, auditErr
	}
	return result, nil
}

func (s *Service) historyByBatch(ctx context.Context, in HistoryInput) (*HistoryResult, error) {
	histories, err := s.repo.ListHistoryByBatchID(ctx, secretdomain.HistoryBatchFilter{
		BatchID:  in.BatchID,
		EnvCodes: in.EnvList,
		UserID:   in.UserID,
	})
	if err != nil {
		return nil, err
	}
	views, err := s.toHistoryViews(ctx, histories, &sync.Map{})
	if err != nil {
		return nil, err
	}

	byGroup := make(map[uuid.UUID]*BatchHistoryView)
	order := make([]uuid.UUID, 0)
	for i, history := range histories {
		item, exists := byGroup[history.GroupID]
		if !exists {
			item = &BatchHistoryView{
				GroupID:  history.GroupID,
				Key:      history.Key,
				Remark:   history.Remark,
				Versions: make(map[uuid.UUID]HistoryView),
			}
			byGroup[history.GroupID] = item
			order = append(order, history.GroupID)
		}
		item.Versions[history.EnvID] = views[i]
	}

	items := make([]BatchHistoryView, 0, len(order))
	for _, groupID := range order {
		items = append(items, *byGroup[groupID])
	}
	sort.Slice(items, func(i, j int) bool {
		return secretKeyLess(items[i].Key, items[i].GroupID, items[j].Key, items[j].GroupID)
	})
	return &HistoryResult{BatchHistories: items}, nil
}

func (s *Service) historyByGroup(ctx context.Context, in HistoryInput) (*HistoryResult, error) {
	targets, err := s.repo.ListHistoryTargetsByGroupID(ctx, secretdomain.HistoryTargetFilter{
		GroupID:  in.GroupID,
		EnvCodes: in.EnvList,
		UserID:   in.UserID,
	})
	if err != nil {
		return nil, err
	}

	type environmentHistory struct {
		envID uuid.UUID
		page  HistoryPage
	}

	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]environmentHistory, len(targets))
	var names sync.Map
	var wg sync.WaitGroup
	var firstErr error
	var errorOnce sync.Once

	for i, target := range targets {
		i, target := i, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			histories, total, err := s.repo.ListHistoryBySecretID(queryCtx, secretdomain.HistoryPageFilter{
				SecretID: target.SecretID,
				EnvCodes: in.EnvList,
				UserID:   in.UserID,
				Offset:   (in.PageNum - 1) * in.PageSize,
				Limit:    in.PageSize,
			})
			if err == nil {
				var views []HistoryView
				views, err = s.toHistoryViews(queryCtx, histories, &names)
				if err == nil {
					results[i] = environmentHistory{
						envID: target.EnvID,
						page:  HistoryPage{Total: total, HistoryList: views},
					}
					return
				}
			}
			errorOnce.Do(func() {
				firstErr = err
				cancel()
			})
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	pages := make(map[uuid.UUID]HistoryPage, len(results))
	for _, result := range results {
		pages[result.envID] = result.page
	}
	return &HistoryResult{EnvironmentHistories: pages}, nil
}

func (s *Service) toHistoryViews(ctx context.Context, histories []*secretdomain.History, names *sync.Map) ([]HistoryView, error) {
	views := make([]HistoryView, 0, len(histories))
	for _, history := range histories {
		value, err := s.cipher.Decrypt(history.ValueCiphertext)
		if err != nil {
			return nil, ErrDecrypt
		}
		views = append(views, HistoryView{
			ID:           history.ID,
			SecretID:     history.SecretID,
			BatchID:      history.BatchID,
			GroupID:      history.GroupID,
			FolderID:     history.FolderID,
			EnvCode:      history.EnvCode,
			Value:        value,
			ValueType:    history.ValueType,
			Version:      history.Version,
			CommitMsg:    history.CommitMsg,
			CreateBy:     history.CreateBy,
			CreateByName: s.resolveNickname(ctx, names, history.CreateBy),
			CreateAt:     history.CreateAt,
		})
	}
	return views, nil
}

func (s *Service) resolveNickname(ctx context.Context, names *sync.Map, userID string) string {
	userID = strings.TrimSpace(userID)
	if name, ok := names.Load(userID); ok {
		return name.(string)
	}
	name := app.ResolveNickname(ctx, s.nameResolver, userID)
	actual, _ := names.LoadOrStore(userID, name)
	return actual.(string)
}

// List 查询 secrets 列表，支持两种查询模式：
//  1. 旧模式：FolderGroupID 非空，按 folder 业务组查询其下全部 secrets。
//  2. 新模式：按 ProjectID + FolderCode + EnvList 查询，KeyList 为空返回全部 key，非空按 key 精确过滤。
func (s *Service) List(ctx context.Context, in ListInput) (views []SecretView, resultErr error) {
	resourceID := in.FolderGroupID.String()
	resourceName := ""
	scopeType := "folder"
	scopeID := resourceID
	if in.FolderGroupID == uuid.Nil {
		resourceID = in.ProjectID.String()
		resourceName = strings.TrimSpace(in.FolderCode)
		scopeType = "project"
		scopeID = resourceID
	}
	defer func() {
		detail := map[string]any{
			"environmentFilterCount": len(in.EnvList),
			"keyFilterCount":         len(in.KeyList),
			"resultCount":            len(views),
		}
		auditErr := s.recordReadResult(
			ctx, auditActionList, "secretCollection", resourceID, resourceName,
			scopeType, scopeID, "", detail, resultErr,
		)
		if auditErr == nil {
			return
		}
		if resultErr != nil {
			resultErr = errors.Join(resultErr, auditErr)
		} else {
			resultErr = auditErr
		}
	}()

	if in.FolderGroupID != uuid.Nil {
		return s.ListByFolder(ctx, in.FolderGroupID)
	}

	in.FolderCode = strings.TrimSpace(in.FolderCode)
	in.EnvList = normalizeList(in.EnvList)
	in.KeyList = normalizeList(in.KeyList)
	if in.ProjectID == uuid.Nil || in.FolderCode == "" || len(in.EnvList) == 0 {
		return nil, ErrInvalidParam
	}

	secrets, err := s.repo.ListByProjectFolder(ctx, secretdomain.ProjectFolderListFilter{
		ProjectID:  in.ProjectID,
		FolderCode: in.FolderCode,
		EnvCodes:   in.EnvList,
		Keys:       in.KeyList,
	})
	if err != nil {
		return nil, err
	}
	return s.buildViews(ctx, secrets)
}

// ListByFolder 查询1：按 folder 业务组查询其下全部 secrets（返回每个 secret 的聚合视图列表）
func (s *Service) ListByFolder(ctx context.Context, folderGroupID uuid.UUID) ([]SecretView, error) {
	if folderGroupID == uuid.Nil {
		return nil, ErrInvalidParam
	}

	folders, err := s.folderRepo.ListByGroupID(ctx, folderGroupID)
	if err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return nil, ErrFolderNotFound
	}

	folderIDs := make([]uuid.UUID, 0, len(folders))
	for _, f := range folders {
		folderIDs = append(folderIDs, f.ID)
	}

	secrets, err := s.repo.ListByFolderIDs(ctx, folderIDs)
	if err != nil {
		return nil, err
	}
	return s.buildViews(ctx, secrets)
}

func normalizeList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

// GetByGroup 查询2：按 secret 业务组查询所有环境下的值信息（聚合视图）
func (s *Service) GetByGroup(ctx context.Context, groupID uuid.UUID) (view *SecretView, resultErr error) {
	resourceID := ""
	if groupID != uuid.Nil {
		resourceID = groupID.String()
	}
	resourceName := ""
	scopeID := ""
	defer func() {
		auditErr := s.recordReadResult(
			ctx, auditActionRead, "secret", resourceID, resourceName,
			"folder", scopeID, "", nil, resultErr,
		)
		if auditErr == nil {
			return
		}
		if resultErr != nil {
			resultErr = errors.Join(resultErr, auditErr)
		} else {
			resultErr = auditErr
		}
	}()

	if groupID == uuid.Nil {
		return nil, ErrInvalidParam
	}

	secrets, err := s.repo.ListByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if len(secrets) == 0 {
		return nil, ErrNotFound
	}

	views, err := s.buildViews(ctx, secrets)
	if err != nil {
		return nil, err
	}
	resourceName = views[0].Key
	scopeID = secrets[0].FolderID.String()
	if s.auditRecorder != nil {
		folder, err := s.folderRepo.GetByID(ctx, secrets[0].FolderID)
		if err != nil {
			return nil, err
		}
		if folder != nil && folder.GroupID != uuid.Nil {
			scopeID = folder.GroupID.String()
		}
	}
	return &views[0], nil
}

// Delete 删除密钥：按 group_id 逻辑删除全部环境实例
func (s *Service) Delete(ctx context.Context, groupID uuid.UUID, operator string) (resultErr error) {
	resourceName := ""
	defer func() {
		if resultErr == nil {
			return
		}
		if auditErr := s.recordDeleteFailure(ctx, groupID, resourceName, operator, resultErr); auditErr != nil {
			resultErr = errors.Join(resultErr, auditErr)
		}
	}()
	if groupID == uuid.Nil {
		return ErrInvalidParam
	}

	resultErr = s.repo.WithTx(ctx, func(txCtx context.Context) error {
		secrets, err := s.repo.ListByGroupID(txCtx, groupID)
		if err != nil {
			return err
		}
		if len(secrets) == 0 {
			return ErrNotFound
		}
		resourceName = secrets[0].Key

		scopeID := secrets[0].FolderID.String()
		if s.auditRecorder != nil {
			folder, err := s.folderRepo.GetByID(txCtx, secrets[0].FolderID)
			if err != nil {
				return err
			}
			if folder != nil && folder.GroupID != uuid.Nil {
				scopeID = folder.GroupID.String()
			}
		}
		if _, err := s.repo.DeleteByGroupID(txCtx, groupID, operator); err != nil {
			return err
		}
		event := newSecretAuditEvent(
			auditActionDelete, auditdomain.ResultSuccess, groupID.String(), resourceName,
			"folder", scopeID, nil, operator,
			[]auditdomain.Change{{Field: "isDeleted", Before: false, After: true, Redacted: false}},
			map[string]any{"environmentCount": len(secrets)},
		)
		return s.recordAudit(txCtx, []*auditdomain.Event{event})
	})
	return resultErr
}

// buildViews 将若干环境实例按 group_id 聚合为 SecretView 列表（解密 value，env code 作为 values 的 key）
// 聚合直接使用 secret 记录上的冗余列（folder_id / env_code），不再跳查 folder/env 表
func (s *Service) buildViews(ctx context.Context, secrets []*secretdomain.Secret) ([]SecretView, error) {
	byGroup := make(map[uuid.UUID]*SecretView)
	order := make([]uuid.UUID, 0) // 保持分组出现顺序

	for _, sec := range secrets {
		view, ok := byGroup[sec.GroupID]
		if !ok {
			view = &SecretView{
				GroupID: sec.GroupID,
				Key:     sec.Key,
				Remark:  sec.Remark,
				Values:  make(map[string]SecretValueView),
			}
			byGroup[sec.GroupID] = view
			order = append(order, sec.GroupID)
		}

		value, err := s.cipher.Decrypt(sec.ValueCiphertext)
		if err != nil {
			return nil, ErrDecrypt
		}

		view.Values[sec.EnvCode] = SecretValueView{
			SecretID:  sec.ID,
			FolderID:  sec.FolderID,
			Value:     value,
			Version:   sec.Version,
			ValueType: sec.ValueType,
			UpdateAt:  sec.UpdateAt,
		}
	}

	views := make([]SecretView, 0, len(order))
	for _, gid := range order {
		views = append(views, *byGroup[gid])
	}
	sort.Slice(views, func(i, j int) bool {
		return secretKeyLess(views[i].Key, views[i].GroupID, views[j].Key, views[j].GroupID)
	})
	return views, nil
}

func secretKeyLess(leftKey string, leftGroupID uuid.UUID, rightKey string, rightGroupID uuid.UUID) bool {
	if leftKey != rightKey {
		return leftKey < rightKey
	}
	return leftGroupID.String() < rightGroupID.String()
}

func effectiveCommitMsg(itemMsg, requestMsg string) string {
	if msg := strings.TrimSpace(itemMsg); msg != "" {
		return msg
	}
	return strings.TrimSpace(requestMsg)
}

func newHistory(sec *secretdomain.Secret, batchID uuid.UUID, commitMsg, operator string, now time.Time) *secretdomain.History {
	return &secretdomain.History{
		ID:              uuid.New(),
		SecretID:        sec.ID,
		BatchID:         batchID,
		GroupID:         sec.GroupID,
		FolderID:        sec.FolderID,
		EnvCode:         sec.EnvCode,
		ValueCiphertext: sec.ValueCiphertext,
		ValueType:       sec.ValueType,
		Version:         sec.Version,
		CommitMsg:       commitMsg,
		CreateBy:        operator,
		CreateAt:        now,
	}
}
