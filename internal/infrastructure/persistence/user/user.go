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
	"env-vault/internal/infrastructure/persistence"
)

type userPO struct {
	ID                  uuid.UUID  `gorm:"column:id;primaryKey"`
	UserID              string     `gorm:"column:user_id"`
	Nickname            string     `gorm:"column:nickname"`
	Username            string     `gorm:"column:username"`
	PasswordHash        string     `gorm:"column:password_hash"`
	Email               string     `gorm:"column:email"`
	Phone               string     `gorm:"column:phone"`
	TenantID            uuid.UUID  `gorm:"column:tenant_id"`
	OrgID               uuid.UUID  `gorm:"column:org_id"`
	IsBlocked           bool       `gorm:"column:is_blocked"`
	IsDeleted           bool       `gorm:"column:is_deleted"`
	DeleteAt            *time.Time `gorm:"column:delete_at"`
	DeleteBy            string     `gorm:"column:delete_by"`
	CreateBy            string     `gorm:"column:create_by"`
	UpdateBy            string     `gorm:"column:update_by"`
	CreateAt            time.Time  `gorm:"column:create_at"`
	UpdateAt            time.Time  `gorm:"column:update_at"`
	ProjectRelationType string     `gorm:"column:project_relation_type;->"`
	ProjectExpireAt     *time.Time `gorm:"column:project_expire_at;->"`
}

type projectUserRelationPO struct {
	ID        uuid.UUID  `gorm:"column:id;primaryKey"`
	ProjectID uuid.UUID  `gorm:"column:project_id"`
	UserID    uuid.UUID  `gorm:"column:user_id"`
	ExpireAt  *time.Time `gorm:"column:expire_at"`
	IsDeleted bool       `gorm:"column:is_deleted"`
	DeleteAt  *time.Time `gorm:"column:delete_at"`
	DeleteBy  string     `gorm:"column:delete_by"`
	CreateBy  string     `gorm:"column:create_by"`
	UpdateBy  string     `gorm:"column:update_by"`
	CreateAt  time.Time  `gorm:"column:create_at"`
	UpdateAt  time.Time  `gorm:"column:update_at"`
}

type managementUserPO struct {
	ID         uuid.UUID `gorm:"column:id"`
	UserID     string    `gorm:"column:user_id"`
	Nickname   string    `gorm:"column:nickname"`
	Username   string    `gorm:"column:username"`
	Email      string    `gorm:"column:email"`
	Phone      string    `gorm:"column:phone"`
	TenantID   uuid.UUID `gorm:"column:tenant_id"`
	TenantName string    `gorm:"column:tenant_name"`
	OrgID      uuid.UUID `gorm:"column:org_id"`
	OrgName    string    `gorm:"column:org_name"`
	IsBlocked  bool      `gorm:"column:is_blocked"`
	CreateAt   time.Time `gorm:"column:create_at"`
	UpdateAt   time.Time `gorm:"column:update_at"`
}

type profileRelationPO struct {
	TenantName string `gorm:"column:tenant_name"`
	OrgName    string `gorm:"column:org_name"`
}

type profileProjectPO struct {
	ID   uuid.UUID `gorm:"column:id"`
	Name string    `gorm:"column:name"`
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

func (r *Repository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(persistence.WithTx(ctx, tx))
	})
}

