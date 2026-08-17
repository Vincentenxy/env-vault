# 数据库表设计

> 本文档记录 env-vault 项目所有数据库表结构（PostgreSQL），实现变更时必须同步更新本文档。
> 所有表结构使用 SQL 代码块描述。
>
> **命名约定**：信息类表统一使用 `_info` 后缀（如 `tenant_info`、`organization_info`）。
>
> **实体层级**：租户（tenant_info）→ 组织（organization_info）→ 项目（project_info）→ 环境（environment_info）→ 文件夹（folder_info）。
> 租户类比公司，组织类比公司部门，项目类比部门承接的具体项目，环境类比项目下的部署环境（开发/测试/仿真/生产），文件夹类比环境下的目录结构。系统（system_info）暂未引入。
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

---

## 表名：`environment_info`

**说明**：环境表。环境归属项目之下（一个项目包含多个部署环境，如开发/测试/仿真/生产），`project_id` 关联项目（代码层面维护，无外键）。项目内编码唯一。

```sql
CREATE TABLE IF NOT EXISTS environment_info (
    id            uuid        PRIMARY KEY,
    code          text        NOT NULL,                 -- 环境编码（项目内唯一，如 dev/test/sim/prod/poc）
    name          text        NOT NULL,                 -- 环境名称（如 开发环境/测试环境）
    remark        text        NOT NULL DEFAULT '',      -- 备注
    project_id    uuid        NOT NULL,                 -- 所属项目 ID（代码层面关联，无外键）
    order_no      integer     NOT NULL DEFAULT 0,       -- 排序号（间隔预留：dev-10/test-20/sim-30/prod-40，便于后续中间插入环境）
    is_check_perm boolean     NOT NULL DEFAULT false,   -- 是否进行权限校验（如 dev/test 为 false，sim/prod 为 true）
    is_deleted    boolean     NOT NULL DEFAULT false,
    delete_at     timestamptz,
    delete_by     text        NOT NULL DEFAULT '',
    create_by     text        NOT NULL DEFAULT '',
    update_by     text        NOT NULL DEFAULT '',
    create_at     timestamptz NOT NULL DEFAULT now(),
    update_at     timestamptz NOT NULL DEFAULT now()
);

-- 按项目查询环境列表
CREATE INDEX IF NOT EXISTS idx_environment_info_project ON environment_info (project_id);
-- 项目内按编码查询环境（唯一性在代码层面保证）
CREATE INDEX IF NOT EXISTS idx_environment_info_project_code ON environment_info (project_id, code);
-- 项目内按排序号展示环境列表
CREATE INDEX IF NOT EXISTS idx_environment_info_project_order ON environment_info (project_id, order_no);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_environment_info_project` | `project_id` | 查询项目下全部环境 |
| `idx_environment_info_project_code` | `project_id, code` | 项目内按编码定位环境 |
| `idx_environment_info_project_order` | `project_id, order_no` | 项目内按排序号展示环境列表 |

**order_no 排序策略说明**（业务逻辑，代码层实现）：

- 创建接口为批量入参（数组），一次可创建多个环境，`order_no` 按列表顺序自动填充：第 1 个 10，第 2 个 20，第 3 个 30……（基础环境 dev-10 / test-20 / sim-30 / prod-40 即按此顺序批量创建）
- 后续如需在中间插入环境，可通过更新接口调整已有环境的 `order_no` 留出间隔（如 dev 与 test 之间插 15），或先插入后逐个调整排序

---

## 表名：`folder_info`

**说明**：文件夹表。文件夹归属环境之下（`env_id` 关联环境，代码层面维护，无外键），`parent_folder_id` 自关联表示层级（NULL 为顶层，非 NULL 为二级），最多 2 层。

**业务组（`group_id`）约定**：业务上"项目下的一个 folder"在物理上展开为该项目每个环境下的各一条记录（创建时对项目下所有环境批量落库）。所有这些环境实例**共享同一个 `group_id`**，即"业务上是同一个 folder"用一个显式字段 `group_id` 关联。`group_id` 在创建时由服务端一次性生成，全环境共享。`group_id` 的引入将后续：
- **聚合查询**：直接按 `group_id` 过滤（替代之前的 `DISTINCT ON (code)` 方案）
- **批量更新**：按 `group_id` 一次更新全环境（替代之前传 `idList`）
- **批量删除**：按 `group_id` 一次删除全环境
- **后续子资源**（变量、密钥等）可通过挂 `folder_group_id` 关联

