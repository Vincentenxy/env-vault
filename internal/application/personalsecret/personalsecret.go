// Package personalsecret implements current-user personal credential use cases.
package personalsecret

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	app "env-vault/internal/application"
	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	personaldomain "env-vault/internal/domain/personalsecret"
	userdomain "env-vault/internal/domain/user"
)

const CredentialTypePassword = "password"

var (
	ErrInvalidParam    = errors.New("invalid personal secret params")
	ErrNotFound        = errors.New("personal secret not found")
	ErrEncrypt         = errors.New("personal secret encryption failed")
	ErrDecrypt         = errors.New("personal secret decryption failed")
	ErrOwnerNotBlocked = errors.New("personal secret owner is not blocked")
	ErrVersionConflict = personaldomain.ErrVersionConflict
)

type CreateInput struct {
	Name           string
	CredentialType string
	Account        string
	LoginURL       string
	Value          string
	Remark         string
	CommitMsg      string
	UserID         string
}

type UpdateInput struct {
	ID             uuid.UUID
	Version        int
	Name           string
	CredentialType string
	Account        string
	LoginURL       string
	Value          string
	Remark         string
	CommitMsg      string
	UserID         string
}

type DeleteInput struct {
	ID      uuid.UUID
	Version int
	UserID  string
}

type ListInput struct {
	Keyword  string
	UserID   string
	PageNum  int
	PageSize int
}

type ManageListInput struct {
	Keyword      string
	TargetUserID string
	Operator     string
	PageNum      int
	PageSize     int
}

type RevealInput struct {
	ID     uuid.UUID
	UserID string
}

type HistoryInput struct {
	PersonalSecretID uuid.UUID
	UserID           string
	PageNum          int
	PageSize         int
}

type RevealHistoryInput struct {
	PersonalSecretID uuid.UUID
	HistoryID        uuid.UUID
	UserID           string
}

type SecretView struct {
	ID             uuid.UUID
	Name           string
	CredentialType string
	Account        string
	LoginURL       string
	Value          string
	Remark         string
	Version        int
	CreateBy       string
	CreateByName   string
	UpdateBy       string
	UpdateByName   string
	CreateAt       time.Time
	UpdateAt       time.Time
}

type HistoryView struct {
	ID               uuid.UUID
	PersonalSecretID uuid.UUID
	BatchID          uuid.UUID
	Name             string
	CredentialType   string
	Account          string
	LoginURL         string
	Remark           string
	Version          int
	CommitMsg        string
	CreateBy         string
	CreateByName     string
	CreateAt         time.Time
}

type RevealView struct {
	ID      uuid.UUID
	Value   string
	Version int
}

type HistoryRevealView struct {
	ID               uuid.UUID
	PersonalSecretID uuid.UUID
	Value            string
	Version          int
}

type userReader interface {
	GetProfile(ctx context.Context, userID string) (*userdomain.User, error)
	GetNickname(ctx context.Context, userID string) (string, error)
}

type valueCryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

type IService interface {
	Create(context.Context, CreateInput) (*SecretView, error)
	Update(context.Context, UpdateInput) (*SecretView, error)
	Delete(context.Context, DeleteInput) error
	List(context.Context, ListInput) ([]SecretView, int64, error)
	ManageList(context.Context, ManageListInput) ([]SecretView, int64, error)
	Reveal(context.Context, RevealInput) (*RevealView, error)
	History(context.Context, HistoryInput) ([]HistoryView, int64, error)
	RevealHistory(context.Context, RevealHistoryInput) (*HistoryRevealView, error)
}

type Service struct {
	repo          personaldomain.Repository
	users         userReader
	cipher        valueCryptor
	auditRecorder auditdomain.Recorder
}

func NewService(repo personaldomain.Repository, users userReader, cipher valueCryptor) *Service {
	return &Service{repo: repo, users: users, cipher: cipher}
}

func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
}

