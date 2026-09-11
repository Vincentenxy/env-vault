package tag

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"time"
)

var ErrCodeExists = errors.New("tag code already exists")
var ErrNotFound = errors.New("tag not found")

type Tag struct {
	ID               uuid.UUID `json:"id"`
	TenantID         uuid.UUID `json:"tenantId"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	Remark           string    `json:"remark"`
	AllowValueSearch bool      `json:"allowValueSearch"`
	CreateBy         string    `json:"createBy"`
	UpdateBy         string    `json:"updateBy"`
	CreateAt         time.Time `json:"createAt"`
	UpdateAt         time.Time `json:"updateAt"`
}

type Filter struct {
	TenantID uuid.UUID
	Keyword  string
	PageNum  int
	PageSize int
}

type Repository interface {
	Create(context.Context, *Tag) error
	Get(context.Context, uuid.UUID, uuid.UUID) (*Tag, error)
	Update(context.Context, *Tag) error
	Delete(context.Context, uuid.UUID, uuid.UUID, string) error
	List(context.Context, Filter) ([]*Tag, int64, error)
}