**保留字评估**：`type` 在 PostgreSQL 中属于 non-reserved 关键字（不可用作函数名/类型名），**可安全用作列名**；`folder` 非关键字。

```sql
CREATE TABLE IF NOT EXISTS folder_info (
    id               uuid        PRIMARY KEY,
    group_id         uuid        NOT NULL,                 -- 业务组 ID：跨环境共享同一 group_id 的 folder 视为"业务上是同一个 folder"（创建时全环境共享同一 group_id）
    code             text        NOT NULL,                 -- 文件夹编码（同一 env_id 内唯一；顶级跨环境去重由 group_id 关联）
    name             text        NOT NULL,                 -- 文件夹名称
    env_id           uuid        NOT NULL,                 -- 所属环境 ID（代码层面关联，无外键）
    parent_folder_id uuid,                                 -- 父文件夹 ID（NULL 为顶层，非 NULL 为二级）
    remark           text        NOT NULL DEFAULT '',      -- 备注
    type             text        NOT NULL,                 -- 目录类型：common-通用目录 / customer-用户目录
    is_deleted       boolean     NOT NULL DEFAULT false,
    delete_at        timestamptz,
    delete_by        text        NOT NULL DEFAULT '',
    create_by        text        NOT NULL DEFAULT '',
    update_by        text        NOT NULL DEFAULT '',
    create_at        timestamptz NOT NULL DEFAULT now(),
    update_at        timestamptz NOT NULL DEFAULT now()
);

-- 按业务组查询/批量更新/批量删除（屏蔽环境层级）
CREATE INDEX IF NOT EXISTS idx_folder_info_group ON folder_info (group_id);
-- 按环境查询文件夹列表
CREATE INDEX IF NOT EXISTS idx_folder_info_env ON folder_info (env_id);
-- 环境内按编码查询文件夹（唯一性在代码层面保证）
CREATE INDEX IF NOT EXISTS idx_folder_info_env_code ON folder_info (env_id, code);
-- 按父文件夹查询子文件夹
CREATE INDEX IF NOT EXISTS idx_folder_info_parent ON folder_info (parent_folder_id);
-- 父文件夹下按编码定位子文件夹（groups 下唯一校验）
CREATE INDEX IF NOT EXISTS idx_folder_info_parent_code ON folder_info (parent_folder_id, code);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_folder_info_group` | `group_id` | 业务组维度：聚合查询 / 批量更新 / 批量删除（屏蔽环境层级） |
| `idx_folder_info_env` | `env_id` | 查询环境下全部文件夹 |
| `idx_folder_info_env_code` | `env_id, code` | 环境内按编码定位文件夹 |
| `idx_folder_info_parent` | `parent_folder_id` | 按父文件夹查询二级目录 |
| `idx_folder_info_parent_code` | `parent_folder_id, code` | 父文件夹下按编码定位子文件夹 |

**层级与类型业务规则**（代码层校验，不落数据库约束）：

| 规则 | 说明 |
|------|------|
| 最多 2 层 | 顶层（`parent_folder_id` 为 NULL）→ 二级（`parent_folder_id` 指向顶层 id） |
| `customer` 仅一级 | customer 目录只能建在顶层，其下不允许二级 |
| `common` 顶级目录 | 仅支持 `global` 与 `groups` 两个编码 |
| `global` 仅一层 | global 下不允许建二级目录 |
| `groups` 两层 | groups 下可建二级目录（如 groups/ob_efficient_cfg） |
| `group_id` 一致性 | 同一业务 folder 的所有环境实例必须共享同一 `group_id`（创建时一次性生成） |
| `(env_id, code)` 唯一 | 同一环境内同一 `code` 只能有一条 folder 记录（顶级 / 二级均按所在环境唯一） |
| `(parent_folder_id, code)` 唯一 | 同一父 folder 下同一 `code` 只能有一条子 folder 记录（二级目录唯一性） |

**project_id 关联说明**：folder_info 仅冗余直接父级 `env_id`（遵循项目层级惯例，如 project_info 只存 org_id）。按项目维度查询/删除时，通过 `env_id` 关联 environment_info 过滤 `project_id` 实现。

**group_id 与 parent_folder_id 关系**：
- `parent_folder_id` 仍是某个具体环境下父 folder 的 `id`（用于定位层级）
- 子 folder 与父 folder 分属不同业务实体，因此**子 folder 的 `group_id` 与父 folder 的 `group_id` 不同**
- 二级目录创建时，先通过 `parent_folder_id` 找到该父 folder 记录，定位项目后再展开全环境，每个环境下的子 folder 共享子 folder 的同一个 `group_id`
