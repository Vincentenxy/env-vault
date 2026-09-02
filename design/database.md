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

## PostgreSQL 数据库、用户与授权初始化

PostgreSQL 的数据库权限、Schema 权限和表权限相互独立。拥有数据库不代表自动拥有其他账号创建的表；表及序列的权限取决于对象所有者和显式授权。

本项目支持下面两种初始化模式：

| 模式 | DDL 执行账号 | 应用账号能力 | 适用场景 |
|------|--------------|--------------|----------|
| 模式一：超管建表、应用账号只读写数据（推荐） | `postgres` 或独立迁移账号 | 只允许连接数据库、使用 Schema 和读写表数据 | 生产环境、人工或发布流水线统一执行 DDL |
| 模式二：应用账号拥有数据库并自行建表 | `env_vault` | 可以创建 Schema、表、索引并读写数据 | 本地开发、CNPG 自动初始化或无需区分 DDL/DML 的环境 |

当前 CNPG 部署清单通过 `bootstrap.initdb.database=env_vault` 和 `bootstrap.initdb.owner=env_vault` 自动初始化，属于模式二。手工安装生产数据库时，推荐使用模式一。

### 模式一：超管建表，应用账号只读写数据（推荐）

#### 1. 创建应用登录用户和数据库

先使用 `postgres` 等管理员账号连接 `postgres` 数据库执行。PostgreSQL 不支持 `CREATE DATABASE IF NOT EXISTS`，以下语句只在首次初始化时执行。

```sql
-- 创建最小权限的 EnvVault 应用登录用户，部署前替换示例密码
CREATE ROLE env_vault
    WITH LOGIN
    PASSWORD 'REPLACE_WITH_REAL_PASSWORD'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION;

-- 数据库由 DDL 管理账号持有，CREATE DATABASE 不能放在事务块中执行
CREATE DATABASE env_vault
    WITH
    OWNER = postgres
    ENCODING = 'UTF8'
    TEMPLATE = template0;

-- 应用账号只需要连接数据库
REVOKE ALL ON DATABASE env_vault FROM PUBLIC;
GRANT CONNECT ON DATABASE env_vault TO env_vault;
```

如果用户或数据库已经存在，不要重复执行 `CREATE`，只需按实际情况修复登录密码。数据库已经由 `env_vault` 持有也不影响后续表授权；如需严格收回应用账号的 DDL 能力，可将数据库所有者调整为 DDL 管理账号。

```sql
ALTER ROLE env_vault
    WITH LOGIN
    PASSWORD 'REPLACE_WITH_REAL_PASSWORD';

ALTER DATABASE env_vault
    OWNER TO postgres;

REVOKE CREATE, TEMPORARY
    ON DATABASE env_vault
    FROM env_vault;

-- 切换到 env_vault 数据库，避免修改到 postgres 数据库的 public Schema
\connect env_vault

ALTER SCHEMA public OWNER TO postgres;
REVOKE CREATE ON SCHEMA public FROM env_vault;
GRANT USAGE ON SCHEMA public TO env_vault;
```

#### 2. 设置 public Schema 权限

下面的语句必须连接到 `env_vault` 数据库后执行。使用 `psql` 时可通过 `\connect` 切换；使用数据库客户端时需要打开连接到 `env_vault` 的查询窗口。

```sql
\connect env_vault

-- 防止其他普通用户在 public schema 中创建对象
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- 应用账号只允许访问 Schema，不允许创建表和索引
REVOKE CREATE ON SCHEMA public FROM env_vault;
GRANT USAGE ON SCHEMA public TO env_vault;
```

#### 3. 给已有表和序列授权

数据库和表创建完成后，由超管在 `env_vault` 数据库中执行。该授权只覆盖执行时已经存在的对象。

```sql
GRANT SELECT, INSERT, UPDATE, DELETE
    ON ALL TABLES IN SCHEMA public
    TO env_vault;

-- 当前表使用 UUID，不依赖序列；保留该授权以兼容后续 identity / serial 字段
GRANT USAGE, SELECT, UPDATE
    ON ALL SEQUENCES IN SCHEMA public
    TO env_vault;
```

以上表权限不包含 `TRUNCATE`、`REFERENCES` 和 `TRIGGER`，满足 EnvVault 当前运行时的查询和增删改需求。数据库结构变更应通过数据库变更流程执行，不依赖运行中的服务账号临时修改表结构。

#### 4. 给后续新增表和序列设置默认授权

PostgreSQL 的默认权限按“对象创建人”分别保存。下面示例假设后续 DDL 由 `postgres` 创建；如果实际由 `migration_user` 等其他管理角色建表，必须将 `FOR ROLE postgres` 替换为真实建表角色。

```sql
-- 必须在 env_vault 数据库中，由 postgres 本人或超级用户执行
ALTER DEFAULT PRIVILEGES
    FOR ROLE postgres
    IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO env_vault;

ALTER DEFAULT PRIVILEGES
    FOR ROLE postgres
    IN SCHEMA public
    GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO env_vault;
```

`ALTER DEFAULT PRIVILEGES IN SCHEMA public ...` 不写 `FOR ROLE` 时，只对当前执行账号以后创建的对象生效。它不会给已有表补授权，也不会影响其他账号创建的表。因此模式一必须同时执行“已有对象授权”和“指定 DDL 账号的默认授权”。

### 模式二：应用账号拥有数据库并自行建表

