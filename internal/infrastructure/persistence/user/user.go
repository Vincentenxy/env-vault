// Package user 用户仓储 PostgreSQL 实现（GORM）。
package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

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
	IsDeleted    bool       `gorm:"column:is_deleted"`
	DeleteAt     *time.Time `gorm:"column:delete_at"`
	DeleteBy     string     `gorm:"column:delete_by"`
	CreateBy     string     `gorm:"column:create_by"`
	UpdateBy     string     `gorm:"column:update_by"`
	CreateAt     time.Time  `gorm:"column:create_at"`
	UpdateAt     time.Time  `gorm:"column:update_at"`
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

// GetByTenantUsername 按租户和登录名查询用户。
func (r *Repository) GetByTenantUsername(ctx context.Context, tenantID uuid.UUID, username string) (*userdomain.User, error) {
	var po userPO
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND username = ? AND is_deleted = false", tenantID, username).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
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
		IsDeleted:    po.IsDeleted,
		DeleteAt:     po.DeleteAt,
		DeleteBy:     po.DeleteBy,
		CreateBy:     po.CreateBy,
		UpdateBy:     po.UpdateBy,
		CreateAt:     po.CreateAt,
		UpdateAt:     po.UpdateAt,
	}
}
