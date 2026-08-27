// Package user 用户仓储 PostgreSQL 实现（GORM）。
package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	userdomain "env-vault/internal/domain/user"
)

type userPO struct {
	ID           uuid.UUID  `gorm:"column:id;primaryKey"`
	UserID       string     `gorm:"column:user_id"`
	Nickname     string     `gorm:"column:nickname"`
	Username     string     `gorm:"column:username"`
	PasswordHash string     `gorm:"column:password_hash"`
	Email        string     `gorm:"column:email"`
	Phone        string     `gorm:"column:phone"`
	TenantID     uuid.UUID  `gorm:"column:tenant_id"`
	OrgID        uuid.UUID  `gorm:"column:org_id"`
	IsBlocked    bool       `gorm:"column:is_blocked"`
	IsDeleted    bool       `gorm:"column:is_deleted"`
	DeleteAt     *time.Time `gorm:"column:delete_at"`
	DeleteBy     string     `gorm:"column:delete_by"`
	CreateBy     string     `gorm:"column:create_by"`
	UpdateBy     string     `gorm:"column:update_by"`
	CreateAt     time.Time  `gorm:"column:create_at"`
	UpdateAt     time.Time  `gorm:"column:update_at"`
}

type projectUserRelationPO struct {
	ID        uuid.UUID `gorm:"column:id;primaryKey"`
	ProjectID uuid.UUID `gorm:"column:project_id"`
	UserID    uuid.UUID `gorm:"column:user_id"`
}

func (projectUserRelationPO) TableName() string {
	return "project_user_relation"
}

func (userPO) TableName() string {
	return "user_info"
}

// Repository 用户仓储 GORM 实现。
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建用户仓储。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// UpdateByUserID 按外部用户 ID 更新用户资料。
func (r *Repository) UpdateByUserID(ctx context.Context, user *userdomain.User) error {
	return r.db.WithContext(ctx).
		Model(&userPO{}).
		Where("user_id = ? AND is_deleted = false", user.UserID).
		Updates(map[string]any{
			"nickname":  user.Nickname,
			"username":  user.Username,
			"email":     user.Email,
			"phone":     user.Phone,
			"tenant_id": user.TenantID,
			"org_id":    user.OrgID,
			"update_by": user.UpdateBy,
			"update_at": user.UpdateAt,
		}).Error
}

