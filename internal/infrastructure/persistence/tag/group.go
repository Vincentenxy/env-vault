package tag

import (
	"context"
	"time"

	domain "env-vault/internal/domain/tag"
	"env-vault/internal/infrastructure/persistence"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetSecretGroup 沿目录、环境、项目、组织解析真实租户，写入时先锁目录再锁密钥
func (r *Repository) GetSecretGroup(ctx context.Context, groupID uuid.UUID, lock bool) (*domain.SecretGroup, error) {
	db := persistence.TxDB(ctx, r.db).WithContext(ctx)
	if lock {
		// 锁住祖先目录，让目录删除与绑定串行，防止删除清理结束后又写入关联
		var folders []struct {
			ID        uuid.UUID
			IsDeleted bool
		}
		err := db.Raw(`WITH RECURSIVE ancestors AS (
			SELECT f.id, f.parent_folder_id FROM folder_info f
			JOIN secret_info s ON s.folder_id = f.id WHERE s.group_id = ? AND s.is_deleted = false
			UNION
			SELECT f.id, f.parent_folder_id FROM folder_info f JOIN ancestors a ON f.id = a.parent_folder_id
		) SELECT f.id, f.is_deleted FROM folder_info f
		WHERE f.id IN (SELECT id FROM ancestors) ORDER BY f.id FOR UPDATE`, groupID).Scan(&folders).Error
		if err != nil {
			return nil, err
		}
		if len(folders) == 0 {
			return nil, domain.ErrGroupNotFound
		}
		for _, folder := range folders {
			if folder.IsDeleted {
				return nil, domain.ErrGroupNotFound
			}
		}
	}
	query := db.Table("secret_info s").
		Select("s.group_id, s.key, f.group_id AS folder_group_id, o.tenant_id").
		Joins("JOIN folder_info f ON f.id = s.folder_id AND f.is_deleted = false").
		Joins("JOIN environment_info e ON e.id = f.env_id AND e.is_deleted = false").
		Joins("JOIN project_info p ON p.id = e.project_id AND p.is_deleted = false").
		Joins("JOIN organization_info o ON o.id = p.org_id AND o.is_deleted = false").
		Joins("JOIN tenant_info t ON t.id = o.tenant_id AND t.is_deleted = false").
		Where("s.group_id = ? AND s.is_deleted = false", groupID).
		Where("f.parent_folder_id IS NULL OR EXISTS (SELECT 1 FROM folder_info parent WHERE parent.id = f.parent_folder_id AND parent.is_deleted = false)").
		Order("s.id")
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "s"}})
	}
	var groups []domain.SecretGroup
	if err := query.Find(&groups).Error; err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, domain.ErrGroupNotFound
	}
	for _, group := range groups {
		if group.TenantID != groups[0].TenantID || group.FolderGroupID != groups[0].FolderGroupID {
			return nil, domain.ErrGroupNotFound
		}
	}
	return &groups[0], nil
}

// ListGroupTags 一次获取整批密钥的有效标签，软删除标签和关联均不可见
func (r *Repository) ListGroupTags(ctx context.Context, groupIDs []uuid.UUID) (map[uuid.UUID][]domain.Summary, error) {
	result := make(map[uuid.UUID][]domain.Summary, len(groupIDs))
	if len(groupIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		GroupID uuid.UUID
		domain.Summary
	}
	err := persistence.TxDB(ctx, r.db).WithContext(ctx).Table("secret_tag_relation r").
		Select("r.target_id AS group_id, t.id, t.code, t.name, t.allow_value_search").
		Joins("JOIN tag_info t ON t.id = r.tag_id AND t.is_deleted = false").
		Where("r.target_type = 'group' AND r.target_id IN ? AND r.is_deleted = false", groupIDs).
		Order("t.code, t.id").Scan(&rows).Error
	for _, row := range rows {
		result[row.GroupID] = append(result[row.GroupID], row.Summary)
	}
	return result, err
}

// ReplaceGroupTags 校验并锁定租户标签，只增删集合差异，重复提交不重建关联
func (r *Repository) ReplaceGroupTags(ctx context.Context, group *domain.SecretGroup, ids []uuid.UUID, operator string) ([]domain.Summary, []domain.Summary, error) {
	db := persistence.TxDB(ctx, r.db).WithContext(ctx)
	after := make([]domain.Summary, 0, len(ids))
	if len(ids) > 0 {
		// 与标签删除竞争同一行锁，标签删除后不再允许新绑定
		err := db.Table("tag_info").Select("id, code, name, allow_value_search").
			Where("tenant_id = ? AND id IN ? AND is_deleted = false", group.TenantID, ids).
			Order("id").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&after).Error
		if err != nil {
			return nil, nil, err
		}
		if len(after) != len(ids) {
			return nil, nil, domain.ErrTagNotInTenant
		}
	}
	existing, err := r.ListGroupTags(ctx, []uuid.UUID{group.GroupID})
	if err != nil {
		return nil, nil, err
	}
	before := existing[group.GroupID]
	if before == nil {
		before = []domain.Summary{}
	}
	keep := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
	}
	bound := make(map[uuid.UUID]bool, len(before))
	removed := make([]uuid.UUID, 0)
	for _, tag := range before {
		bound[tag.ID] = true
		if !keep[tag.ID] {
			removed = append(removed, tag.ID)
		}
	}
	if len(removed) > 0 {
		if err := SoftDeleteRelations(db.Where("target_id = ? AND tag_id IN ?", group.GroupID, removed), operator); err != nil {
			return nil, nil, err
		}
	}
	now := time.Now()
	for _, id := range ids {
		if bound[id] {
			continue
		}
		err := db.Table("secret_tag_relation").Create(map[string]any{
			"id": uuid.New(), "target_type": "group", "target_id": group.GroupID, "tag_id": id,
			"create_by": operator, "update_by": operator, "create_at": now, "update_at": now,
		}).Error
		if err != nil {
			return nil, nil, err
		}
	}
	return before, after, nil
}

// SoftDeleteRelations 复用软删除字段，调用方在业务事务中限定清理范围
func SoftDeleteRelations(query *gorm.DB, operator string) error {
	now := time.Now()
	return query.Table("secret_tag_relation").Where("target_type = 'group' AND is_deleted = false").Updates(map[string]any{
		"is_deleted": true, "delete_at": now, "delete_by": operator, "update_at": now, "update_by": operator,
	}).Error
}

var _ domain.GroupRepository = (*Repository)(nil)