该模式下数据库 owner 和 DDL 执行账号都是 `env_vault`。`env_vault` 创建的表和序列天然归自己所有，不需要再向自己执行 `GRANT ON ALL TABLES` 或 `ALTER DEFAULT PRIVILEGES`。

```sql
-- 使用 postgres 等管理员账号连接 postgres 数据库执行
CREATE ROLE env_vault
    WITH LOGIN
    PASSWORD 'REPLACE_WITH_REAL_PASSWORD'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION;

CREATE DATABASE env_vault
    WITH
    OWNER = env_vault
    ENCODING = 'UTF8'
    TEMPLATE = template0;

-- 切换到 env_vault 数据库后执行
\connect env_vault

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO env_vault;
```

模式二的关键要求是：后续建表 SQL 必须使用 `env_vault` 登录执行。如果改用 `postgres` 建表，就已经切换为模式一，必须重新给已有对象授权，并为实际 DDL 账号配置默认权限。

### 权限检查

```sql
SELECT current_database(), current_user;

SELECT has_database_privilege('env_vault', 'env_vault', 'CONNECT') AS can_connect,
       has_schema_privilege('env_vault', 'public', 'USAGE') AS can_use_schema,
       has_schema_privilege('env_vault', 'public', 'CREATE') AS can_create_object;

SELECT table_schema, table_name, privilege_type
FROM information_schema.role_table_grants
WHERE grantee = 'env_vault'
  AND table_schema = 'public'
ORDER BY table_name, privilege_type;

-- 检查 public 下的表由哪个账号创建
SELECT schemaname, tablename, tableowner
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY tablename;
```


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
    tenant_id     uuid                ,                  -- 所属租户 ID（代码层面关联，无外键）
    org_id        uuid                ,                  -- 所属组织 ID（代码层面关联，无外键）
    is_blocked    boolean     NOT NULL DEFAULT false,   -- 是否锁定，锁定用户禁止访问认证接口
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
-- 本地登录名在全部有效用户中忽略大小写后唯一；空登录名不启用本地认证
CREATE UNIQUE INDEX IF NOT EXISTS uk_user_info_username_active
    ON user_info (lower(username))
    WHERE is_deleted = false AND username <> '';
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_user_info_user_id` | `user_id` | 根据外部用户 ID 定位用户 |
| `idx_user_info_tenant` | `tenant_id` | 查询租户下全部用户 |
| `idx_user_info_org` | `org_id` | 查询组织下全部用户 |
| `uk_user_info_username_active` | `lower(username)` | 本地登录按用户名定位用户，并保证有效非空用户名全局唯一 |

**业务规则**（代码层校验）：

- `user_id` 在未删除用户中全局唯一。
- `username` 在全部未删除且启用本地认证的用户中忽略大小写后全局唯一，登录时不要求用户选择租户。
- 用户更新接口只更新已存在的用户，不承担创建职责；用户创建由后续登录相关接口实现。
- `password_hash` 仅允许保存带独立随机 Salt 和参数的 Argon2id PHC 格式不可逆密码哈希，禁止保存明文、可逆密文或使用 Secret 主密钥加密密码。
- `password_hash = ''` 表示该用户未启用本地密码登录，公司统一认证不依赖该字段。
- `is_blocked = true` 的用户通过 JWT 验签后由认证中间件返回 HTTP 403，不进入业务处理器。

**缓存规则**：

- PostgreSQL 是用户信息的权威数据源。
- 系统启动后异步查询全部未删除用户，将用户资料刷新到 Redis，并将 `user_id -> nickname` 刷新到进程内存。
- 用户锁定状态单独写入 Redis Hash，认证时优先查询 Redis，未命中或 Redis 异常时回源 PostgreSQL。
- 查询用户姓名时依次查询进程内存、Redis、PostgreSQL；Redis 或 PostgreSQL 命中后回填前级缓存，PostgreSQL 仍未查询到时返回用户不存在错误。
- 用户资料更新成功后，同步刷新当前实例的内存姓名缓存和 Redis 用户资料缓存；缓存刷新失败不回滚已提交的数据库更新。
- Redis 用户资料不保存 `password_hash`，避免扩大密码凭证的存储范围。

**待办**：Kubernetes 多实例部署时，引入 Redis Pub/Sub 或等价的缓存变更通知机制，使一个实例更新用户姓名后，其余实例同步刷新进程内存。本期暂不实现跨实例内存同步。

---

## 表名：`user_access_token_info`

**说明**：用户个人访问令牌（PAT）表。一个用户可以创建多个长期访问令牌，令牌使用本地登录相同的 RSA 私钥签发，并通过独立的 JWT 声明标识为 PAT。`owner_id` 关联 `user_info.id`（系统内部 UUID），关联关系由代码层维护，不使用外键。完整令牌使用当前系统主密钥和 AES-256-GCM 加密后存储，数据库、审计日志和应用日志均不得保存令牌明文。

```sql
CREATE TABLE IF NOT EXISTS user_access_token_info (
    id               uuid        PRIMARY KEY,
    owner_id         uuid        NOT NULL,                 -- user_info.id，令牌所有者的内部 UUID
    name             text        NOT NULL,                 -- Token 名称，去除首尾空格后长度不超过 64
    jti              uuid        NOT NULL,                 -- JWT 唯一标识，用于认证时校验撤销状态
    token_ciphertext text        NOT NULL DEFAULT '',      -- 使用系统主密钥加密后的完整 JWT
    expires_at       timestamptz NOT NULL,                 -- 到期时间，不支持延期
    last_used_at     timestamptz,                          -- 最近一次认证成功时间
    is_deleted       boolean     NOT NULL DEFAULT false,
    delete_at        timestamptz,
    delete_by        text        NOT NULL DEFAULT '',
    create_by        text        NOT NULL DEFAULT '',
    update_by        text        NOT NULL DEFAULT '',
    create_at        timestamptz NOT NULL DEFAULT now(),
    update_at        timestamptz NOT NULL DEFAULT now()
);

