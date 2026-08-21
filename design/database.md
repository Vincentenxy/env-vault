# 数据库表设计

> 本文档记录 env-vault 项目所有数据库表结构（PostgreSQL），实现变更时必须同步更新本文档。
> 所有表结构使用 SQL 代码块描述。
>
> **命名约定**：信息类表统一使用 `_info` 后缀（如 `tenant_info`、`organization_info`）。
>
> **实体层级**：租户（tenant_info）→ 组织（organization_info）→ 项目（project_info）→ 环境（environment_info）→ 文件夹（folder_info）→ 密钥（secret_info）。
> 租户类比公司，组织类比公司部门，项目类比部门承接的具体项目，环境类比项目下的部署环境（开发/测试/仿真/生产），文件夹类比环境下的目录结构，密钥类比文件夹下的 key=value 键值对。系统（system_info）暂未引入。
>
> **通用字段约定**：所有可变业务表必须包含以下字段；只追加、不更新、不删除的历史表仅保留 `create_at` / `create_by`
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

## 已有表字段变更：`manager`

`manager` 保存资源管理员的外部用户 ID，与当前 JWT 的 `staffuserid` 使用同一标识。历史数据可使用原记录的 `create_by` 回填；后续新建资源由应用层处理：请求未传 `manager` 时使用当前登录人的用户 ID。