var _ IService = (*Service)(nil)

func (s *Service) Create(ctx context.Context, in CreateInput) (*SecretView, error) {
	normalizeCreate(&in)
	if !validCreate(in) {
		return nil, ErrInvalidParam
	}
	owner, err := s.owner(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.cipher.Encrypt(in.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncrypt, err)
	}

	now := time.Now()
	batchID := uuid.New()
	secret := &personaldomain.Secret{
		ID: uuid.New(), OwnerID: owner.ID, Name: in.Name, CredentialType: in.CredentialType,
		Account: in.Account, LoginURL: in.LoginURL, ValueCiphertext: ciphertext, Remark: in.Remark,
		Version: 1, CreateBy: in.UserID, UpdateBy: in.UserID, CreateAt: now, UpdateAt: now,
	}
	history := snapshot(secret, batchID, in.CommitMsg, in.UserID, now)
	result, err := auditapp.RunWrite(ctx, s.auditRecorder, s.repo, true,
		func(writeCtx context.Context) (*personaldomain.Secret, *auditdomain.Event, error) {
			if createErr := s.repo.Create(writeCtx, secret); createErr != nil {
				return nil, nil, createErr
			}
			if historyErr := s.repo.CreateHistory(writeCtx, history); historyErr != nil {
				return nil, nil, historyErr
			}
			return secret, personalEvent("personalSecret.create", auditdomain.ResultSuccess, secret, in.UserID,
				[]auditdomain.Change{
					{Field: "name", After: secret.Name}, {Field: "account", After: secret.Account},
					{Field: "loginUrl", After: secret.LoginURL}, {Field: "remark", After: secret.Remark},
					{Field: "value", Changed: true, Redacted: true},
				}, map[string]any{"version": secret.Version}), nil
		},
		func(operationErr error) *auditdomain.Event {
			return failureEvent(personalEvent("personalSecret.create", auditdomain.ResultFailure, secret, in.UserID, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	view := s.toSecretView(ctx, result)
	return &view, nil
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (*SecretView, error) {
	normalizeUpdate(&in)
	if !validUpdate(in) {
		return nil, ErrInvalidParam
	}
	owner, err := s.owner(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	current, err := s.repo.GetByIDAndOwner(ctx, in.ID, owner.ID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrNotFound
	}
	if current.Version != in.Version {
		return nil, ErrVersionConflict
	}

	valueCiphertext := current.ValueCiphertext
	valueChanged := false
	if in.Value != "" {
		currentValue, decryptErr := s.cipher.Decrypt(current.ValueCiphertext)
		if decryptErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrDecrypt, decryptErr)
		}
		if currentValue != in.Value {
			valueCiphertext, err = s.cipher.Encrypt(in.Value)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrEncrypt, err)
			}
			valueChanged = true
		}
	}
	metadataChanged := current.Name != in.Name || current.CredentialType != in.CredentialType ||
		current.Account != in.Account || current.LoginURL != in.LoginURL || current.Remark != in.Remark
	if !metadataChanged && !valueChanged {
		view := s.toSecretView(ctx, current)
		return &view, nil
	}

	now := time.Now()
	updated := *current
	updated.Name = in.Name
	updated.CredentialType = in.CredentialType
	updated.Account = in.Account
	updated.LoginURL = in.LoginURL
	updated.ValueCiphertext = valueCiphertext
	updated.Remark = in.Remark
	updated.Version = current.Version + 1
	updated.UpdateBy = in.UserID
	updated.UpdateAt = now
	history := snapshot(&updated, uuid.New(), in.CommitMsg, in.UserID, now)

	result, err := auditapp.RunWrite(ctx, s.auditRecorder, s.repo, true,
		func(writeCtx context.Context) (*personaldomain.Secret, *auditdomain.Event, error) {
			if updateErr := s.repo.Update(writeCtx, &updated, current.Version); updateErr != nil {
				return nil, nil, updateErr
			}
			if historyErr := s.repo.CreateHistory(writeCtx, history); historyErr != nil {
				return nil, nil, historyErr
			}
			changes := []auditdomain.Change{
				{Field: "name", Before: current.Name, After: updated.Name},
				{Field: "account", Before: current.Account, After: updated.Account},
				{Field: "loginUrl", Before: current.LoginURL, After: updated.LoginURL},
				{Field: "remark", Before: current.Remark, After: updated.Remark},
			}
			changes = auditapp.ChangedFields(changes...)
			if valueChanged {
				changes = append(changes, auditdomain.Change{Field: "value", Changed: true, Redacted: true})
			}
			return &updated, personalEvent("personalSecret.update", auditdomain.ResultSuccess, &updated, in.UserID,
				changes, map[string]any{"version": updated.Version}), nil
		},
		func(operationErr error) *auditdomain.Event {
			return failureEvent(personalEvent("personalSecret.update", auditdomain.ResultFailure, current, in.UserID, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	view := s.toSecretView(ctx, result)
	return &view, nil
}

func (s *Service) Delete(ctx context.Context, in DeleteInput) error {
	if in.ID == uuid.Nil || in.Version <= 0 || strings.TrimSpace(in.UserID) == "" {
		return ErrInvalidParam
	}
	in.UserID = strings.TrimSpace(in.UserID)
	owner, err := s.owner(ctx, in.UserID)
	if err != nil {
		return err
	}
	current, err := s.repo.GetByIDAndOwner(ctx, in.ID, owner.ID)
	if err != nil {
		return err
	}
	if current == nil {
		return ErrNotFound
	}
	if current.Version != in.Version {
		return ErrVersionConflict
	}
	_, err = auditapp.RunWrite(ctx, s.auditRecorder, s.repo, true,
		func(writeCtx context.Context) (struct{}, *auditdomain.Event, error) {
			affected, deleteErr := s.repo.SoftDelete(writeCtx, in.ID, owner.ID, in.Version, in.UserID, time.Now())
			if deleteErr != nil {
				return struct{}{}, nil, deleteErr
			}
			if affected != 1 {
				return struct{}{}, nil, ErrVersionConflict
			}
			return struct{}{}, personalEvent("personalSecret.delete", auditdomain.ResultSuccess, current, in.UserID, nil,
				map[string]any{"version": current.Version}), nil
		},
		func(operationErr error) *auditdomain.Event {
			return failureEvent(personalEvent("personalSecret.delete", auditdomain.ResultFailure, current, in.UserID, nil, nil), operationErr)
		},
	)
	return err
}

func (s *Service) List(ctx context.Context, in ListInput) ([]SecretView, int64, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	if in.UserID == "" || in.PageNum <= 0 || in.PageSize <= 0 {
		return nil, 0, ErrInvalidParam
	}
	owner, err := s.owner(ctx, in.UserID)
	if err != nil {
		return nil, 0, err
	}
	type listResult struct {
		items []SecretView
		total int64
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (listResult, error) {
			items, total, listErr := s.repo.List(readCtx, personaldomain.ListFilter{
				OwnerID: owner.ID, Keyword: strings.TrimSpace(in.Keyword),
				Offset: (in.PageNum - 1) * in.PageSize, Limit: in.PageSize,
			})
			if listErr != nil {
				return listResult{}, listErr
			}
			views := make([]SecretView, 0, len(items))
			for _, item := range items {
				value, decryptErr := s.cipher.Decrypt(item.ValueCiphertext)
				if decryptErr != nil {
					return listResult{total: total}, fmt.Errorf("%w: %v", ErrDecrypt, decryptErr)
				}
				view := s.toSecretView(readCtx, item)
				view.Value = value
				views = append(views, view)
			}
			return listResult{items: views, total: total}, nil
		},
		func(result listResult, operationErr error) *auditdomain.Event {
			return readEvent("personalSecret.list", "", "", in.UserID, operationErr,
				map[string]any{"total": result.total, "pageNum": in.PageNum, "pageSize": in.PageSize})
		},
	)
	if err != nil {
		return nil, 0, err
	}
	return result.items, result.total, nil
}

// ManageList returns decrypted credentials for a locked user. Authorization is
// intentionally reserved for the permission center; the locked-state check is
// enforced here so this endpoint cannot read an active user's credentials.
func (s *Service) ManageList(ctx context.Context, in ManageListInput) ([]SecretView, int64, error) {
	in.TargetUserID = strings.TrimSpace(in.TargetUserID)
	in.Operator = strings.TrimSpace(in.Operator)
	if in.TargetUserID == "" || in.Operator == "" || in.PageNum <= 0 || in.PageSize <= 0 {
		return nil, 0, ErrInvalidParam
	}
	type listResult struct {
		items []SecretView
		total int64
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (listResult, error) {
			owner, ownerErr := s.owner(readCtx, in.TargetUserID)
			if ownerErr != nil {
				return listResult{}, ownerErr
			}
			if !owner.IsBlocked {
				return listResult{}, ErrOwnerNotBlocked
			}
			items, total, listErr := s.repo.List(readCtx, personaldomain.ListFilter{
				OwnerID: owner.ID, Keyword: strings.TrimSpace(in.Keyword),
				Offset: (in.PageNum - 1) * in.PageSize, Limit: in.PageSize,
			})
			if listErr != nil {
				return listResult{}, listErr
			}
			views := make([]SecretView, 0, len(items))
			for _, item := range items {
				value, decryptErr := s.cipher.Decrypt(item.ValueCiphertext)
				if decryptErr != nil {
					return listResult{total: total}, fmt.Errorf("%w: %v", ErrDecrypt, decryptErr)
				}
				view := s.toSecretView(readCtx, item)
				view.Value = value
				views = append(views, view)
			}
			return listResult{items: views, total: total}, nil
		},
		func(result listResult, operationErr error) *auditdomain.Event {
			return readEvent("personalSecret.manage.list", in.TargetUserID, "", in.Operator, operationErr,
				map[string]any{
					"targetUserId": in.TargetUserID, "total": result.total,
					"pageNum": in.PageNum, "pageSize": in.PageSize,
				})
		},
	)
	if err != nil {
		return nil, 0, err
	}
	return result.items, result.total, nil
}

func (s *Service) Reveal(ctx context.Context, in RevealInput) (*RevealView, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	if in.ID == uuid.Nil || in.UserID == "" {
		return nil, ErrInvalidParam
	}
	owner, err := s.owner(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*RevealView, error) {
			item, readErr := s.repo.GetByIDAndOwner(readCtx, in.ID, owner.ID)
			if readErr != nil {
				return nil, readErr
			}
			if item == nil {
				return nil, ErrNotFound
			}
			value, decryptErr := s.cipher.Decrypt(item.ValueCiphertext)
			if decryptErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrDecrypt, decryptErr)
			}
			return &RevealView{ID: item.ID, Value: value, Version: item.Version}, nil
		},
		func(result *RevealView, operationErr error) *auditdomain.Event {
			return readEvent("personalSecret.reveal", in.ID.String(), "", in.UserID, operationErr, nil)
		},
	)
	return result, err
}

func (s *Service) History(ctx context.Context, in HistoryInput) ([]HistoryView, int64, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	if in.PersonalSecretID == uuid.Nil || in.UserID == "" || in.PageNum <= 0 || in.PageSize <= 0 {
		return nil, 0, ErrInvalidParam
	}
	owner, err := s.owner(ctx, in.UserID)
	if err != nil {
		return nil, 0, err
	}
	if err := s.ensureOwnedCurrent(ctx, in.PersonalSecretID, owner.ID); err != nil {
		return nil, 0, err
	}
	type historyResult struct {
		items []HistoryView
		total int64
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (historyResult, error) {
			items, total, historyErr := s.repo.ListHistory(readCtx, personaldomain.HistoryFilter{
				OwnerID: owner.ID, PersonalSecretID: in.PersonalSecretID,
				Offset: (in.PageNum - 1) * in.PageSize, Limit: in.PageSize,
			})
			if historyErr != nil {
				return historyResult{}, historyErr
			}
			views := make([]HistoryView, 0, len(items))
			for _, item := range items {
				views = append(views, s.toHistoryView(readCtx, item))
			}
			return historyResult{items: views, total: total}, nil
		},
		func(result historyResult, operationErr error) *auditdomain.Event {
			return readEvent("personalSecret.history", in.PersonalSecretID.String(), "", in.UserID, operationErr,
				map[string]any{"total": result.total, "pageNum": in.PageNum, "pageSize": in.PageSize})
		},
	)
	if err != nil {
		return nil, 0, err
	}
	return result.items, result.total, nil
}

func (s *Service) RevealHistory(ctx context.Context, in RevealHistoryInput) (*HistoryRevealView, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	if in.PersonalSecretID == uuid.Nil || in.HistoryID == uuid.Nil || in.UserID == "" {
		return nil, ErrInvalidParam
	}
	owner, err := s.owner(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureOwnedCurrent(ctx, in.PersonalSecretID, owner.ID); err != nil {
		return nil, err
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*HistoryRevealView, error) {
			item, readErr := s.repo.GetHistoryByIDAndOwner(readCtx, in.HistoryID, in.PersonalSecretID, owner.ID)
			if readErr != nil {
				return nil, readErr
			}
			if item == nil {
				return nil, ErrNotFound
			}
			value, decryptErr := s.cipher.Decrypt(item.ValueCiphertext)
			if decryptErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrDecrypt, decryptErr)
			}
			return &HistoryRevealView{
				ID: item.ID, PersonalSecretID: item.PersonalSecretID, Value: value, Version: item.Version,
			}, nil
		},
		func(result *HistoryRevealView, operationErr error) *auditdomain.Event {
			return readEvent("personalSecret.history.reveal", in.PersonalSecretID.String(), "", in.UserID, operationErr,
				map[string]any{"historyId": in.HistoryID.String()})
		},
	)
	return result, err
}

func (s *Service) owner(ctx context.Context, userID string) (*userdomain.User, error) {
	if s.users == nil {
		return nil, ErrNotFound
	}
	user, err := s.users.GetProfile(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	if user == nil || user.ID == uuid.Nil || user.IsDeleted {
		return nil, ErrNotFound
	}
	return user, nil
}

func (s *Service) ensureOwnedCurrent(ctx context.Context, id, ownerID uuid.UUID) error {
	item, err := s.repo.GetByIDAndOwner(ctx, id, ownerID)
	if err != nil {
		return err
	}
	if item == nil {
		return ErrNotFound
	}
	return nil
}

func (s *Service) toSecretView(ctx context.Context, item *personaldomain.Secret) SecretView {
	return SecretView{
		ID: item.ID, Name: item.Name, CredentialType: item.CredentialType,
		Account: item.Account, LoginURL: item.LoginURL, Remark: item.Remark, Version: item.Version,
		CreateBy: item.CreateBy, CreateByName: app.ResolveNickname(ctx, s.users, item.CreateBy),
		UpdateBy: item.UpdateBy, UpdateByName: app.ResolveNickname(ctx, s.users, item.UpdateBy),
		CreateAt: item.CreateAt, UpdateAt: item.UpdateAt,
	}
}

func (s *Service) toHistoryView(ctx context.Context, item *personaldomain.History) HistoryView {
	return HistoryView{
		ID: item.ID, PersonalSecretID: item.PersonalSecretID, BatchID: item.BatchID,
		Name: item.Name, CredentialType: item.CredentialType, Account: item.Account,
		LoginURL: item.LoginURL, Remark: item.Remark, Version: item.Version, CommitMsg: item.CommitMsg,
		CreateBy: item.CreateBy, CreateByName: app.ResolveNickname(ctx, s.users, item.CreateBy), CreateAt: item.CreateAt,
	}
}

func snapshot(secret *personaldomain.Secret, batchID uuid.UUID, commitMsg, operator string, now time.Time) *personaldomain.History {
	return &personaldomain.History{
		ID: uuid.New(), PersonalSecretID: secret.ID, BatchID: batchID, OwnerID: secret.OwnerID,
		Name: secret.Name, CredentialType: secret.CredentialType, Account: secret.Account,
		LoginURL: secret.LoginURL, ValueCiphertext: secret.ValueCiphertext, Remark: secret.Remark,
		Version: secret.Version, CommitMsg: commitMsg, CreateBy: operator, CreateAt: now,
	}
}

func normalizeCreate(in *CreateInput) {
	in.Name = strings.TrimSpace(in.Name)
	in.CredentialType = strings.TrimSpace(in.CredentialType)
	if in.CredentialType == "" {
		in.CredentialType = CredentialTypePassword
	}
	in.Account = strings.TrimSpace(in.Account)
	in.LoginURL = strings.TrimSpace(in.LoginURL)
	in.Remark = strings.TrimSpace(in.Remark)
	in.CommitMsg = strings.TrimSpace(in.CommitMsg)
	if in.CommitMsg == "" {
		in.CommitMsg = "personal secret created"
	}
	in.UserID = strings.TrimSpace(in.UserID)
}

func normalizeUpdate(in *UpdateInput) {
	in.Name = strings.TrimSpace(in.Name)
	in.CredentialType = strings.TrimSpace(in.CredentialType)
	if in.CredentialType == "" {
		in.CredentialType = CredentialTypePassword
	}
	in.Account = strings.TrimSpace(in.Account)
	in.LoginURL = strings.TrimSpace(in.LoginURL)
	in.Remark = strings.TrimSpace(in.Remark)
	in.CommitMsg = strings.TrimSpace(in.CommitMsg)
	in.UserID = strings.TrimSpace(in.UserID)
}

func validCreate(in CreateInput) bool {
	return in.Name != "" && in.Value != "" && in.UserID != "" && in.CredentialType == CredentialTypePassword
}

func validUpdate(in UpdateInput) bool {
	return in.ID != uuid.Nil && in.Version > 0 && in.Name != "" && in.CommitMsg != "" &&
		in.UserID != "" && in.CredentialType == CredentialTypePassword
}

func personalEvent(action, result string, secret *personaldomain.Secret, operator string, changes []auditdomain.Change, detail map[string]any) *auditdomain.Event {
	resourceID, resourceName, scopeID := "", "", ""
	if secret != nil {
		resourceID, resourceName, scopeID = secret.ID.String(), secret.Name, secret.OwnerID.String()
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: "personalSecret",
		ResourceID: resourceID, ResourceName: resourceName, ScopeType: "user",
		ScopeID: scopeID, Operator: operator, Changes: changes, Detail: detail,
	})
}

func readEvent(action, resourceID, resourceName, operator string, operationErr error, detail map[string]any) *auditdomain.Event {
	event := auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: auditdomain.ResultSuccess, ResourceType: "personalSecret",
		ResourceID: resourceID, ResourceName: resourceName, ScopeType: "user",
		Operator: operator, Detail: detail,
	})
	if operationErr != nil {
		return failureEvent(event, operationErr)
	}
	return event
}

func failureEvent(event *auditdomain.Event, operationErr error) *auditdomain.Event {
	return auditapp.MarkFailure(event, operationErr, ErrInvalidParam, ErrNotFound, ErrEncrypt, ErrDecrypt,
		ErrOwnerNotBlocked, ErrVersionConflict)
}
