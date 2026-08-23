package secret

import (
	"context"
	"time"

	"github.com/google/uuid"

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

// ListHistoryBySecretID 按具体环境实例分页查询历史，版本号倒序
func (r *Repository) ListHistoryBySecretID(ctx context.Context, secretID uuid.UUID, offset, limit int) ([]*secretdomain.History, int64, error) {
	db := r.withTxDB(ctx).WithContext(ctx).Model(&secretHistoryPO{}).Where("secret_id = ?", secretID)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var pos []secretHistoryPO
	if err := db.Order("version DESC, create_at DESC").Offset(offset).Limit(limit).Find(&pos).Error; err != nil {
		return nil, 0, err
	}
	return historiesToDomain(pos), total, nil
}

// ListHistoryTargetsByGroupID 查询逻辑 Secret 下各环境对应的物理 Secret。
func (r *Repository) ListHistoryTargetsByGroupID(ctx context.Context, groupID uuid.UUID) ([]secretdomain.HistoryTarget, error) {
	var rows []historyTargetRow
	err := r.withTxDB(ctx).WithContext(ctx).
		Table("secret_info AS s").
		Select("f.env_id AS env_id, s.id AS secret_id").
		Joins("JOIN folder_info AS f ON f.id = s.folder_id AND f.is_deleted = false").
		Where("s.group_id = ? AND s.is_deleted = false", groupID).
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
func (r *Repository) ListHistoryByBatchID(ctx context.Context, batchID uuid.UUID) ([]*secretdomain.History, error) {
	var pos []secretHistoryPO
	err := r.withTxDB(ctx).WithContext(ctx).
		Table("secret_info_history AS h").
		Select("h.*, s.key AS secret_key, s.remark AS secret_remark, f.env_id AS env_id").
		Joins("JOIN secret_info AS s ON s.id = h.secret_id").
		Joins("JOIN folder_info AS f ON f.id = h.folder_id").
		Where("h.batch_id = ?", batchID).
		Order("s.key ASC, h.group_id ASC, h.env_code ASC, h.id ASC").
		Scan(&pos).Error
	if err != nil {
		return nil, err
	}
	return historiesToDomain(pos), nil
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
