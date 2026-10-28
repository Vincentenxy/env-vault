# 全局审计日志中心（第一阶段）

系统内提供独立的只读日志页面 `/app/audit`，由 `POST /api/v1/audit/global/list` 查询既有审计表。Java SDK 协议和审计写入流程不变，不新增数据库表。

## 查询约定

- 支持资源类型、原资源 ID、操作动作、操作人 ID、结果、起止时间、批次 ID、请求关联 ID、Trace ID 组合筛选。字符串精确匹配；省略条件可跨资源查询。
- 分页在 HTTP Handler 统一 Normalize，一次传入 Service，使用 `page.Response[AuditEventDTO]` 返回 `total/list`，按 `create_at DESC, id DESC` 稳定排序。
- 不关联租户、组织、项目、Folder 等当前资源表，已删除资源日志仍然保留。资源名称来自事件快照，不推测当前删除状态。
- 时间使用 RFC3339，包含起止边界。Web 默认最近 7 个自然日；清空时间后取消时间限制。
- 详情使用当前页中返回的不可变事件，不额外请求接口；脱敏字段仅展示“发生变更”。
- 原 `/api/v1/audit/list` 继续要求 `resourceType + resourceId`。

## 页面快捷入口与链接

- 用户管理行内“操作记录”按 `resourceType=user`、`resourceId=工号 userId` 查询用户资料操作，不使用用户表 UUID，也不将目标用户写入 `createBy`。
- 用户管理工具栏“列表查询记录”按 `resourceType=userCollection` 查询；资源 ID 为当前租户 ID，未选租户时为 `all`。成员分配事件仍归属实际租户、组织或项目资源，从对应资源的日志入口查看。
- 个人密钥列表行内“操作记录”按 `resourceType=personalSecret`、`resourceId=密钥 id` 查询，与“历史版本”入口独立。已删除密钥可在全局中心使用原 ID 查询。
- 上述快捷入口使用 `timeRange=all`，不限制日期；直接打开全局中心仍默认最近 7 个自然日。筛选与分页保存在 URL，支持刷新、复制链接、浏览器前进 / 后退；URL 不携带密钥内容或完整资源对象。无效链接显示校验提示并阻止查询。

## 权限接入与当前边界

1. 路由仍挂在 JWT 认证分组下，未认证调用不能进入业务查询。
2. `Service.GlobalList` 通过 `globalPermissions.Check(ctx, biz.AuditGlobalRead, "global")` 调用责任链。当前用户只读取可信 `userctx`，不从筛选条件接受身份。
3. `pkg/permission/biz/audit_access.go` 注册 `envvault.audit.global.read`。按本阶段需求，`CheckAuditGlobalPermission` 暂时返回允许；取消请求仍失败。这个允许策略只用于全局审计，不影响原租户业务策略。
4. 策略拒绝返回标准 403，未认证返回 401，策略缺失、依赖异常或查询失败返回 500；不会因异常默认允许。
5. 持久层 `applyPermissionFilter` 当前仍是明确标注的 no-op。后续接入独立权限中心时，用当前登录用户查询可信范围，再按事件 `scope_type/scope_id` 限制 SQL，必须作用于 Count 和分页两条查询。客户端传的 `createBy` 只是被查询的操作人。

**当前阶段任何通过认证的用户均能查询全局日志，还没有完成资源范围隔离。** 菜单可见性只是展示，真实权限边界在后端。

## 本轮之后的事项

- 主密钥页面的“操作记录”快捷入口；用户管理和个人密钥列表入口已补充，复用现有全局查询接口。
- 登录与认证的专属预设视图；当前可通过全局中心的资源类型、动作与结果筛选查看已写入的对应事件。
- 接入真实全局查看授权和可信数据范围过滤。
- 决定查询日志是否生成 `audit.read`，以及保留期清理策略；本轮不改变审计写入和保留期。

接口契约见 [audit-global-openapi.yaml](audit-global-openapi.yaml)。ApiPost 需在明确要求同步时另行更新。
