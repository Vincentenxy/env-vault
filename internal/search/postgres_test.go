package search

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tagrepo "env-vault/internal/infrastructure/persistence/tag"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 每个测试使用独立 schema，只连接显式提供的测试数据库
func testDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("ENV_VAULT_SEARCH_TEST_DSN")
	if dsn == "" {
		t.Skip("set ENV_VAULT_SEARCH_TEST_DSN for PostgreSQL integration tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := "search_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err = admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema+",public"), &gorm.Config{})
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
CREATE TABLE tenant_info(id uuid PRIMARY KEY,name text DEFAULT 'Tenant',is_deleted boolean DEFAULT false);
CREATE TABLE organization_info(id uuid PRIMARY KEY,tenant_id uuid,name text DEFAULT 'Org',is_deleted boolean DEFAULT false);
CREATE TABLE project_info(id uuid PRIMARY KEY,org_id uuid,name text DEFAULT 'Project',is_deleted boolean DEFAULT false);
CREATE TABLE environment_info(id uuid PRIMARY KEY,project_id uuid,code text,name text,order_no numeric DEFAULT 10,is_deleted boolean DEFAULT false);
CREATE TABLE folder_info(id uuid PRIMARY KEY,group_id uuid,env_id uuid,parent_folder_id uuid,name text,code text,is_deleted boolean DEFAULT false);
CREATE TABLE secret_info(id uuid PRIMARY KEY,group_id uuid,folder_id uuid,env_code text,key text,remark text DEFAULT '',value_ciphertext text DEFAULT 'cipher',version integer DEFAULT 1,update_at timestamptz DEFAULT now(),is_deleted boolean DEFAULT false);
CREATE INDEX search_group_idx ON secret_info(group_id);
CREATE INDEX search_folder_idx ON secret_info(folder_id);
CREATE TABLE tag_info(id uuid PRIMARY KEY,code text,name text,allow_value_search boolean DEFAULT false,is_deleted boolean DEFAULT false);
CREATE TABLE secret_tag_relation(target_id uuid,tag_id uuid,target_type text DEFAULT 'group',is_deleted boolean DEFAULT false);
`
	if err = db.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

type fixture struct {
	tenant, org, project, parent, folder uuid.UUID
	folders                              []uuid.UUID
	envs                                 []uuid.UUID
}

func testFixture(t *testing.T, db *gorm.DB) fixture {
	t.Helper()
	f := fixture{tenant: uuid.New(), org: uuid.New(), project: uuid.New(), parent: uuid.New(), folder: uuid.New()}
	exec := func(query string, args ...any) {
		t.Helper()
		if err := db.Exec(query, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tenant_info(id) VALUES (?)", f.tenant)
	exec("INSERT INTO organization_info(id,tenant_id) VALUES (?,?)", f.org, f.tenant)
	exec("INSERT INTO project_info(id,org_id) VALUES (?,?)", f.project, f.org)
	for i, code := range []string{"dev", "test", "sim", "prod"} {
		env, parent, folder := uuid.New(), uuid.New(), uuid.New()
		f.folders = append(f.folders, folder)
		f.envs = append(f.envs, env)
		exec("INSERT INTO environment_info(id,project_id,code,name,order_no) VALUES (?,?,?,?,?)", env, f.project, code, code, (i+1)*10)
		exec("INSERT INTO folder_info(id,group_id,env_id,name,code) VALUES (?,?,?,'Groups','groups')", parent, f.parent, env)
		exec("INSERT INTO folder_info(id,group_id,env_id,parent_folder_id,name,code) VALUES (?,?,?,?,'Common','common')", folder, f.folder, env, parent)
	}
	return f
}

func addGroup(t *testing.T, db *gorm.DB, f fixture, key, remark string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	for i, folder := range f.folders {
		if err := db.Exec("INSERT INTO secret_info(id,group_id,folder_id,env_code,key,remark,value_ciphertext,version) VALUES (?,?,?,?,?,?,?,?)", uuid.New(), id, folder, []string{"dev", "test", "sim", "prod"}[i], key, remark, "cipher-"+key, i+1).Error; err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestPostgresContainsAndGroupPagination(t *testing.T) {
	db := testDatabase(t)
	f := testFixture(t, db)
	addGroup(t, db, f, "DB_HOST", "数据库连接")
	addGroup(t, db, f, "DBXHOST", "other")
	addGroup(t, db, f, "100%READY!", "字面百分号")
	addGroup(t, db, f, "SECOND", "host remark")
	repo := &repository{db: db, tags: tagrepo.NewRepository(db)}
	for _, test := range []struct {
		keyword string
		want    int64
	}{{"host", 3}, {"DB_HOST", 1}, {"100%READY!", 1}, {"数据库", 1}, {"missing", 0}} {
		in := Input{Keyword: test.keyword, PageNum: 1, PageSize: 1, UserID: "reader"}
		page, err := repo.read(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != test.want {
			t.Fatalf("%q total=%d want %d", test.keyword, page.Total, test.want)
		}
		if test.want > 0 && (len(page.Groups) != 1 || len(page.Groups[0].Environments) != 4) {
			t.Fatal("group split across pages")
		}
	}
	in := Input{Keyword: "HOST", PageNum: 1, PageSize: 1, UserID: "reader"}
	first, err := repo.read(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	in.PageNum = 2
	second, err := repo.read(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Groups[0].GroupID == second.Groups[0].GroupID {
		t.Fatal("unstable pagination")
	}
	in.PageNum = 999
	last, err := repo.read(context.Background(), in)
	if err != nil || last.Total != 3 || len(last.Groups) != 0 {
		t.Fatalf("bad empty page: %+v %v", last, err)
	}
}

func TestPostgresScopeEnvironmentAndSoftDeletion(t *testing.T) {
	db := testDatabase(t)
	f := testFixture(t, db)
	other := testFixture(t, db)
	id := addGroup(t, db, f, "DOMAIN", "service")
	addGroup(t, db, other, "DOMAIN", "same key different project")
	tag := uuid.New()
	if err := db.Exec("INSERT INTO tag_info(id,code,name) VALUES (?,'service','Service')", tag).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO secret_tag_relation(target_id,tag_id) VALUES (?,?)", id, tag).Error; err != nil {
		t.Fatal(err)
	}
	repo := &repository{db: db, tags: tagrepo.NewRepository(db)}
	in := Input{Keyword: "DOMAIN", Scopes: []Scope{{Type: "folder", ID: f.parent}, {Type: "folder", ID: f.folder}}, EnvList: []string{"prod"}, PageNum: 1, PageSize: 20, UserID: "reader"}
	page, err := repo.read(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Groups[0].Environments) != 1 || page.Groups[0].Environments[0].EnvCode != "prod" || len(page.Groups[0].Scope.Folders) != 2 || len(page.Groups[0].TagList) != 1 {
		t.Fatalf("bad scoped page: %+v", page)
	}
	in.EnvList = nil
	if _, err = repo.read(context.Background(), in); !errors.Is(err, ErrEnvironment) {
		t.Fatalf("missing env accepted: %v", err)
	}
	in.Scopes = append(in.Scopes, Scope{Type: "project", ID: other.project})
	in.EnvList = []string{"prod"}
	if _, err = repo.read(context.Background(), in); !errors.Is(err, ErrEnvironment) {
		t.Fatalf("cross-project env accepted: %v", err)
	}
	in.EnvList = nil
	page, err = repo.read(context.Background(), in)
	if err != nil || page.Total != 2 {
		t.Fatalf("multi-scope search: %v %v", page.Total, err)
	}
	// 混合租户与项目范围时，也要识别已失效的项目，不能悄悄忽略无效条件
	in.Scopes = []Scope{{Type: "tenant", ID: f.tenant}, {Type: "project", ID: uuid.New()}}
	if _, err = repo.read(context.Background(), in); !errors.Is(err, ErrScope) {
		t.Fatalf("invalid mixed scope accepted: %v", err)
	}
	in.Scopes[1].ID = other.project
	page, err = repo.read(context.Background(), in)
	if err != nil || page.Total != 2 {
		t.Fatalf("valid mixed scope rejected: %v %v", page.Total, err)
	}
	if err = db.Exec("UPDATE folder_info SET is_deleted=true WHERE group_id=?", f.parent).Error; err != nil {
		t.Fatal(err)
	}
	in.Scopes = nil
	page, err = repo.read(context.Background(), in)
	if err != nil || page.Total != 1 {
		t.Fatalf("deleted parent returned: %v %v", page.Total, err)
	}
	if err = db.Exec("UPDATE tenant_info SET is_deleted=true WHERE id=?", other.tenant).Error; err != nil {
		t.Fatal(err)
	}
	page, err = repo.read(context.Background(), in)
	if err != nil || page.Total != 0 {
		t.Fatal("deleted tenant returned")
	}
}

func TestPostgresServiceReadsOnlyCurrentPageValues(t *testing.T) {
	db := testDatabase(t)
	f := testFixture(t, db)
	addGroup(t, db, f, "FIRST", "match")
	addGroup(t, db, f, "SECOND", "match")
	calls := 0
	svc := NewService(db, decryptFunc(func(cipher string) (string, error) {
		calls++
		if cipher == "cipher-SECOND" {
			t.Fatal("decrypted off-page value")
		}
		return "", nil
	}), tagrepo.NewRepository(db), nil, time.Second)
	result, err := svc.Search(context.Background(), Input{Keyword: "match", PageNum: 1, PageSize: 1, UserID: "reader"})
	if err != nil || result.Total != 2 || len(result.List) != 1 || calls != 4 {
		t.Fatalf("bad result: %+v %v calls=%d", result, err, calls)
	}
	for _, env := range result.List[0].Values {
		if env.Value != "" {
			t.Fatal("empty value corrupted")
		}
	}
}