-- JWT jti 全局唯一
CREATE UNIQUE INDEX IF NOT EXISTS uk_user_access_token_info_jti
    ON user_access_token_info (jti);

-- 查询用户的全部未删除 Token
CREATE INDEX IF NOT EXISTS idx_user_access_token_info_owner_created
    ON user_access_token_info (owner_id, create_at DESC)
    WHERE is_deleted = false;

-- 认证和过期清理使用
CREATE INDEX IF NOT EXISTS idx_user_access_token_info_expires
    ON user_access_token_info (expires_at)
    WHERE is_deleted = false;
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `uk_user_access_token_info_jti` | `jti` | 认证时唯一定位令牌并防止重复标识 |
| `idx_user_access_token_info_owner_created` | `owner_id, create_at DESC` | 查询用户的全部未删除 Token |
| `idx_user_access_token_info_expires` | `expires_at` | 认证校验和后续过期清理 |

**业务规则**（代码层校验）：

- Token 所有者只允许从已认证 JWT 的 `staffuserid` 获取，请求不得指定其他用户。
- Token 名称去除首尾空格后不能为空，长度不得超过 64 个字符；允许同一用户使用相同名称。
- 到期时间由前端时间选择器提供，必须晚于当前时间；创建后不提供延期或修改接口。
- 每个用户最多同时拥有 10 个未删除且未到期的 Token。创建事务先锁定对应的 `user_info` 记录，再统计有效 Token，避免并发请求突破上限。
- PAT 使用与本地登录令牌相同的 RSA 密钥、`iss` 和 `aud`，并增加 `authSource=personalToken`、`tokenUse=personalAccessToken` 和 JWT Header `typ=env-vault-pat+jwt`。
- PAT 不能调用 PAT 创建接口；PAT 的其他接口访问能力当前与所属用户一致，细粒度权限后续实现。
- 每次 PAT 认证除校验 JWT 签名、`iss`、`aud` 和 `exp` 外，还必须按 `jti` 校验数据库记录未删除、未过期且属于当前用户。
- 用户 `is_blocked=true` 时，其全部 PAT 立即失效；删除 PAT 使用软删除，删除后立即拒绝认证。
- 列表接口允许所属用户查看完整令牌，后端解密后返回明文，并设置禁止缓存的响应头；前端默认遮罩，点击眼睛显示，点击 Token 内容复制。
- 创建、列表查看、删除和 PAT 认证失败均写入审计日志；日志仅记录 Token 记录 ID、`jti`、名称和操作结果，禁止记录明文、密文、Authorization Header 或完整请求响应体。

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

## 表名：`project_user_relation`

**说明**：项目与用户的多对多绑定关系表，同时承载项目协作访问的有效期。`project_id` 关联 `project_info.id`；`user_id` 关联 `user_info.id`（系统内部 UUID），不是 `user_info.user_id` 中保存的外部用户标识。关联关系由代码层维护，不使用外键。

