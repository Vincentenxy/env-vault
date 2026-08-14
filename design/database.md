# 数据库表设计

> 本文档记录 env-vault 项目所有数据库表结构（PostgreSQL），实现变更时必须同步更新本文档。
> 所有表结构使用 SQL 代码块描述。
>
> **命名约定**：信息类表统一使用 `_info` 后缀（如 `tenant_info`、`organization_info`）。
>
> **实体层级**：租户（tenant_info）→ 组织（organization_info）→ 项目（project_info）。
> 租户类比公司，组织类比公司部门，项目类比部门承接的具体项目。系统（system_info）暂未引入。
>
> **通用字段约定**：所有业务表必须包含以下字段
>
> | 列名 | 类型 | 说明 |
> |------|------|------|
> | `create_at` | `timestamptz NOT NULL DEFAULT now()` | 创建时间 |
> | `update_at` | `timestamptz NOT NULL DEFAULT now()` | 更新时间 |
> | `create_by` | `text NOT NULL DEFAULT ''` | 创建人 |
> | `update_by` | `text NOT NULL DEFAULT ''` | 更新人 |
> | `delete_at` | `timestamptz` | 删除时间（软删除，未删除为 NULL） |
> | `delete_by` | `text NOT NULL DEFAULT ''` | 删除人 |
> | `is_deleted` | `boolean NOT NULL DEFAULT false` | 软删除标记 |
>
> **硬性规则**：不使用外键；除 NOT NULL / 默认值 / 必要索引外不加数据库侧约束，业务校验在代码层面显式实现。

---

## 表名：`tenant_info`

**说明**：租户表。租户是系统内最顶级实体（类比公司），组织、系统等内容均归属租户之下。

```sql
CREATE TABLE IF NOT EXISTS tenant_info (
    id          uuid        PRIMARY KEY,
    code        text        NOT NULL,                 -- 租户编码
    name        text        NOT NULL,                 -- 租户名称
    remark      text        NOT NULL DEFAULT '',      -- 备注
    is_deleted  boolean     NOT NULL DEFAULT false,
    delete_at   timestamptz,
    delete_by   text        NOT NULL DEFAULT '',
    create_by   text        NOT NULL DEFAULT '',
    update_by   text        NOT NULL DEFAULT '',
    create_at   timestamptz NOT NULL DEFAULT now(),
    update_at   timestamptz NOT NULL DEFAULT now()
);

-- 按编码查询租户（唯一性在代码层面保证）
CREATE INDEX IF NOT EXISTS idx_tenant_info_code ON tenant_info (code);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_tenant_info_code` | `code` | 按编码定位租户 |

---

## 表名：`organization_info`

**说明**：组织表。组织归属租户之下（类比公司部门），`tenant_id` 关联租户（代码层面维护，无外键）。

```sql
CREATE TABLE IF NOT EXISTS organization_info (
    id          uuid        PRIMARY KEY,
    code        text        NOT NULL,                 -- 组织编码（租户内唯一）
    name        text        NOT NULL,                 -- 组织名称
    remark      text        NOT NULL DEFAULT '',      -- 备注（原 comment，PG 保留字已替换）
    tenant_id   uuid        NOT NULL,                 -- 所属租户 ID（代码层面关联，无外键）
    is_deleted  boolean     NOT NULL DEFAULT false,
    delete_at   timestamptz,
    delete_by   text        NOT NULL DEFAULT '',
    create_by   text        NOT NULL DEFAULT '',
    update_by   text        NOT NULL DEFAULT '',
    create_at   timestamptz NOT NULL DEFAULT now(),
    update_at   timestamptz NOT NULL DEFAULT now()
);

-- 按租户查询组织列表
CREATE INDEX IF NOT EXISTS idx_organization_info_tenant ON organization_info (tenant_id);
-- 租户内按编码查询组织（唯一性在代码层面保证）
CREATE INDEX IF NOT EXISTS idx_organization_info_tenant_code ON organization_info (tenant_id, code);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_organization_info_tenant` | `tenant_id` | 查询租户下全部组织 |
| `idx_organization_info_tenant_code` | `tenant_id, code` | 租户内按编码定位组织 |

---

## 表名：`project_info`

**说明**：项目表。项目归属组织之下（一个组织承接多个具体项目），`org_id` 关联组织（代码层面维护，无外键）。组织内编码唯一。

```sql
CREATE TABLE IF NOT EXISTS project_info (
    id          uuid        PRIMARY KEY,
    code        text        NOT NULL,                 -- 项目编码（组织内唯一）
    name        text        NOT NULL,                 -- 项目名称
    remark      text        NOT NULL DEFAULT '',      -- 备注
    org_id      uuid        NOT NULL,                 -- 所属组织 ID（代码层面关联，无外键）
    is_deleted  boolean     NOT NULL DEFAULT false,
    delete_at   timestamptz,
    delete_by   text        NOT NULL DEFAULT '',
    create_by   text        NOT NULL DEFAULT '',
    update_by   text        NOT NULL DEFAULT '',
    create_at   timestamptz NOT NULL DEFAULT now(),
    update_at   timestamptz NOT NULL DEFAULT now()
);

-- 按组织查询项目列表
CREATE INDEX IF NOT EXISTS idx_project_info_org ON project_info (org_id);
-- 组织内按编码查询项目（唯一性在代码层面保证）
CREATE INDEX IF NOT EXISTS idx_project_info_org_code ON project_info (org_id, code);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_project_info_org` | `org_id` | 查询组织下全部项目 |
| `idx_project_info_org_code` | `org_id, code` | 组织内按编码定位项目 |