// GetByUserID 按外部用户 ID 查询用户。
func (r *Repository) GetByUserID(ctx context.Context, userID string) (*userdomain.User, error) {
	var po userPO
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND is_deleted = false", userID).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// GetByUsername 忽略大小写按全局登录名查询用户。
func (r *Repository) GetByUsername(ctx context.Context, username string) (*userdomain.User, error) {
	var po userPO
	err := r.db.WithContext(ctx).
		Where("lower(username) = lower(?) AND username <> '' AND is_deleted = false", username).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// UpdatePasswordHashByUsername 按全局登录名更新本地认证密码哈希。
func (r *Repository) UpdatePasswordHashByUsername(ctx context.Context, username, passwordHash, operator string) error {
	result := r.db.WithContext(ctx).
		Model(&userPO{}).
		Where("lower(username) = lower(?) AND username <> '' AND is_deleted = false", username).
		Updates(map[string]any{
			"password_hash": passwordHash,
			"update_by":     operator,
			"update_at":     time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// List 查询未删除用户，只投影接口需要的非敏感字段。
func (r *Repository) List(ctx context.Context, filter userdomain.ListFilter) ([]*userdomain.User, error) {
	var pos []userPO
	query := r.db.WithContext(ctx).
		Model(&userPO{}).
		Select("user_info.id, user_info.user_id, user_info.nickname, user_info.is_blocked").
		Where("user_info.is_deleted = false")

	switch {
	case filter.ProjectID != uuid.Nil:
		query = query.
			Joins("JOIN project_user_relation AS pur ON pur.user_id = user_info.id").
			Where("pur.project_id = ?", filter.ProjectID)
	case filter.OrgID != uuid.Nil:
		query = query.Where("user_info.org_id = ?", filter.OrgID)
	case filter.TenantID != uuid.Nil:
		query = query.Where("user_info.tenant_id = ?", filter.TenantID)
	case filter.Undistributed:
		query = query.Where(
			"(user_info.tenant_id IS NULL OR user_info.tenant_id = ?) AND (user_info.org_id IS NULL OR user_info.org_id = ?)",
			uuid.Nil,
			uuid.Nil,
		)
	}

	if err := query.
		Order("user_info.nickname ASC, user_info.user_id ASC, user_info.id ASC").
		Find(&pos).Error; err != nil {
		return nil, err
	}

	users := make([]*userdomain.User, 0, len(pos))
	for i := range pos {
		users = append(users, &userdomain.User{
			ID:       pos[i].ID,
			UserID:   pos[i].UserID,
			Nickname: pos[i].Nickname,
		})
	}
	return users, nil
}

// Allocate 在单个事务内批量修改用户的租户、组织和项目归属。
func (r *Repository) Allocate(ctx context.Context, change userdomain.AllocationChange) ([]*userdomain.User, []string, error) {
	var updated []*userdomain.User
	var missing []string

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pos []userPO
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id IN ? AND is_deleted = false", change.UserIDs).
			Find(&pos).Error; err != nil {
			return err
		}

		found := make(map[string]struct{}, len(pos))
		userInternalIDs := make([]uuid.UUID, 0, len(pos))
		for i := range pos {
			found[pos[i].UserID] = struct{}{}
			userInternalIDs = append(userInternalIDs, pos[i].ID)
		}
		for _, userID := range change.UserIDs {
			if _, ok := found[userID]; !ok {
				missing = append(missing, userID)
			}
		}
		if len(missing) > 0 {
			return nil
		}

		now := time.Now()
		updates := map[string]any{
			"update_by": change.Operator,
			"update_at": now,
		}

		switch change.Type {
		case userdomain.AllocationTypeTenant:
			if change.Operation == userdomain.AllocationOperationAdd {
				updates["tenant_id"] = change.TenantID
				if err := updateUsers(tx, change.UserIDs, nil, updates); err != nil {
					return err
				}
			} else {
				updates["tenant_id"] = nil
				updates["org_id"] = nil
				if err := updateUsers(tx, change.UserIDs, &change.ResourceID, updates); err != nil {
					return err
				}
				if err := tx.Where(`user_id IN ? AND project_id IN (
					SELECT p.id FROM project_info AS p
					JOIN organization_info AS o ON o.id = p.org_id
					WHERE o.tenant_id = ?
				)`, userInternalIDs, change.ResourceID).Delete(&projectUserRelationPO{}).Error; err != nil {
					return err
				}
			}

		case userdomain.AllocationTypeOrg:
			if change.Operation == userdomain.AllocationOperationAdd {
				updates["tenant_id"] = change.TenantID
				updates["org_id"] = change.OrgID
				if err := updateUsers(tx, change.UserIDs, nil, updates); err != nil {
					return err
				}
			} else {
				updates["org_id"] = nil
				if err := updateUsersByOrg(tx, change.UserIDs, change.ResourceID, updates); err != nil {
					return err
				}
				if err := tx.Where(`user_id IN ? AND project_id IN (
					SELECT id FROM project_info WHERE org_id = ?
				)`, userInternalIDs, change.ResourceID).Delete(&projectUserRelationPO{}).Error; err != nil {
					return err
				}
			}

		case userdomain.AllocationTypeProject:
			if change.Operation == userdomain.AllocationOperationAdd {
				updates["tenant_id"] = change.TenantID
				updates["org_id"] = change.OrgID
				if err := updateUsers(tx, change.UserIDs, nil, updates); err != nil {
					return err
				}
				relations := make([]projectUserRelationPO, 0, len(userInternalIDs))
				for _, userID := range userInternalIDs {
					relations = append(relations, projectUserRelationPO{
						ID: uuid.New(), ProjectID: change.ProjectID, UserID: userID,
					})
				}
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "project_id"}, {Name: "user_id"}},
					DoNothing: true,
				}).Create(&relations).Error; err != nil {
					return err
				}
			} else if err := tx.
				Where("project_id = ? AND user_id IN ?", change.ProjectID, userInternalIDs).
				Delete(&projectUserRelationPO{}).Error; err != nil {
				return err
			}
		}

		var refreshed []userPO
		if err := tx.Where("id IN ? AND is_deleted = false", userInternalIDs).Find(&refreshed).Error; err != nil {
			return err
		}
		updated = make([]*userdomain.User, 0, len(refreshed))
		for i := range refreshed {
			updated = append(updated, toDomain(&refreshed[i]))
		}
		return nil
	})
	return updated, missing, err
}

func updateUsers(tx *gorm.DB, userIDs []string, tenantID *uuid.UUID, updates map[string]any) error {
	query := tx.Model(&userPO{}).Where("user_id IN ? AND is_deleted = false", userIDs)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	return query.Updates(updates).Error
}

func updateUsersByOrg(tx *gorm.DB, userIDs []string, orgID uuid.UUID, updates map[string]any) error {
	return tx.Model(&userPO{}).
		Where("user_id IN ? AND org_id = ? AND is_deleted = false", userIDs, orgID).
		Updates(updates).Error
}

// ListAll 查询全部未删除用户。
func (r *Repository) ListAll(ctx context.Context) ([]*userdomain.User, error) {
	var pos []userPO
	if err := r.db.WithContext(ctx).
		Where("is_deleted = false").
		Order("create_at ASC").
		Find(&pos).Error; err != nil {
		return nil, err
	}

	users := make([]*userdomain.User, 0, len(pos))
	for i := range pos {
		users = append(users, toDomain(&pos[i]))
	}
	return users, nil
}

func toDomain(po *userPO) *userdomain.User {
	return &userdomain.User{
		ID:           po.ID,
		UserID:       po.UserID,
		Nickname:     po.Nickname,
		Username:     po.Username,
		PasswordHash: po.PasswordHash,
		Email:        po.Email,
		Phone:        po.Phone,
		TenantID:     po.TenantID,
		OrgID:        po.OrgID,
		IsBlocked:    po.IsBlocked,
		IsDeleted:    po.IsDeleted,
		DeleteAt:     po.DeleteAt,
		DeleteBy:     po.DeleteBy,
		CreateBy:     po.CreateBy,
		UpdateBy:     po.UpdateBy,
		CreateAt:     po.CreateAt,
		UpdateAt:     po.UpdateAt,
	}
}