```sql
CREATE TABLE IF NOT EXISTS project_user_relation (
    id         uuid        PRIMARY KEY,              -- 应用层生成
    project_id uuid        NOT NULL,                 -- project_info.id
    user_id    uuid        NOT NULL,                 -- user_info.id（系统内部 UUID）
    expire_at  timestamptz,                          -- 访问到期时间，NULL 表示长期有效
    is_deleted boolean     NOT NULL DEFAULT false,
    delete_at  timestamptz,
    delete_by  text        NOT NULL DEFAULT '',
    create_by  text        NOT NULL DEFAULT '',
    update_by  text        NOT NULL DEFAULT '',
    create_at  timestamptz NOT NULL DEFAULT now(),
    update_at  timestamptz NOT NULL DEFAULT now()
);

-- 同一个未撤销关系只能保留一条；撤销后允许重新邀请并生成新记录
CREATE UNIQUE INDEX IF NOT EXISTS uk_project_user_relation_project_user
    ON project_user_relation (project_id, user_id)
    WHERE is_deleted = false;

-- 支持按用户查询当前有效的普通项目和协作项目
CREATE INDEX IF NOT EXISTS idx_project_user_relation_user_active
    ON project_user_relation (user_id, project_id, expire_at)
    WHERE is_deleted = false;

-- 支持到期关系筛选以及后续到期通知/清理任务
CREATE INDEX IF NOT EXISTS idx_project_user_relation_expire
    ON project_user_relation (expire_at)
    WHERE is_deleted = false AND expire_at IS NOT NULL;
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `project_user_relation_pkey` | `id` | 主键索引，根据关系 ID 定位记录，由 PostgreSQL 自动创建 |
| `uk_project_user_relation_project_user` | `project_id, user_id` | 防止同一个未撤销关系重复绑定；软删除后允许重新邀请 |
| `idx_project_user_relation_user_active` | `user_id, project_id, expire_at` | 按用户查询未撤销关系，并结合 `expire_at` 判断是否仍有效 |
| `idx_project_user_relation_expire` | `expire_at` | 支持到期筛选以及后续到期通知/清理任务 |

**业务规则**（代码层校验）：

- 新增绑定时由应用层生成 `id`。
- 绑定前必须确认项目和用户存在且未删除。
- `expire_at IS NULL` 表示长期有效；`expire_at > now()` 表示限时访问仍有效。
- 查询项目、项目成员和访问关系时必须同时满足 `is_deleted = false`，以及 `expire_at IS NULL OR expire_at > now()`。
- 到期只代表关系失效，不自动修改 `is_deleted`；只有用户主动关闭、撤销分享或上级资源删除时才执行软删除。
- 同一个未撤销的 `project_id + user_id` 只能存在一条记录；对已到期但未撤销的关系续期时更新原记录的 `expire_at`。
- 项目成员关系不修改 `user_info.tenant_id` 或 `user_info.org_id`。用户所属租户/组织表示其主归属，跨组织项目通过本表单独授权。
- “协作项目”由项目所属组织与用户主归属组织不同推导，不额外保存协作标记，也不向前端暴露协作项目的所属组织信息。
- 外部协作者不能担任项目管理员；项目管理员对应用户的主组织必须与 `project_info.org_id` 一致。该规则由项目 service 在创建和修改时校验。
- 删除项目或用户时，由应用层软删除相关绑定记录。


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
    key_pattern      text        NOT NULL DEFAULT '',      -- Secret key 完整匹配表达式，空字符串表示关闭格式校验
    is_deleted       boolean     NOT NULL DEFAULT false,
    delete_at        timestamptz,
    delete_by        text        NOT NULL DEFAULT '',
    create_by        text        NOT NULL DEFAULT '',
    update_by        text        NOT NULL DEFAULT '',
    create_at        timestamptz NOT NULL DEFAULT now(),
    update_at        timestamptz NOT NULL DEFAULT now()
);

-- 已存在的 folder_info 表使用以下语句补充字段
ALTER TABLE folder_info
    ADD COLUMN IF NOT EXISTS key_pattern text NOT NULL DEFAULT '';

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
| 创建展开 | 入参 `folderGroupId` + 各环境的 value 列表（含 envId）；每个 secret 至少填写一个非空环境值，已提交的空环境仍创建实例以便后续通过 `secretId` 补值，后端按 `folderGroupId + envId` 定位对应 folder 的 id |
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

---

## 表名：`personal_secret_info`

**说明**：用户个人密钥当前值表。每条记录归属于一个 `user_info` 用户，用于保存该用户个人持有、离职后需要由系统接管的工作凭据。该表不进入租户、组织、项目、环境、文件夹层级，也不与共享 `secret_info` 混用。

`owner_id` 关联 `user_info.id`（系统内部 UUID），关联关系由代码层维护，不使用外键。密码值使用当前系统主密钥和 AES-256-GCM 加密，密文格式与 `secret_info.value_ciphertext` 完全一致，不引入第二套加密密钥。

```sql
CREATE TABLE IF NOT EXISTS personal_secret_info (
    id               uuid        PRIMARY KEY,
    owner_id         uuid        NOT NULL,                 -- user_info.id，个人密钥所有者的内部 UUID
    name             text        NOT NULL,                 -- 凭据名称，如 GitLab、服务器账号
    credential_type  text        NOT NULL DEFAULT 'password', -- 凭据类型，首期只使用 password
    account          text        NOT NULL DEFAULT '',      -- 登录账号，作为列表元数据保存
    login_url        text        NOT NULL DEFAULT '',      -- 登录地址，作为列表元数据保存
    value_ciphertext text        NOT NULL DEFAULT '',      -- AES-256-GCM 加密后的密码值
    remark           text        NOT NULL DEFAULT '',      -- 备注
    version          integer     NOT NULL DEFAULT 1,       -- 当前版本号，实际内容变化时递增
    is_deleted       boolean     NOT NULL DEFAULT false,
    delete_at        timestamptz,
    delete_by        text        NOT NULL DEFAULT '',
    create_by        text        NOT NULL DEFAULT '',
    update_by        text        NOT NULL DEFAULT '',
    create_at        timestamptz NOT NULL DEFAULT now(),
    update_at        timestamptz NOT NULL DEFAULT now()
);

