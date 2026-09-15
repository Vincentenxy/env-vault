package secret

import (
	"context"
	"sort"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	tagdomain "env-vault/internal/domain/tag"
	"github.com/google/uuid"
)

// GroupTags 是独立于密钥明文的标签编辑视图
type GroupTags struct {
	GroupID  uuid.UUID           `json:"groupId"`
	TenantID uuid.UUID           `json:"tenantId"`
	TagList  []tagdomain.Summary `json:"tagList"`
}

// TagService 管理整组标签，不修改密钥值、版本和历史
type TagService struct {
	repo     tagdomain.GroupRepository
	recorder auditdomain.Recorder
}

// NewTagService 创建密钥标签服务
func NewTagService(repo tagdomain.GroupRepository, recorder auditdomain.Recorder) *TagService {
	return &TagService{repo: repo, recorder: recorder}
}

// Info 查询真实租户和当前标签，用于前端加载该租户的可选标签
func (s *TagService) Info(ctx context.Context, groupID uuid.UUID) (*GroupTags, error) {
	if groupID == uuid.Nil {
		return nil, ErrInvalidParam
	}
	group, err := s.repo.GetSecretGroup(ctx, groupID, false)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListGroupTags(ctx, []uuid.UUID{groupID})
	if err != nil {
		return nil, err
	}
	list := items[groupID]
	if list == nil {
		list = []tagdomain.Summary{}
	}
	return &GroupTags{GroupID: groupID, TenantID: group.TenantID, TagList: list}, nil
}

// Update 完整替换标签集合，省略列表拒绝写入，显式空数组解绑全部
func (s *TagService) Update(ctx context.Context, groupID uuid.UUID, ids []uuid.UUID, operator string) (*GroupTags, error) {
	if groupID == uuid.Nil || ids == nil {
		return nil, ErrInvalidParam
	}
	ids, err := normalizeTagIDs(ids)
	if err != nil {
		return nil, err
	}
	return auditapp.RunWrite(ctx, s.recorder, s.repo, true, func(workCtx context.Context) (*GroupTags, *auditdomain.Event, error) {
		group, err := s.repo.GetSecretGroup(workCtx, groupID, true)
		if err != nil {
			return nil, nil, err
		}
		// TODO: 接入统一权限中心后，按当前用户和 group 校验标签维护权限
		before, after, err := s.repo.ReplaceGroupTags(workCtx, group, ids, operator)
		if err != nil {
			return nil, nil, err
		}
		sort.Slice(after, func(i, j int) bool { return after[i].Code < after[j].Code })
		event := auditapp.NewResourceEvent(auditapp.ResourceEventInput{
			ActionCode: "secret.tag.update", ResultCode: auditdomain.ResultSuccess,
			ResourceType: "secret", ResourceID: groupID.String(), ResourceName: group.Key,
			ScopeType: "folder", ScopeID: group.FolderGroupID.String(), Operator: operator,
			Changes: auditapp.ChangedFields(auditdomain.Change{Field: "tagIdList", Before: summaryIDs(before), After: summaryIDs(after)}),
		})
		return &GroupTags{GroupID: groupID, TenantID: group.TenantID, TagList: after}, event, nil
	}, func(err error) *auditdomain.Event {
		event := auditapp.NewResourceEvent(auditapp.ResourceEventInput{
			ActionCode: "secret.tag.update", ResultCode: auditdomain.ResultFailure,
			ResourceType: "secret", ResourceID: groupID.String(), Operator: operator,
		})
		return auditapp.MarkFailure(event, err, ErrInvalidParam, tagdomain.ErrGroupNotFound, tagdomain.ErrTagNotInTenant)
	})
}

// normalizeTagIDs 去重和排序，统一加锁顺序并拒绝空 UUID
func normalizeTagIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	seen := make(map[uuid.UUID]bool, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, ErrInvalidParam
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result, nil
}

func summaryIDs(tags []tagdomain.Summary) []string {
	ids := make([]string, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.ID.String())
	}
	sort.Strings(ids)
	return ids
}
