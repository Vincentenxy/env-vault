package tag_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	secretapp "env-vault/internal/application/secret"
	auditdomain "env-vault/internal/domain/audit"
	secretdomain "env-vault/internal/domain/secret"
	tagdomain "env-vault/internal/domain/tag"
	"env-vault/internal/infrastructure/persistence"
	folderrepo "env-vault/internal/infrastructure/persistence/folder"
	secretrepo "env-vault/internal/infrastructure/persistence/secret"
	tagrepo "env-vault/internal/infrastructure/persistence/tag"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 每次测试独立 schema，只连接显式配置的测试数据库
func tagTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("ENV_VAULT_TAG_TEST_DSN")
	if dsn == "" {
		t.Skip("set ENV_VAULT_TAG_TEST_DSN to run PostgreSQL integration tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := "tag_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		_ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		adminDB, _ := admin.DB()
		_ = adminDB.Close()
	})
	ddl := `
CREATE TABLE tenant_info (id uuid PRIMARY KEY, is_deleted boolean DEFAULT false);
CREATE TABLE organization_info (id uuid PRIMARY KEY, tenant_id uuid, is_deleted boolean DEFAULT false);
CREATE TABLE project_info (id uuid PRIMARY KEY, org_id uuid, is_deleted boolean DEFAULT false);
CREATE TABLE environment_info (id uuid PRIMARY KEY, project_id uuid, code text, is_deleted boolean DEFAULT false);
CREATE TABLE folder_info (id uuid PRIMARY KEY, group_id uuid, env_id uuid, parent_folder_id uuid, code text, is_deleted boolean DEFAULT false,
 delete_at timestamptz, delete_by text, update_at timestamptz, update_by text);
CREATE TABLE secret_info (id uuid PRIMARY KEY, group_id uuid, folder_id uuid, env_code text, key text, value_ciphertext text DEFAULT 'test-value',
 value_type text DEFAULT '', remark text DEFAULT '', version integer DEFAULT 1, is_deleted boolean DEFAULT false,
 delete_at timestamptz, delete_by text, create_at timestamptz DEFAULT now(), create_by text, update_at timestamptz DEFAULT now(), update_by text);
CREATE TABLE tag_info (id uuid PRIMARY KEY, tenant_id uuid, code text, name text, remark text DEFAULT '', allow_value_search boolean DEFAULT true,
 is_deleted boolean DEFAULT false, delete_at timestamptz, delete_by text, create_at timestamptz DEFAULT now(), create_by text, update_at timestamptz DEFAULT now(), update_by text);
CREATE TABLE secret_tag_relation (id uuid PRIMARY KEY, tag_id uuid, target_type text, target_id uuid, is_deleted boolean DEFAULT false,
 delete_at timestamptz, delete_by text, create_at timestamptz DEFAULT now(), create_by text, update_at timestamptz DEFAULT now(), update_by text);
CREATE UNIQUE INDEX relation_active ON secret_tag_relation(target_type,target_id,tag_id) WHERE is_deleted=false;
CREATE TABLE test_audit (action_code text, result_code text);
`
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

type tagFixture struct {
	tenant, project, parentGroup, group, otherGroup, tag, otherTag, foreignTag uuid.UUID
	folders                                                                    []uuid.UUID
}

type plainTestCryptor struct{}

func (plainTestCryptor) Encrypt(value string) (string, error) { return value, nil }
func (plainTestCryptor) Decrypt(value string) (string, error) { return value, nil }