-- 本人列表和超管接管列表均按所有者查询，并按名称稳定排序
CREATE INDEX IF NOT EXISTS idx_personal_secret_info_owner_name
    ON personal_secret_info (owner_id, name, id)
    WHERE is_deleted = false;
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_personal_secret_info_owner_name` | `owner_id, name, id` | 查询某个用户的全部有效个人密钥，并按名称和 ID 稳定排序 |

**业务规则**（代码层校验，不落数据库约束）：

| 规则 | 说明 |
|------|------|
| 所有权 | 创建时根据 JWT `staffuserid` 查询 `user_info.id` 并写入 `owner_id`；请求不得自行声明或修改所有者 |
| 本人访问 | 普通接口始终追加 `owner_id = 当前用户内部 UUID`，其他普通用户和资源管理员不能查询该记录 |
| 超管接管 | 只有具备系统超级管理员权限、目标用户 `is_blocked=true` 且完成三份主密钥分片验证后，才能通过独立接管接口访问 |
| 本人列表显值 | 普通用户本人调用 list 时，由服务端批量解密并随每条记录返回 `value`，前端默认遮挡并在当前单元格内切换显隐；接口永远不返回 `value_ciphertext` |
| 显值接口预留 | 保留 reveal 接口供后续切换为按需查询；当前 list 和 reveal 响应均禁止缓存，明文不得进入 Redis、日志、审计事件、URL 或前端持久化存储 |
| 加密密钥 | 复用系统当前已经激活的 `masterkey.Manager`；不保存明文，不引入个人密钥专用主密钥 |
| 类型预留 | `credential_type` 首期只使用 `password`；后续新增 token / recoveryCode 等类型时由代码层扩展 |
| 版本递增 | name / credential_type / account / login_url / value / remark 任一实际变化时，当前版本递增并写入完整历史快照；无实际变化不产生新版本 |
| 软删除 | 删除只修改当前表的软删除字段，历史快照继续保留；业务接口不提供历史删除能力 |
| 离职顺序 | 用户应先锁定，完成个人密钥接管和外部密码轮换后再删除用户；存在未完成接管的个人密钥时不得提前删除用户 |

---

## 表名：`personal_secret_info_history`

**说明**：用户个人密钥的不可变历史版本表。每次创建或实际更新个人密钥时，保存当时的完整字段快照；历史密码仍使用当前系统主密钥和 AES-256-GCM 加密。该表只追加、不更新、不删除，因此不包含 `update_at` / `update_by` / `delete_at` / `delete_by` / `is_deleted`。

```sql
CREATE TABLE IF NOT EXISTS personal_secret_info_history (
    id                 uuid        PRIMARY KEY,
    personal_secret_id uuid        NOT NULL,                 -- personal_secret_info.id
    batch_id           uuid        NOT NULL,                 -- 一次 create/update 请求的批次 ID
    owner_id           uuid        NOT NULL,                 -- 版本产生时的 user_info.id
    name               text        NOT NULL,                 -- 版本产生时的凭据名称
    credential_type    text        NOT NULL DEFAULT 'password',
    account            text        NOT NULL DEFAULT '',      -- 版本产生时的登录账号
    login_url          text        NOT NULL DEFAULT '',      -- 版本产生时的登录地址
    value_ciphertext   text        NOT NULL DEFAULT '',      -- 该版本的 AES-256-GCM 密文
    remark             text        NOT NULL DEFAULT '',      -- 版本产生时的备注
    version            integer     NOT NULL,                 -- 对应当前表的版本号
    commit_msg         text        NOT NULL DEFAULT '',      -- 本次版本变更说明
    create_by          text        NOT NULL DEFAULT '',      -- 版本提交人外部用户 ID
    create_at          timestamptz NOT NULL DEFAULT now()    -- 版本提交时间
);

-- 按个人密钥分页查询历史版本
CREATE INDEX IF NOT EXISTS idx_personal_secret_info_history_secret_version
    ON personal_secret_info_history (personal_secret_id, version DESC);
-- 按一次创建或更新批次查询历史快照
CREATE INDEX IF NOT EXISTS idx_personal_secret_info_history_batch
    ON personal_secret_info_history (batch_id, create_at ASC);
