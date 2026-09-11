package tag

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	domain "env-vault/internal/domain/tag"
	tenantdomain "env-vault/internal/domain/tenant"
	"github.com/google/uuid"
)

var ErrInvalidParam = errors.New("invalid tag params")
var ErrTenantNotFound = errors.New("tenant not found")
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type TenantReader interface {
	GetByID(context.Context, uuid.UUID) (*tenantdomain.Tenant, error)
}
type Input struct {
	TenantID         uuid.UUID `json:"tenantId"`
	ID               uuid.UUID `json:"id"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	Remark           string    `json:"remark"`
	AllowValueSearch *bool     `json:"allowValueSearch"`
}
type IService interface {
	Create(context.Context, Input, string) (*domain.Tag, error)
	Update(context.Context, Input, string) (*domain.Tag, error)
	Delete(context.Context, Input, string) error
	Info(context.Context, Input) (*domain.Tag, error)
	List(context.Context, domain.Filter) ([]*domain.Tag, int64, error)
}
type Service struct {
	repo     domain.Repository
	tenants  TenantReader
	recorder auditdomain.Recorder
}

func NewService(repo domain.Repository, tenants TenantReader, recorder auditdomain.Recorder) *Service {
	return &Service{repo: repo, tenants: tenants, recorder: recorder}
}
func (s *Service) tenant(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrInvalidParam
	}
	tenant, err := s.tenants.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if tenant == nil || tenant.IsDeleted {
		return ErrTenantNotFound
	}
	return nil
}
func valid(in Input, create bool) bool {
	return in.TenantID != uuid.Nil && (!create || codePattern.MatchString(in.Code)) &&
		(create || in.ID != uuid.Nil) && in.Name != "" && utf8.RuneCountInString(in.Name) <= 64 && utf8.RuneCountInString(in.Remark) <= 1024
}
func (s *Service) Create(ctx context.Context, in Input, operator string) (*domain.Tag, error) {
	in.Code, in.Name, in.Remark = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.Remark)
	if !valid(in, true) {
		return nil, ErrInvalidParam
	}
	return s.write(ctx, "create", in, operator)
}
func (s *Service) Update(ctx context.Context, in Input, operator string) (*domain.Tag, error) {
	in.Name, in.Remark = strings.TrimSpace(in.Name), strings.TrimSpace(in.Remark)
	if !valid(in, false) {
		return nil, ErrInvalidParam
	}
	return s.write(ctx, "update", in, operator)
}
func (s *Service) Delete(ctx context.Context, in Input, operator string) error {
	if in.ID == uuid.Nil || in.TenantID == uuid.Nil {
		return ErrInvalidParam
	}
	_, err := s.write(ctx, "delete", in, operator)
	return err
}
func (s *Service) write(ctx context.Context, action string, in Input, operator string) (*domain.Tag, error) {
	transactor, _ := s.repo.(auditapp.Transactor)
	event := func(id uuid.UUID, name, result string, changes []auditdomain.Change) *auditdomain.Event {
		return auditapp.NewResourceEvent(auditapp.ResourceEventInput{ActionCode: "tag." + action, ResultCode: result,
			ResourceType: "tag", ResourceID: id.String(), ResourceName: name, ScopeType: "tenant", ScopeID: in.TenantID.String(), Operator: operator, Changes: changes})
	}
	return auditapp.RunWrite(ctx, s.recorder, transactor, false, func(workCtx context.Context) (*domain.Tag, *auditdomain.Event, error) {
		if err := s.tenant(workCtx, in.TenantID); err != nil {
			return nil, nil, err
		}
		now := time.Now()
		item := &domain.Tag{ID: uuid.New(), TenantID: in.TenantID, Code: in.Code, AllowValueSearch: true, CreateBy: operator, CreateAt: now}
		var before domain.Tag
		if action != "create" {
			existing, err := s.repo.Get(workCtx, in.TenantID, in.ID)
			if err != nil {
				return nil, nil, err
			}
			before = *existing
			item = existing
			if in.Code != "" && in.Code != item.Code {
				return nil, nil, ErrInvalidParam
			}
		}
		var err error
		var changes []auditdomain.Change
		if action == "delete" {
			err = s.repo.Delete(workCtx, in.TenantID, in.ID, operator)
			changes = []auditdomain.Change{{Field: "isDeleted", Before: false, After: true, Changed: true}}
		} else {
			item.Name, item.Remark, item.UpdateBy, item.UpdateAt = in.Name, in.Remark, operator, now
			if in.AllowValueSearch != nil {
				item.AllowValueSearch = *in.AllowValueSearch
			}
			var beforeSearch any
			if action != "create" {
				beforeSearch = before.AllowValueSearch
			}
			changes = auditapp.ChangedFields(
				auditdomain.Change{Field: "code", Before: before.Code, After: item.Code},
				auditdomain.Change{Field: "name", Before: before.Name, After: item.Name},
				auditdomain.Change{Field: "remark", Before: before.Remark, After: item.Remark},
				auditdomain.Change{Field: "allowValueSearch", Before: beforeSearch, After: item.AllowValueSearch},
			)
			if action == "create" {
				err = s.repo.Create(workCtx, item)
			} else {
				err = s.repo.Update(workCtx, item)
			}
		}
		return item, event(item.ID, item.Name, auditdomain.ResultSuccess, changes), err
	}, func(err error) *auditdomain.Event {
		return auditapp.MarkFailure(event(in.ID, in.Name, auditdomain.ResultFailure, nil), err, ErrInvalidParam, ErrTenantNotFound, domain.ErrNotFound, domain.ErrCodeExists)
	})
}
func (s *Service) Info(ctx context.Context, in Input) (*domain.Tag, error) {
	if in.ID == uuid.Nil {
		return nil, ErrInvalidParam
	}
	if err := s.tenant(ctx, in.TenantID); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, in.TenantID, in.ID)
}
func (s *Service) List(ctx context.Context, filter domain.Filter) ([]*domain.Tag, int64, error) {
	if err := s.tenant(ctx, filter.TenantID); err != nil {
		return nil, 0, err
	}
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	return s.repo.List(ctx, filter)
}