// UpdateByUserID 按外部用户 ID 更新用户资料。
func (r *Repository) UpdateByUserID(ctx context.Context, user *userdomain.User) error {
	return persistence.TxDB(ctx, r.db).WithContext(ctx).
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

// UpdateManagement 在当前事务中更新用户资料，并清理旧直属归属范围内的项目关系。
func (r *Repository) UpdateManagement(ctx context.Context, change userdomain.ManagementUpdate) error {
	user := change.User
	tx := persistence.TxDB(ctx, r.db).WithContext(ctx)
	result := tx.Model(&userPO{}).
		Where("id = ? AND user_id = ? AND is_deleted = false", user.ID, user.UserID).
		Updates(map[string]any{
			"nickname":  user.Nickname,
			"username":  user.Username,
			"email":     user.Email,
			"phone":     user.Phone,
			"tenant_id": nullableUUID(user.TenantID),
			"org_id":    nullableUUID(user.OrgID),
			"update_by": user.UpdateBy,
			"update_at": user.UpdateAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	switch {
	case change.PreviousTenantID != uuid.Nil && change.PreviousTenantID != user.TenantID:
		return softDeleteProjectRelations(tx.Where(`user_id = ? AND project_id IN (
			SELECT p.id FROM project_info AS p
			JOIN organization_info AS o ON o.id = p.org_id
			WHERE o.tenant_id = ?
		)`, user.ID, change.PreviousTenantID), user.UpdateBy, user.UpdateAt)
	case change.PreviousOrgID != uuid.Nil && change.PreviousOrgID != user.OrgID:
		return softDeleteProjectRelations(tx.Where(`user_id = ? AND project_id IN (
			SELECT id FROM project_info WHERE org_id = ?
		)`, user.ID, change.PreviousOrgID), user.UpdateBy, user.UpdateAt)
	default:
		return nil
	}
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// GetByUserID 按外部用户 ID 查询用户。
func (r *Repository) GetByUserID(ctx context.Context, userID string) (*userdomain.User, error) {
	var po userPO
	err := persistence.TxDB(ctx, r.db).WithContext(ctx).
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

// GetProfileRelations 实时查询用户的租户、组织名称及项目成员关系。
func (r *Repository) GetProfileRelations(ctx context.Context, userID uuid.UUID) (*userdomain.ProfileRelations, error) {
	db := persistence.TxDB(ctx, r.db).WithContext(ctx)

	var relation profileRelationPO
	if err := db.
		Table("user_info AS u").
		Select(`COALESCE(t.name, '') AS tenant_name, COALESCE(o.name, '') AS org_name`).
		Joins("LEFT JOIN tenant_info AS t ON t.id = u.tenant_id AND t.is_deleted = false").
		Joins("LEFT JOIN organization_info AS o ON o.id = u.org_id AND o.is_deleted = false").
		Where("u.id = ? AND u.is_deleted = false", userID).
		Scan(&relation).Error; err != nil {
		return nil, err
	}

	var projectPOs []profileProjectPO
	if err := db.
		Table("project_info AS p").
		Select("p.id, p.name").
		Joins("JOIN project_user_relation AS pur ON pur.project_id = p.id").
		Where("pur.user_id = ? AND pur.is_deleted = false AND (pur.expire_at IS NULL OR pur.expire_at > NOW()) AND p.is_deleted = false", userID).
		Order("p.name ASC, p.id ASC").
		Scan(&projectPOs).Error; err != nil {
		return nil, err
	}

	projects := make([]userdomain.ProfileProject, 0, len(projectPOs))
	for _, project := range projectPOs {
		projects = append(projects, userdomain.ProfileProject{ID: project.ID, Name: project.Name})
	}
	return &userdomain.ProfileRelations{
		TenantName: relation.TenantName,
		OrgName:    relation.OrgName,
		Projects:   projects,
	}, nil
}

// GetByUsername 忽略大小写按全局登录名查询用户。
func (r *Repository) GetByUsername(ctx context.Context, username string) (*userdomain.User, error) {
	var po userPO
	err := persistence.TxDB(ctx, r.db).WithContext(ctx).
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
	result := persistence.TxDB(ctx, r.db).WithContext(ctx).
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
	query := persistence.TxDB(ctx, r.db).WithContext(ctx).
		Model(&userPO{}).
		Select("user_info.id, user_info.user_id, user_info.nickname, user_info.is_blocked").
		Where("user_info.is_deleted = false")

	switch {
	case filter.ProjectID != uuid.Nil:
		query = query.
			Select(`user_info.id, user_info.user_id, user_info.nickname, user_info.is_blocked,
				CASE WHEN user_info.org_id IS DISTINCT FROM p.org_id THEN 'external' ELSE 'internal' END AS project_relation_type,
				pur.expire_at AS project_expire_at`).
			Joins("JOIN project_user_relation AS pur ON pur.user_id = user_info.id AND pur.is_deleted = false AND (pur.expire_at IS NULL OR pur.expire_at > NOW())").
			Joins("JOIN project_info AS p ON p.id = pur.project_id AND p.is_deleted = false").
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
		user := &userdomain.User{
			ID:        pos[i].ID,
			UserID:    pos[i].UserID,
			Nickname:  pos[i].Nickname,
			IsBlocked: pos[i].IsBlocked,
		}
		if filter.ProjectID != uuid.Nil {
			user.ProjectRelation = &userdomain.ProjectRelation{
				MemberType: userdomain.ProjectMemberType(pos[i].ProjectRelationType),
				ExpireAt:   pos[i].ProjectExpireAt,
			}
		}
		users = append(users, user)
	}
	return users, nil
}

// IsOrganizationMember 判断外部用户标识对应的有效用户是否属于指定组织。
func (r *Repository) IsOrganizationMember(ctx context.Context, userID string, orgID uuid.UUID) (bool, error) {
	var count int64
	err := persistence.TxDB(ctx, r.db).WithContext(ctx).
		Model(&userPO{}).
		Where("user_id = ? AND org_id = ? AND is_deleted = false AND is_blocked = false", userID, orgID).
		Count(&count).Error
	return count > 0, err
}

// ListManagement 分页查询用户管理列表，并在同一条查询中附加租户和组织名称。
func (r *Repository) ListManagement(ctx context.Context, filter userdomain.ManagementListFilter) ([]*userdomain.ManagementUser, int64, error) {
	query := persistence.TxDB(ctx, r.db).WithContext(ctx).
		Table("user_info AS u").
		Where("u.is_deleted = false")
	if filter.TenantID != uuid.Nil {
		query = query.Where("u.tenant_id = ?", filter.TenantID)
	}
	if filter.Keyword != "" {
		keyword := "%" + filter.Keyword + "%"
		query = query.Where(`(u.nickname ILIKE ? OR u.username ILIKE ? OR u.user_id ILIKE ? OR u.email ILIKE ?)`,
			keyword, keyword, keyword, keyword)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var pos []managementUserPO
	err := query.
		Select(`u.id, u.user_id, u.nickname, u.username, u.email, u.phone,
			u.tenant_id, COALESCE(t.name, '') AS tenant_name,
			u.org_id, COALESCE(o.name, '') AS org_name,
			u.is_blocked, u.create_at, u.update_at`).
		Joins("LEFT JOIN tenant_info AS t ON t.id = u.tenant_id AND t.is_deleted = false").
		Joins("LEFT JOIN organization_info AS o ON o.id = u.org_id AND o.is_deleted = false").
		Order("u.nickname ASC, u.user_id ASC, u.id ASC").
		Offset((filter.PageNum - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Scan(&pos).Error
	if err != nil {
		return nil, 0, err
	}

	users := make([]*userdomain.ManagementUser, 0, len(pos))
	for i := range pos {
		users = append(users, &userdomain.ManagementUser{
			User: userdomain.User{
				ID: pos[i].ID, UserID: pos[i].UserID, Nickname: pos[i].Nickname,
				Username: pos[i].Username, Email: pos[i].Email, Phone: pos[i].Phone,
				TenantID: pos[i].TenantID, OrgID: pos[i].OrgID, IsBlocked: pos[i].IsBlocked,
				CreateAt: pos[i].CreateAt, UpdateAt: pos[i].UpdateAt,
			},
			TenantName: pos[i].TenantName,
			OrgName:    pos[i].OrgName,
		})
	}
	return users, total, nil
}

// Allocate 在单个事务内批量修改用户的租户、组织和项目归属。
func (r *Repository) Allocate(ctx context.Context, change userdomain.AllocationChange) ([]*userdomain.User, []string, error) {
	var updated []*userdomain.User
	var missing []string

	tx := persistence.TxDB(ctx, r.db).WithContext(ctx)
	err := func() error {
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
				if err := softDeleteProjectRelations(tx.Where(`user_id IN ? AND project_id IN (
					SELECT p.id FROM project_info AS p
					JOIN organization_info AS o ON o.id = p.org_id
					WHERE o.tenant_id = ?
				)`, userInternalIDs, change.ResourceID), change.Operator, now); err != nil {
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
				if err := softDeleteProjectRelations(tx.Where(`user_id IN ? AND project_id IN (
					SELECT id FROM project_info WHERE org_id = ?
				)`, userInternalIDs, change.ResourceID), change.Operator, now); err != nil {
					return err
				}
			}

		case userdomain.AllocationTypeProject:
			if change.Operation == userdomain.AllocationOperationAdd {
				relations := make([]projectUserRelationPO, 0, len(userInternalIDs))
				for _, userID := range userInternalIDs {
					relations = append(relations, projectUserRelationPO{
						ID:        uuid.New(),
						ProjectID: change.ProjectID,
						UserID:    userID,
						CreateBy:  change.Operator,
						UpdateBy:  change.Operator,
						CreateAt:  now,
						UpdateAt:  now,
					})
				}
				if err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "project_id"}, {Name: "user_id"}},
					TargetWhere: clause.Where{Exprs: []clause.Expression{
						clause.Eq{Column: clause.Column{Name: "is_deleted"}, Value: false},
					}},
					DoUpdates: clause.Assignments(map[string]any{
						"expire_at": nil,
						"update_by": change.Operator,
						"update_at": now,
					}),
				}).Create(&relations).Error; err != nil {
					return err
				}
			} else if err := softDeleteProjectRelations(tx.
				Where("project_id = ? AND user_id IN ?", change.ProjectID, userInternalIDs), change.Operator, now); err != nil {
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
	}()
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

func softDeleteProjectRelations(query *gorm.DB, operator string, now time.Time) error {
	return query.Model(&projectUserRelationPO{}).
		Where("is_deleted = false").
		Updates(map[string]any{
			"is_deleted": true,
			"delete_at":  now,
			"delete_by":  operator,
			"update_at":  now,
			"update_by":  operator,
		}).Error
}

// ListAll 查询全部未删除用户。
func (r *Repository) ListAll(ctx context.Context) ([]*userdomain.User, error) {
	var pos []userPO
	if err := persistence.TxDB(ctx, r.db).WithContext(ctx).
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