-- 预留按所有者查询个人密钥历史
CREATE INDEX IF NOT EXISTS idx_personal_secret_info_history_owner_created
    ON personal_secret_info_history (owner_id, create_at DESC);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_personal_secret_info_history_secret_version` | `personal_secret_id, version DESC` | 分页查询单个个人密钥的历史版本 |
| `idx_personal_secret_info_history_batch` | `batch_id, create_at ASC` | 查询一次创建或更新产生的历史快照 |
| `idx_personal_secret_info_history_owner_created` | `owner_id, create_at DESC` | 预留按用户查询全部个人密钥历史 |

**业务规则**：

| 规则 | 说明 |
|------|------|
| 初始版本 | 创建个人密钥时，在同一事务中写入当前记录和 `version=1` 的完整历史快照 |
| 更新版本 | 更新前比较实际字段；发生变化时更新当前记录、递增版本并写入完整历史快照 |
| 原子性 | 当前值创建或更新与历史快照写入必须使用同一 PostgreSQL 事务，任一步失败则整体回滚 |
| 历史列表 | 历史列表只返回版本、变更说明、提交人和时间等元数据，不批量解密或返回历史密码 |
| 历史显值 | 历史密码通过独立 history/reveal 接口按单个版本解密；本人访问仍校验所有权，超管访问仍要求有效接管授权 |
| 提交人名称 | 接口响应中的提交人名称按规范返回 `createByName`，优先从现有用户姓名缓存解析，失败时返回空字符串 |
| 加密与审计 | 历史明文、密文和主密钥分片均不得写入审计事件；审计仅记录资源 ID、版本、操作人、目标用户和结果 |

---

## 表名：`audit_event_log`（已确认）

**说明**：全系统业务审计事件表，记录“谁在什么时间对什么资源执行了什么操作，以及操作结果”。该表是只追加历史表，业务代码只允许插入和查询，不提供更新、软删除或物理删除能力；超过一年保留期的数据仅允许由系统保留期任务清理。

普通运行日志仍由 `pkg/logger` 输出；本表是可持久化、可按资源追溯的审计事实来源，不能使用普通日志替代。

```sql
CREATE TABLE IF NOT EXISTS audit_event_log (
    id                  uuid        PRIMARY KEY,
    event_source        text        NOT NULL DEFAULT 'server', -- 事件产生方：server / client / external / system；首期只写 server / system
    source_event_id     uuid,                                -- 预留：客户端、SDK 或外部系统事件幂等 ID，首期不使用
    source_occurred_at  timestamptz,                         -- 预留：事件源声明的发生时间，仅作辅助信息，不作为可信审计时间
    entry_type          text        NOT NULL DEFAULT 'http', -- 服务入口：http / grpc / sdk / internal / job
    caller_type         text        NOT NULL DEFAULT 'unknown', -- 调用方类型：web / sdk / service / cli / system / unknown
    caller_name         text        NOT NULL DEFAULT '',     -- SDK、CLI 或调用服务名称；浏览器请求可为空
    caller_version      text        NOT NULL DEFAULT '',     -- SDK、CLI 或调用服务版本
    operation_name      text        NOT NULL DEFAULT '',     -- 技术入口：HTTP 路由模板、gRPC FullMethod、SDK 方法或任务名
    action_code         text        NOT NULL,                -- 稳定动作编码，如 secret.update / auth.login / masterKey.share.submit
    result_code         text        NOT NULL,                -- 结果：success / failure
    actor_type          text        NOT NULL DEFAULT 'user', -- 操作主体：user / anonymous / service / system
    resource_type       text        NOT NULL DEFAULT '',     -- 被操作资源类型，如 tenant / project / secret / masterKey
    resource_id         text        NOT NULL DEFAULT '',     -- 被操作资源 ID；使用 text 兼容 UUID、外部用户 ID 和系统级资源
    resource_name       text        NOT NULL DEFAULT '',     -- 资源名称快照；Secret 可记录 key，但禁止记录 value
    scope_type          text        NOT NULL DEFAULT '',     -- 权限归属资源类型，如 tenant / org / project / folder / system
    scope_id            text        NOT NULL DEFAULT '',     -- 权限归属资源 ID，供后续资源管理员权限过滤
    batch_id            uuid,                                -- 可选业务批次 ID；同一次批量 Secret 操作共享
    change_detail       jsonb       NOT NULL DEFAULT '[]'::jsonb, -- 字段变更数组；Secret value 只能标记 changed/redacted
    event_detail        jsonb       NOT NULL DEFAULT '{}'::jsonb, -- 安全扩展信息，如影响数量、环境 ID；禁止放请求/响应体
    failure_code        text        NOT NULL DEFAULT '',     -- 稳定失败编码，不写底层原始错误
    failure_reason      text        NOT NULL DEFAULT '',     -- 可展示的安全失败原因
    correlation_id      text        NOT NULL DEFAULT '',     -- 单次入口调用关联 ID，如 x-request-id、gRPC request-id 或 job run-id
    trace_id            text        NOT NULL DEFAULT '',     -- 分布式追踪 trace ID；未接入追踪系统时可为空
    protocol_status     text        NOT NULL DEFAULT '',     -- 协议结果码，如 HTTP 200、gRPC OK；内部任务可为空
    protocol_detail     jsonb       NOT NULL DEFAULT '{}'::jsonb, -- 协议白名单上下文，禁止放 header/metadata/body
    client_ip           text        NOT NULL DEFAULT '',     -- 调用端 IP 或 peer address；内部任务为空
    user_agent          text        NOT NULL DEFAULT '',     -- 浏览器、SDK 或 gRPC User-Agent；内部任务为空
    expire_at           timestamptz NOT NULL DEFAULT (now() + interval '1 year'), -- 到期清理时间
    create_by           text        NOT NULL DEFAULT '',     -- 操作人外部用户 ID；匿名事件为空
    create_by_name      text        NOT NULL DEFAULT '',     -- 操作人姓名快照
    create_at           timestamptz NOT NULL DEFAULT now()   -- 服务端可信审计时间
);

-- 后续按某一个资源查询全部操作记录的主索引
CREATE INDEX IF NOT EXISTS idx_audit_event_log_resource_created
    ON audit_event_log (resource_type, resource_id, create_at DESC);
-- 后续按权限归属资源过滤，供资源管理员查看
CREATE INDEX IF NOT EXISTS idx_audit_event_log_scope_created
    ON audit_event_log (scope_type, scope_id, create_at DESC);
-- 按操作人查询审计记录
CREATE INDEX IF NOT EXISTS idx_audit_event_log_creator_created
    ON audit_event_log (create_by, create_at DESC);
-- 按动作类型查询审计记录
CREATE INDEX IF NOT EXISTS idx_audit_event_log_action_created
    ON audit_event_log (action_code, create_at DESC);
-- 全局审计列表按时间倒序查询
CREATE INDEX IF NOT EXISTS idx_audit_event_log_created
    ON audit_event_log (create_at DESC);
-- 按单次入口调用关联其产生的全部资源事件
CREATE INDEX IF NOT EXISTS idx_audit_event_log_correlation
    ON audit_event_log (correlation_id);
-- 按分布式 trace-id 关联跨 HTTP、gRPC 和服务调用的事件
CREATE INDEX IF NOT EXISTS idx_audit_event_log_trace
    ON audit_event_log (trace_id);
-- 一年保留期批量清理
CREATE INDEX IF NOT EXISTS idx_audit_event_log_expire
    ON audit_event_log (expire_at);
