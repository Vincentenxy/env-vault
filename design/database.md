# 数据库表设计

> 本文档记录 env-vault 项目所有数据库表结构（PostgreSQL），实现变更时必须同步更新本文档。
> 所有表结构使用 SQL 代码块描述。
>
> **通用字段约定**：所有业务表必须包含以下字段
>
> | 列名 | 类型 | 说明 |
> |------|------|------|
> | `create_at` | `timestamptz NOT NULL DEFAULT now()` | 创建时间 |
> | `update_at` | `timestamptz NOT NULL DEFAULT now()` | 更新时间 |
> | `create_by` | `varchar(64) NOT NULL DEFAULT ''` | 创建人 |
> | `update_by` | `varchar(64) NOT NULL DEFAULT ''` | 更新人 |

---

<!-- 表设计示例格式（新增表时复制此模板）：

## 表名：`example_table`

**说明**：示例表用途描述

```sql
CREATE TABLE example_table (
    id          bigserial PRIMARY KEY,
    name        varchar(128) NOT NULL DEFAULT '' COMMENT '名称',
    create_at   timestamptz  NOT NULL DEFAULT now(),
    update_at   timestamptz  NOT NULL DEFAULT now(),
    create_by   varchar(64)  NOT NULL DEFAULT '',
    update_by   varchar(64)  NOT NULL DEFAULT ''
);

CREATE INDEX idx_example_table_name ON example_table (name);
```

**索引说明**：

| 索引名 | 字段 | 说明 |
|--------|------|------|
| `idx_example_table_name` | `name` | 按名称查询 |

-->
