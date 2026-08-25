package secret

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	secretdomain "env-vault/internal/domain/secret"
)

// secretHistoryPO secret_info_history 表持久化对象
type secretHistoryPO struct {
	ID              uuid.UUID `gorm:"column:id;primaryKey"`
	SecretID        uuid.UUID `gorm:"column:secret_id"`
	BatchID         uuid.UUID `gorm:"column:batch_id"`
	GroupID         uuid.UUID `gorm:"column:group_id"`
	FolderID        uuid.UUID `gorm:"column:folder_id"`
	EnvID           uuid.UUID `gorm:"column:env_id;->"`
	EnvCode         string    `gorm:"column:env_code"`
	Key             string    `gorm:"column:secret_key;->"`
	Remark          string    `gorm:"column:secret_remark;->"`
	ValueCiphertext string    `gorm:"column:value_ciphertext"`
	ValueType       string    `gorm:"column:value_type"`
	Version         int       `gorm:"column:version"`
	CommitMsg       string    `gorm:"column:commit_msg"`
	CreateBy        string    `gorm:"column:create_by"`
	CreateAt        time.Time `gorm:"column:create_at"`
}

type historyTargetRow struct {
	EnvID    uuid.UUID `gorm:"column:env_id"`
	SecretID uuid.UUID `gorm:"column:secret_id"`
}

func (secretHistoryPO) TableName() string {
	return "secret_info_history"
}

// CreateHistoryBatch 批量写入 value 历史快照
func (r *Repository) CreateHistoryBatch(ctx context.Context, histories []*secretdomain.History) error {
	if len(histories) == 0 {
		return nil
	}
	pos := make([]*secretHistoryPO, 0, len(histories))
	for _, history := range histories {
		pos = append(pos, historyToPO(history))
	}
	return r.withTxDB(ctx).WithContext(ctx).Create(&pos).Error
}

// ListHistoryBySecretID 按具体环境实例分页查询历史，版本号倒序。
func (r *Repository) ListHistoryBySecretID(ctx context.Context, filter secretdomain.HistoryPageFilter) ([]*secretdomain.History, int64, error) {
	db := r.withTxDB(ctx).WithContext(ctx).Model(&secretHistoryPO{}).Where("secret_id = ?", filter.SecretID)
	if len(filter.EnvCodes) > 0 {
		db = db.Where("env_code IN ?", filter.EnvCodes)
	}
	db = applyHistoryPermissionFilter(db, filter.UserID, "secret_info_history")
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var pos []secretHistoryPO
	if err := db.Order("version DESC, create_at DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&pos).Error; err != nil {
		return nil, 0, err
	}
	return historiesToDomain(pos), total, nil
}

// ListHistoryTargetsByGroupID 查询逻辑 Secret 下各环境对应的物理 Secret。
func (r *Repository) ListHistoryTargetsByGroupID(ctx context.Context, filter secretdomain.HistoryTargetFilter) ([]secretdomain.HistoryTarget, error) {
	var rows []historyTargetRow
	query := r.withTxDB(ctx).WithContext(ctx).
		Table("secret_info AS s").
		Select("f.env_id AS env_id, s.id AS secret_id").
		Joins("JOIN folder_info AS f ON f.id = s.folder_id AND f.is_deleted = false").
		Where("s.group_id = ? AND s.is_deleted = false", filter.GroupID)
	if len(filter.EnvCodes) > 0 {
		query = query.Where("s.env_code IN ?", filter.EnvCodes)
	}
	query = applyHistoryTargetPermissionFilter(query, filter.UserID)
	err := query.
		Order("s.env_code ASC, s.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	targets := make([]secretdomain.HistoryTarget, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, secretdomain.HistoryTarget{
			EnvID:    row.EnvID,
			SecretID: row.SecretID,
		})
	}
	return targets, nil
}

// ListHistoryByBatchID 查询一次 create/update 批次产生的全部历史，并补齐聚合展示所需的环境和 Secret 信息。
// 历史查询不限制当前 Secret/Folder 的软删除状态，确保资源删除后仍能查看既有批次。
func (r *Repository) ListHistoryByBatchID(ctx context.Context, filter secretdomain.HistoryBatchFilter) ([]*secretdomain.History, error) {
	var pos []secretHistoryPO
	query := r.withTxDB(ctx).WithContext(ctx).
		Table("secret_info_history AS h").
		Select("h.*, s.key AS secret_key, s.remark AS secret_remark, f.env_id AS env_id").
		Joins("JOIN secret_info AS s ON s.id = h.secret_id").
		Joins("JOIN folder_info AS f ON f.id = h.folder_id").
		Where("h.batch_id = ?", filter.BatchID)
	if len(filter.EnvCodes) > 0 {
		query = query.Where("h.env_code IN ?", filter.EnvCodes)
	}
	query = applyHistoryPermissionFilter(query, filter.UserID, "h")
	err := query.
		Order("s.key ASC, h.group_id ASC, h.env_code ASC, h.id ASC").
		Scan(&pos).Error
	if err != nil {
		return nil, err
	}
	return historiesToDomain(pos), nil
}

func applyHistoryPermissionFilter(query *gorm.DB, userID, historyAlias string) *gorm.DB {
	// TODO(permission): 权限模型落地后，使用 historyAlias 的 folder_id/env_code
	// 关联环境权限表。该条件与请求 EnvCodes 条件叠加，未授权环境直接在 SQL 层过滤。
	_, _ = userID, historyAlias
	return query
}

func applyHistoryTargetPermissionFilter(query *gorm.DB, userID string) *gorm.DB {
	// TODO(permission): 权限模型落地后，按 userID 过滤 s/f 对应的环境访问权限。
	// EnvCodes 为空时仍必须保留此权限过滤，以实现“全部有权限环境”的查询语义。
	_ = userID
	return query
}

func historyToPO(history *secretdomain.History) *secretHistoryPO {
	return &secretHistoryPO{
		ID:              history.ID,
		SecretID:        history.SecretID,
		BatchID:         history.BatchID,
		GroupID:         history.GroupID,
		FolderID:        history.FolderID,
		EnvCode:         history.EnvCode,
		ValueCiphertext: history.ValueCiphertext,
		ValueType:       history.ValueType,
		Version:         history.Version,
		CommitMsg:       history.CommitMsg,
		CreateBy:        history.CreateBy,
		CreateAt:        history.CreateAt,
	}
}

func historiesToDomain(pos []secretHistoryPO) []*secretdomain.History {
	histories := make([]*secretdomain.History, 0, len(pos))
	for i := range pos {
		history := pos[i]
		histories = append(histories, &secretdomain.History{
			ID:              history.ID,
			SecretID:        history.SecretID,
			BatchID:         history.BatchID,
			GroupID:         history.GroupID,
			FolderID:        history.FolderID,
			EnvID:           history.EnvID,
			EnvCode:         history.EnvCode,
			Key:             history.Key,
			Remark:          history.Remark,
			ValueCiphertext: history.ValueCiphertext,
			ValueType:       history.ValueType,
			Version:         history.Version,
			CommitMsg:       history.CommitMsg,
			CreateBy:        history.CreateBy,
			CreateAt:        history.CreateAt,
		})
	}
	return histories
}