```sql
ALTER TABLE tenant_info
    ADD COLUMN IF NOT EXISTS manager text NOT NULL DEFAULT '';

ALTER TABLE organization_info
    ADD COLUMN IF NOT EXISTS manager text NOT NULL DEFAULT '';

ALTER TABLE project_info
    ADD COLUMN IF NOT EXISTS manager text NOT NULL DEFAULT '';

ALTER TABLE folder_info
    ADD COLUMN IF NOT EXISTS manager text NOT NULL DEFAULT '';

-- 可选：为已有数据回填管理员，不覆盖已经存在的 manager。
UPDATE tenant_info SET manager = create_by WHERE manager = '' AND create_by <> '';
UPDATE organization_info SET manager = create_by WHERE manager = '' AND create_by <> '';
UPDATE project_info SET manager = create_by WHERE manager = '' AND create_by <> '';
UPDATE folder_info SET manager = create_by WHERE manager = '' AND create_by <> '';

---

## 表名：`tenant_info`

**说明**：租户表。租户是系统内最顶级实体（类比公司），组织、系统等内容均归属租户之下。

```sql
CREATE TABLE IF NOT EXISTS tenant_info (
    id          uuid        PRIMARY KEY,
    code        text        NOT NULL,                 -- 租户编码
    name        text        NOT NULL,                 -- 租户名称
    remark      text        NOT NULL DEFAULT '',      -- 备注
    manager     text        NOT NULL DEFAULT '',      -- 管理员用户 ID
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
    manager     text        NOT NULL DEFAULT '',      -- 管理员用户 ID
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

## 表名：`user_info`

**说明**：用户信息表。`id` 为系统内部生成的 UUID，`user_id` 为外部系统传入的用户标识，对应当前 JWT 中的 `staffuserid`。用户归属租户和组织，关联关系由代码层维护，不使用外键。密码字段仅允许保存不可逆哈希，当前不设置密码时保存空字符串。

```sql
CREATE TABLE IF NOT EXISTS user_info (
    id            uuid        PRIMARY KEY,               -- 系统内部生成
    user_id       text        NOT NULL,                  -- 外部用户 ID，对应 JWT staffuserid
    nickname      text        NOT NULL DEFAULT '',       -- 用户姓名/昵称
    username      text        NOT NULL DEFAULT '',       -- 登录名
    password_hash text        NOT NULL DEFAULT '',       -- 密码哈希，当前不设置
    email         text        NOT NULL DEFAULT '',       -- 邮箱，当前不设置
    phone         text        NOT NULL DEFAULT '',       -- 手机号，当前不设置
    tenant_id     uuid        NOT NULL,                  -- 所属租户 ID（代码层面关联，无外键）
    org_id        uuid        NOT NULL,                  -- 所属组织 ID（代码层面关联，无外键）
    is_deleted    boolean     NOT NULL DEFAULT false,
    delete_at     timestamptz,
    delete_by     text        NOT NULL DEFAULT '',
    create_by     text        NOT NULL DEFAULT '',
    update_by     text        NOT NULL DEFAULT '',
    create_at     timestamptz NOT NULL DEFAULT now(),
    update_at     timestamptz NOT NULL DEFAULT now()
);

-- 根据外部用户 ID 定位用户（唯一性在代码层面保证）
CREATE INDEX IF NOT EXISTS idx_user_info_user_id ON user_info (user_id);
-- 按租户查询用户
CREATE INDEX IF NOT EXISTS idx_user_info_tenant ON user_info (tenant_id);
-- 按组织查询用户
CREATE INDEX IF NOT EXISTS idx_user_info_org ON user_info (org_id);
-- 租户内按登录名定位用户（唯一性在代码层面保证）
CREATE INDEX IF NOT EXISTS idx_user_info_tenant_username ON user_info (tenant_id, username);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_user_info_user_id` | `user_id` | 根据外部用户 ID 定位用户 |
| `idx_user_info_tenant` | `tenant_id` | 查询租户下全部用户 |
| `idx_user_info_org` | `org_id` | 查询组织下全部用户 |
| `idx_user_info_tenant_username` | `tenant_id, username` | 租户内按登录名定位用户 |

**业务规则**（代码层校验）：

- `user_id` 在未删除用户中全局唯一。
- `username` 在同一租户的未删除用户中唯一。
- 用户更新接口只更新已存在的用户，不承担创建职责；用户创建由后续登录相关接口实现。
- `password_hash` 仅允许保存不可逆密码哈希，禁止保存明文密码。

**缓存规则**：

- PostgreSQL 是用户信息的权威数据源。
- 系统启动后异步查询全部未删除用户，将用户资料刷新到 Redis，并将 `user_id -> nickname` 刷新到进程内存。
- 查询用户姓名时依次查询进程内存、Redis、PostgreSQL；Redis 或 PostgreSQL 命中后回填前级缓存，PostgreSQL 仍未查询到时返回用户不存在错误。
- 用户资料更新成功后，同步刷新当前实例的内存姓名缓存和 Redis 用户资料缓存；缓存刷新失败不回滚已提交的数据库更新。
- Redis 用户资料不保存 `password_hash`，避免扩大密码凭证的存储范围。

**待办**：Kubernetes 多实例部署时，引入 Redis Pub/Sub 或等价的缓存变更通知机制，使一个实例更新用户姓名后，其余实例同步刷新进程内存。本期暂不实现跨实例内存同步。

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
    manager     text        NOT NULL DEFAULT '',      -- 管理员用户 ID
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
    manager          text        NOT NULL DEFAULT '',      -- 管理员用户 ID
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

---

## 表名：`secret_info`

**说明**：密钥表。密钥归属文件夹之下（`folder_id` 关联 folder_info 的 id，代码层面维护，无外键），表示一个 key=value 键值对。同一密钥在每个环境各有一条记录（value 各不相同），共享同一 `group_id`（与 folder_info 的业务组模式一致）。

**保留字评估**：`key` 在 PostgreSQL 中属于 non-reserved 关键字，**可安全用作列名**；`value` 同样为 non-reserved，且本表使用 `value_ciphertext` 后缀形式更无风险；`version` 非关键字；`secret` 非关键字。

**加密存储约定**：`value_ciphertext` 存储加密结果 JSON 字符串（AES-256-GCM），格式参考：

```json
{"data": "<base64 密文>", "nonce": "<base64 nonce>", "algorithm": "AES-256-GCM"}
```

加密私钥通过配置文件注入（`configs/config.yaml`），由代码层读取后完成加解密，密文不可逆时接口返回错误。

```sql
CREATE TABLE IF NOT EXISTS secret_info (
    id               uuid        PRIMARY KEY,
    group_id         uuid        NOT NULL,                 -- 业务组 ID：同一 secret 的所有环境实例共享（跨环境定位）
    folder_id        uuid        NOT NULL,                 -- 所属文件夹 ID（当前环境下 folder_info 的 id，代码层面关联，无外键）
    env_code         text        NOT NULL,                 -- 冗余：与 folder_id 所属环境的 code 保持一致（创建时写入，env.code 不可更新，安全冗余）
    key              text        NOT NULL,                 -- 键名（同一业务 folder 内唯一，代码层校验）
    value_ciphertext text        NOT NULL DEFAULT '',      -- 加密后的值（JSON：data / nonce / algorithm）
    value_type       text        NOT NULL DEFAULT '',      -- 值类型：number/string（预留，暂不启用）
    remark           text        NOT NULL DEFAULT '',      -- 备注
    version          integer     NOT NULL DEFAULT 1,       -- 版本号，数字递增
    is_deleted       boolean     NOT NULL DEFAULT false,
    delete_at        timestamptz,
    delete_by        text        NOT NULL DEFAULT '',
    create_by        text        NOT NULL DEFAULT '',
    update_by        text        NOT NULL DEFAULT '',
    create_at        timestamptz NOT NULL DEFAULT now(),
    update_at        timestamptz NOT NULL DEFAULT now()
);

-- 按业务组查询全部环境实例（聚合查询 / 批量删除）
CREATE INDEX IF NOT EXISTS idx_secret_info_group ON secret_info (group_id);
-- 按文件夹查询 secrets 列表
CREATE INDEX IF NOT EXISTS idx_secret_info_folder ON secret_info (folder_id);
-- 文件夹内按 key 定位（唯一性校验）
CREATE INDEX IF NOT EXISTS idx_secret_info_folder_key ON secret_info (folder_id, key);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_secret_info_group` | `group_id` | 业务组维度：按 secret 聚合查询各环境值 / 批量删除 |
| `idx_secret_info_folder` | `folder_id` | 按文件夹查询该 folder 下全部 secrets |
| `idx_secret_info_folder_key` | `folder_id, key` | 文件夹内按 key 定位（唯一性校验） |

**业务规则**（代码层校验，不落数据库约束）：

| 规则 | 说明 |
|------|------|
| `group_id` 一致性 | 同一 secret 的所有环境实例共享同一 `group_id`（创建时服务端一次性生成，全环境共享） |
| key 唯一范围 | 同一业务 folder（逻辑）下 key 唯一；物理上同一 `folder_id` 内唯一。校验时通过 folder 的 `group_id` 展开全部环境实例后比对 |
| 创建展开 | 入参 `folderGroupId` + 各环境的 value 列表（含 envId），后端按 `folderGroupId + envId` 定位该环境下 folder 的 id 落库 |
| `env_code` 冗余 | 与 `folder_id` 所属环境的 code 一致（创建时写入）。因 folder.env_id 创建后不变、所有表的 code 均不可更新，该冗余永久有效，查询聚合时无需再跳 folder/env 表 |
| `value_type` 预留 | 当前默认空串，后续启用 number/string 类型语义 |
| `version` 递增 | 每个环境实例独立维护版本号，默认 1；仅该环境的 value 实际变化时递增 |
| 软删除 | 按 `group_id` 逻辑删除该 secret 的所有环境实例 |

**folder_id 关联说明**：secret_info 的 `folder_id` 指向**某个具体环境下** folder_info 的 `id`（该环境实例挂在该环境的 folder 记录下）。跨环境聚合时通过 `group_id` 屏蔽环境层级（与 folder_info 同模式）。

---

## 表名：`secret_info_history`

**说明**：密钥值历史表。每行保存 `secret_info` 中某个具体环境实例的一个 value 版本快照。该表只追加、不更新、不删除，因此不包含 `update_at` / `update_by` / `delete_at` / `delete_by` / `is_deleted`。

```sql
CREATE TABLE IF NOT EXISTS secret_info_history (
    id               uuid        PRIMARY KEY,
    secret_id        uuid        NOT NULL,                 -- secret_info.id，具体环境实例
    batch_id         uuid        NOT NULL,                 -- 一次 create/update 请求产生的统一批次 ID
    group_id         uuid        NOT NULL,                 -- 逻辑 Secret ID，同一 Secret 的环境实例共享
    folder_id        uuid        NOT NULL,                 -- 当前环境对应的 folder_info.id
    env_code         text        NOT NULL,                 -- 环境编码
    value_ciphertext text        NOT NULL DEFAULT '',      -- 该版本的加密值
    value_type       text        NOT NULL DEFAULT '',      -- 该版本的值类型
    version          integer     NOT NULL,                 -- 该环境实例的版本号
    commit_msg       text        NOT NULL DEFAULT '',      -- 本次版本变更说明
    create_by        text        NOT NULL DEFAULT '',      -- 版本提交人
    create_at        timestamptz NOT NULL DEFAULT now()    -- 版本提交时间
);

-- 按具体环境实例分页查询历史版本
CREATE INDEX IF NOT EXISTS idx_secret_info_history_secret_version
    ON secret_info_history (secret_id, version DESC);
-- 按批次查询一次 create/update 产生的全部版本记录
CREATE INDEX IF NOT EXISTS idx_secret_info_history_batch
    ON secret_info_history (batch_id, create_at ASC);
-- 预留按逻辑 Secret 查询全部环境历史
CREATE INDEX IF NOT EXISTS idx_secret_info_history_group_created
    ON secret_info_history (group_id, create_at DESC);
```

**业务规则**：

| 规则 | 说明 |
|------|------|
| 初始版本 | 创建 Secret 时写入 `version=1` 的历史快照；一次 create 请求共用一个 `batch_id` |
| 更新批次 | 一次 update 请求生成一个 `batch_id`，请求内所有实际发生 value 变化的历史记录共用该 ID |
| 独立版本 | 不同环境通过各自的 `secret_id` 独立维护版本；只有新旧明文不同时才更新并递增版本 |
| 原子性 | 当前值更新与历史快照写入必须在同一事务中完成，任一失败则整体回滚 |
| commit_msg | update 顶层和单个 Secret 均可传 `commitMsg`；单个 Secret 非空时优先，否则使用顶层值 |
| 加密存储 | 历史 value 与当前 value 使用相同 AES-256-GCM 密文格式，不存储明文 |
| 查询优先级 | 历史接口按 `secretId > batchId > groupId` 选择条件；当前仅实现 secretId 分页和 batchId 不分页查询，groupId 返回暂不支持错误 |

**历史数据说明**：上线前已产生但未记录的历史 value 无法恢复。迁移时最多只能将 `secret_info` 当前值按当前 `version` 补为一条基线快照。