func fixture(t *testing.T, db *gorm.DB) tagFixture {
	t.Helper()
	f := tagFixture{tenant: uuid.New(), project: uuid.New(), parentGroup: uuid.New(), group: uuid.New(), otherGroup: uuid.New(), tag: uuid.New(), otherTag: uuid.New(), foreignTag: uuid.New()}
	org := uuid.New()
	exec := func(sql string, args ...any) {
		if err := db.Exec(sql, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tenant_info(id) VALUES (?)", f.tenant)
	exec("INSERT INTO organization_info(id,tenant_id) VALUES (?,?)", org, f.tenant)
	exec("INSERT INTO project_info(id,org_id) VALUES (?,?)", f.project, org)
	childGroup := uuid.New()
	for _, env := range []string{"dev", "prod"} {
		envID, parentID, folderID := uuid.New(), uuid.New(), uuid.New()
		f.folders = append(f.folders, folderID)
		exec("INSERT INTO environment_info(id,project_id,code) VALUES (?,?,?)", envID, f.project, env)
		exec("INSERT INTO folder_info(id,group_id,env_id,code) VALUES (?,?,?,'groups')", parentID, f.parentGroup, envID)
		exec("INSERT INTO folder_info(id,group_id,env_id,parent_folder_id,code) VALUES (?,?,?,?,'config')", folderID, childGroup, envID, parentID)
		exec("INSERT INTO secret_info(id,group_id,folder_id,env_code,key) VALUES (?,?,?,?,'PASSWORD')", uuid.New(), f.group, folderID, env)
		exec("INSERT INTO secret_info(id,group_id,folder_id,env_code,key) VALUES (?,?,?,?,'DOMAIN')", uuid.New(), f.otherGroup, folderID, env)
	}
	exec("INSERT INTO tag_info(id,tenant_id,code,name) VALUES (?,?,'password','Password'), (?,?,'important','Important'), (?,?,'foreign','Foreign')",
		f.tag, f.tenant, f.otherTag, f.tenant, f.foreignTag, uuid.New())
	return f
}

type auditRecorder struct {
	db   *gorm.DB
	fail bool
}

func (r *auditRecorder) Record(ctx context.Context, event *auditdomain.Event) error {
	if r.fail && event.ResultCode == auditdomain.ResultSuccess {
		return errors.New("audit write failed")
	}
	return persistence.TxDB(ctx, r.db).Exec("INSERT INTO test_audit VALUES (?,?)", event.ActionCode, event.ResultCode).Error
}
func (r *auditRecorder) RecordBatch(ctx context.Context, events []*auditdomain.Event) error {
	for _, event := range events {
		if err := r.Record(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func countActive(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Table("secret_tag_relation").Where("is_deleted=false").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestGroupTagsLifecycleAndFilters(t *testing.T) {
	db := tagTestDB(t)
	f := fixture(t, db)
	ctx := context.Background()
	repo := tagrepo.NewRepository(db)
	audit := &auditRecorder{db: db}
	svc := secretapp.NewTagService(repo, audit)
	result, err := svc.Update(ctx, f.group, []uuid.UUID{f.tag, f.tag, f.otherTag}, "tester")
	if err != nil || len(result.TagList) != 2 || result.TenantID != f.tenant || countActive(t, db) != 2 {
		t.Fatalf("bind: result=%+v err=%v", result, err)
	}
	var relationCount int64
	if _, err := svc.Update(ctx, f.group, []uuid.UUID{f.otherTag, f.tag}, "tester"); err != nil {
		t.Fatal(err)
	}
	db.Table("secret_tag_relation").Count(&relationCount)
	if relationCount != 2 {
		t.Fatalf("repeat created duplicate relations: %d", relationCount)
	}
	if _, err := svc.Update(ctx, f.otherGroup, []uuid.UUID{f.tag}, "tester"); err != nil {
		t.Fatal(err)
	}

	secrets := secretrepo.NewRepository(db)
	viewSvc := secretapp.NewService(secrets, nil, nil, plainTestCryptor{}).WithTags(repo)
	view, err := viewSvc.GetByGroup(ctx, f.group)
	if err != nil || len(view.Values) != 2 || len(view.TagList) != 2 {
		t.Fatalf("aggregate tags across environments: view=%+v err=%v", view, err)
	}
	// 新环境共享 groupId 后立即获得原标签，不复制关联行
	if err := db.Exec("INSERT INTO secret_info(id,group_id,folder_id,env_code,key) VALUES (?,?,?,'stage','PASSWORD')", uuid.New(), f.group, f.folders[0]).Error; err != nil {
		t.Fatal(err)
	}
	view, err = viewSvc.GetByGroup(ctx, f.group)
	if err != nil || len(view.Values) != 3 || len(view.TagList) != 2 {
		t.Fatalf("new environment lost tags: %v", err)
	}
	if err := db.Exec("DELETE FROM secret_info WHERE env_code = 'stage'").Error; err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]uuid.UUID{nil, {f.tag, f.otherTag}, {f.otherTag}, {f.foreignTag}} {
		want := 4
		if len(ids) == 1 {
			want = 2
			if ids[0] == f.foreignTag {
				want = 0
			}
		}
		items, err := secrets.ListByFolderIDs(ctx, f.folders, ids...)
		if err != nil || len(items) != want {
			t.Fatalf("folder filter got=%d want=%d err=%v", len(items), want, err)
		}
		items, err = secrets.ListByProjectFolder(ctx, secretdomain.ProjectFolderListFilter{ProjectID: f.project, FolderCode: "config", EnvCodes: []string{"dev", "prod"}, TagIDs: ids})
		if err != nil || len(items) != want {
			t.Fatalf("project filter got=%d want=%d err=%v", len(items), want, err)
		}
	}
	if _, err := svc.Update(ctx, f.group, []uuid.UUID{f.tag, f.foreignTag}, "tester"); !errors.Is(err, tagdomain.ErrTagNotInTenant) {
		t.Fatalf("foreign tenant: %v", err)
	}
	if countActive(t, db) != 3 {
		t.Fatal("invalid replacement changed relations")
	}
	audit.fail = true
	if _, err := svc.Update(ctx, f.group, []uuid.UUID{}, "tester"); err == nil {
		t.Fatal("expected audit failure")
	}
	if countActive(t, db) != 3 {
		t.Fatal("audit failure did not roll back")
	}
	audit.fail = false
	if err := repo.Delete(ctx, f.tenant, f.tag, "tester"); err != nil {
		t.Fatal(err)
	}
	if countActive(t, db) != 1 {
		t.Fatal("tag deletion did not clean all groups")
	}
	if _, err := svc.Update(ctx, f.group, []uuid.UUID{f.tag}, "tester"); !errors.Is(err, tagdomain.ErrTagNotInTenant) {
		t.Fatalf("deleted tag: %v", err)
	}
	if _, err := svc.Update(ctx, f.group, []uuid.UUID{}, "tester"); err != nil {
		t.Fatal(err)
	}
	if countActive(t, db) != 0 {
		t.Fatal("clear did not unlink")
	}
	if _, err := svc.Update(ctx, f.group, []uuid.UUID{f.otherTag}, "tester"); err != nil {
		t.Fatal(err)
	}
	var changed int64
	db.Table("secret_info").Where("version <> 1 OR value_ciphertext <> 'test-value'").Count(&changed)
	if changed != 0 {
		t.Fatal("tag edit changed secret values or versions")
	}
	if _, err := secrets.DeleteByGroupID(ctx, f.group, "tester"); err != nil {
		t.Fatal(err)
	}
	if countActive(t, db) != 0 {
		t.Fatal("secret deletion did not unlink")
	}
	if _, err := svc.Update(ctx, f.group, []uuid.UUID{f.otherTag}, "tester"); !errors.Is(err, tagdomain.ErrGroupNotFound) {
		t.Fatalf("deleted group: %v", err)
	}
}

func TestGroupTagsFolderDeletionAndConcurrency(t *testing.T) {
	for _, deleting := range []string{"tag", "secret", "parentFolder"} {
		t.Run(deleting, func(t *testing.T) {
			db := tagTestDB(t)
			f := fixture(t, db)
			ctx := context.Background()
			repo := tagrepo.NewRepository(db)
			svc := secretapp.NewTagService(repo, nil)
			bound := make(chan struct{})
			release := make(chan struct{})
			bindDone := make(chan error, 1)
			go func() {
				bindDone <- repo.WithTx(ctx, func(txCtx context.Context) error {
					group, err := repo.GetSecretGroup(txCtx, f.group, true)
					if err != nil {
						return err
					}
					if _, _, err := repo.ReplaceGroupTags(txCtx, group, []uuid.UUID{f.tag}, "tester"); err != nil {
						return err
					}
					close(bound)
					<-release
					return nil
				})
			}()
			select {
			case <-bound:
			case err := <-bindDone:
				t.Fatalf("bind before deletion: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("bind timeout")
			}
			deleteDone := make(chan error, 1)
			go func() {
				var err error
				switch deleting {
				case "tag":
					err = repo.Delete(ctx, f.tenant, f.tag, "tester")
				case "secret":
					_, err = secretrepo.NewRepository(db).DeleteByGroupID(ctx, f.group, "tester")
				case "parentFolder":
					_, err = folderrepo.NewRepository(db).DeleteByGroupID(ctx, f.parentGroup, "tester")
				}
				deleteDone <- err
			}()
			select {
			case err := <-deleteDone:
				close(release)
				t.Fatalf("delete bypassed pending bind: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			if err := <-bindDone; err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-deleteDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("delete timeout")
			}
			if countActive(t, db) != 0 {
				t.Fatal("concurrent delete left active relations")
			}
			if _, err := svc.Update(ctx, f.group, []uuid.UUID{f.tag}, "tester"); err == nil {
				t.Fatal("rebound deleted resource")
			}
		})
	}
}