-- 预留客户端、SDK 或外部事件，防止同一个 sourceEventId 重复写入；NULL 的服务端事件不进入该索引
CREATE UNIQUE INDEX IF NOT EXISTS uk_audit_event_log_source_event
    ON audit_event_log (source_event_id)
    WHERE source_event_id IS NOT NULL;
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_audit_event_log_resource_created` | `resource_type, resource_id, create_at DESC` | 查询某个资源的完整操作历史 |
| `idx_audit_event_log_scope_created` | `scope_type, scope_id, create_at DESC` | 后续按资源管理员权限范围过滤 |
| `idx_audit_event_log_creator_created` | `create_by, create_at DESC` | 查询某个用户的全部操作 |
| `idx_audit_event_log_action_created` | `action_code, create_at DESC` | 按动作编码筛选 |
| `idx_audit_event_log_created` | `create_at DESC` | 超管查看全局时间线 |
| `idx_audit_event_log_correlation` | `correlation_id` | 定位同一次 HTTP、gRPC、SDK 或任务调用产生的事件 |
| `idx_audit_event_log_trace` | `trace_id` | 定位分布式调用链上的关联事件 |
| `idx_audit_event_log_expire` | `expire_at` | 一年保留期清理扫描 |
| `uk_audit_event_log_source_event` | `source_event_id` | 预留客户端、SDK 或外部事件幂等控制 |

### 审计事件业务规则

| 规则 | 说明 |
|------|------|
| 覆盖范围 | 除健康检查外，无论经 HTTP、gRPC、SDK 还是内部任务进入，均记录登录成功/失败/限流、认证和授权失败，以及已经进入 application service 的认证业务查询、创建、更新、删除、用户分配、Secret 历史读取、主密钥状态查询和分片提交；系统内部关键操作使用 `event_source=system` |
| Handler 校验边界 | 普通业务接口在 Handler 层发生的 JSON 转换、参数绑定或必填项校验失败不写业务审计，因为请求尚未形成有效业务操作；登录、JWT 认证和主密钥分片等安全边界按各自安全策略记录失败尝试 |
| 入口与调用方分离 | `entry_type` 表示服务实际接收调用的入口，`caller_type` 表示调用方。SDK 经 HTTP 调用时记录 `entry_type=http, caller_type=sdk`；SDK 经 gRPC 调用时记录 `entry_type=grpc, caller_type=sdk`；仅进程内直接调用才使用 `entry_type=sdk` |
| 通用入口信息 | `operation_name` 使用稳定、低基数名称：HTTP 写路由模板而非原始 URL，gRPC 写 FullMethod，SDK 写公开方法名，任务写任务名；协议专属信息只允许写入 `protocol_detail` 白名单 |
| 一资源一事件 | 一次入口调用批量操作多个逻辑资源时，每个资源写一条事件并共享 `correlation_id` / `trace_id` / `batch_id`；保证按 `resource_type + resource_id` 可直接查询，不依赖 JSON 扫描 |
| 操作人快照 | `create_by` 来自认证用户 ID，`create_by_name` 保存当时姓名；登录失败等未知用户使用 `actor_type=anonymous`，不伪造用户 ID |
| 可信时间 | `create_at` 只使用服务端时间；未来客户端、SDK 或外部事件的 `source_occurred_at` 仅供辅助展示，不参与顺序和保留期计算 |
| 变更内容 | 普通字段在 `change_detail` 中记录字段名和 before/after；用户未实际修改的字段不记录 |
| Secret 脱敏 | Secret 明文、密文均禁止进入审计表。仅记录 Secret `groupId`、key、环境 ID/code、版本号、批次 ID，并用 `changed=true, redacted=true` 表示值发生变化 |
| 其他敏感数据 | 禁止记录密码、password hash、JWT、access token、Authorization、Cookie、主密钥、密钥分片、私钥、连接串、完整 HTTP header、gRPC metadata、请求体和响应体 |
| 双重防护 | 业务模块只构造类型化白名单事件；audit 模块写库前再次递归拦截敏感字段名。`failure_reason` 使用稳定、安全文案，禁止直接保存底层错误字符串 |
| 强一致写入 | 数据库写操作与 success 审计事件必须使用同一 PostgreSQL 事务；审计写入失败则业务事务回滚。查询操作必须在审计写入成功后才向客户端返回数据 |
| 失败事件 | 业务事务失败后，failure 审计使用独立事务写入；失败事件写入失败时使用 `pkg/logger` 输出不含敏感数据的告警，但不能把失败操作误记为成功 |
| 内存状态操作 | 主密钥分片等内存状态变更必须使用“校验 -> 审计持久化 -> 提交内存状态”的预提交方式；审计失败时不得改变内存状态 |
| 外部事件预留 | 首期不接收复制、显示/隐藏等纯前端事件，也不接收 SDK 离线补报事件。未来启用时使用 `source_event_id` 做幂等，`source_occurred_at` 只作辅助；操作人、服务端时间、IP 等仍由后端根据认证和连接上下文填写，不信任事件源上送值 |
| 权限预留 | 当前查询接口只要求通过认证，`applyPermissionFilter` 保留为空实现。后续由独立权限管控系统提供授权结果，再按 `userId + scope_type + scope_id` 接入资源管理员过滤并支持超管全局查询；在该系统接入前禁止将当前实现误认为已具备资源级日志查看权限 |
| 保留期 | 每条记录默认写入一年后的 `expire_at`，当前阶段不实现清理任务。后续由系统保留期任务按 `expire_at` 分批物理清理到期数据，清理行为自身也必须留下系统审计事件 |
| 数据库变更 | 本项目维护表结构设计和代码字段映射，但暂不内置 migration；实际建表、加列和索引变更由外部数据库变更系统执行。应用版本发布前必须由外部系统先完成对应 DDL，避免代码与数据库结构不一致 |
| gRPC / SDK | 当前仅保留统一事件模型、入口类型和调用方字段，不实现 gRPC interceptor、SDK adapter 或离线事件接收；相关接入在对应协议能力开发时补充 |

不同接入方式的字段映射如下。`action_code` 始终表示业务动作，不随接入协议变化；例如下面三种入口执行相同业务时均使用 `secret.update`。

| 调用场景 | `event_source` | `entry_type` | `caller_type` | `operation_name` | `protocol_status` |
|----------|----------------|--------------|---------------|------------------|-------------------|
| Web 页面调用 HTTP API | `server` | `http` | `web` | `POST /api/v1/secret/update` | `200` |
| Go SDK 经 HTTP API 调用 | `server` | `http` | `sdk` | `POST /api/v1/secret/update` | `200` |
| Java SDK 经 gRPC 调用 | `server` | `grpc` | `sdk` | `/envvault.v1.SecretService/UpdateSecret` | `OK` |
| 进程内 SDK 直接调用应用服务 | `server` | `sdk` | `sdk` | `SecretClient.Update` | `success` |
| 系统保留期清理任务 | `system` | `job` | `system` | `audit.retention.cleanup` | `success` |

`protocol_detail` 只允许按入口类型写入预定义白名单。例如 HTTP 可记录 `method`，gRPC 可记录 `service`、`method`、`streamType`，SDK 可记录 `language`；路由参数、query、header、metadata、请求体和响应体均不得写入。SDK 名称和版本统一写入 `caller_name` / `caller_version`，不塞入 JSON。

`change_detail` 示例（仅定义安全结构，不要求数据库校验 JSON 内容）：

```json
[
  {
    "field": "name",
    "before": "旧项目名",
    "after": "新项目名",
    "redacted": false
  },
  {
    "field": "values.prod",
    "changed": true,
    "redacted": true
  }
]
```

### 可复用的现有能力

| 现有能力 | 审计模块复用方式 |
|----------|------------------|
| `pkg/userctx` | 从请求上下文取得 `create_by` / `create_by_name`，不允许业务请求自行声明操作人 |
| `middleware.RequestID` / `logger.TraceIDKey` | 首期复用现有 `x-request-id` 写入 `correlation_id`；它是请求关联 ID，不冒充分布式 `trace_id` |
| Gin 请求上下文 | HTTP adapter 统一填充入口类型、路由模板、状态码、client IP 和 User-Agent；禁止读取并序列化 body |
| 后续 gRPC interceptor | 从 FullMethod、status code、peer 和白名单 metadata 构造同一套审计上下文；禁止保存完整 metadata 或 message |
| 后续 SDK adapter | 填充 SDK 名称、版本、语言和调用 ID；若 SDK 底层使用 HTTP/gRPC，则入口类型仍记录实际协议，调用方类型记录为 `sdk` |
| `persistence.WithTx` / `persistence.TxDB` | 让审计仓储复用业务事务，保证业务写入和 success 审计原子提交 |
| `application.NicknameResolver` | 仅在上下文缺少姓名时补齐操作人姓名快照，查询失败保留空字符串 |
| `pkg/logger` | 只记录审计模块自身故障和安全告警，不作为审计表的成功写入替代品 |
| 现有 permission filter 预留 | 后续审计查询按 `userId + scope_type + scope_id` 接入资源权限过滤 |

### HMAC 哈希链增强方案（本期不启用）

只追加表和“不提供更新/删除接口”能降低误操作风险，但数据库高权限账号仍可能直接篡改数据。HMAC 哈希链可以提供**篡改检测**：每条审计记录的哈希同时包含上一条记录哈希，任意修改、插入、删除或重排都会导致后续校验失败。

计算规则建议为：

```text
eventHash = HMAC-SHA256(
    auditHmacKey,
    chainId || sequence || previousHash || canonicalEventPayload
)
```

- `canonicalEventPayload` 必须采用固定字段顺序、UTC 时间和稳定 JSON 编码，且不包含 `event_hash` 自身。
- HMAC 密钥必须通过独立 Kubernetes Secret 注入，不得与 Secret 主密钥或 JWT 签名密钥复用。
- 使用 `hash_key_id` 标识密钥版本，支持轮换；旧密钥需按审计保留期保留验证能力。
- 多实例并发写入时，需要在 PostgreSQL 中锁定对应链头记录，原子分配 `sequence` 并更新链头，不能由各实例自行计算上一条哈希。
- 建议按自然日建立一条链（如 `2026-08-28`），便于一年后整链清理并降低单链无限增长影响。
- 哈希链只能发现篡改，不能阻止拥有数据库和 HMAC 密钥的攻击者重算整条链。更强保证需要定期把每日链尾哈希写入外部只读存储或 SIEM 作为锚点。

未来启用时再新增 `chain_id`、`chain_sequence`、`previous_hash`、`event_hash`、`hash_key_id` 字段和独立的链头状态表。HMAC 未启用前不写空占位字段，避免让使用方误以为当前记录已经具备防篡改能力。
