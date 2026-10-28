# 权限策略工厂与责任链

## 本期范围

已交付可复用的 Go 框架 `pkg/permission`，以及 `pkg/permission/biz` 中的业务权限检查策略骨架。组织详情接口已接入租户访问链；管理员、成员、环境、权限码及远程接口的具体判断仍待实现。不新增数据库表、权限配置页或 REST 接口。

参考 `efficient-common/docs/PERMISSION_CHAIN_DESIGN.md` 的注册、上下文隔离、稳定排序和全节点通过语义。Java 的 Spring 自动装配、SpEL 和既有 RBAC 实现不直接移植；Go 通过构造器注入扩展实现。

## 业务调用

应用启动时创建一个 `*permission.Factory`，注入需要鉴权的业务服务。后续业务中的核心调用为：

```go
err := s.permissions.Check(ctx, "envvault.secret.value.read", secretID)
```

调用方必须检查错误，校验通过后才能读取明文或产生业务副作用：

```go
if err := s.permissions.Check(ctx, "envvault.secret.value.read", secretID); err != nil {
    return nil, err
}
```

需要业务参数时传入一个可选 map：

```go
err := s.permissions.Check(ctx, "envvault.secret.value.read", secretID,
    permission.Values{"requestedEnv": envCode})
```

这些是未来业务接入示例，示例关键字尚未注册。关键字必须由后端业务代码定义，不能从 HTTP 请求中接收链名、策略名、表达式或关闭校验开关。资源 ID 必须非空，`*` 不表示全部资源。只传关键字不足以确定资源级校验的目标。

## 模块契约

| 类型 | 职责 |
| --- | --- |
| `Resource` | 资源类型和 ID，不预置租户、环境等业务字段 |
| `Subject` | 已认证主体类型和 ID，不含 Token |
| `Request` | 资源、动作、不可信业务参数，不含用户和链选择参数 |
| `Context` | 主体、请求、由业务加载的可信属性 |
| `ContextFactory` | 获取真实认证身份、确认资源目标、加载可信事实 |
| `Strategy` | 用 `Key()` 标识实现，通过 `Check(ctx, input, node)` 判断 |
| `OrderedStrategy` | 可选扩展，用 `Order()` 提供默认顺序；未实现时为 100 |
| `NodeValidator` | 可选扩展，启动时校验每一次策略引用的配置 |
| `Node` | 节点名、策略名、可选顺序、静态策略参数 |
| `ChainDefinition` | 业务关键字、资源类型、动作和非空节点集合 |
| `Factory` | 启动时校验与编译；运行时按关键字找到完整业务链 |
| `Decision` | 当前节点的 ALLOW、DENY、ERROR 决策 |
| `Failure` | 区分未认证、权限拒绝和执行失败的错误 |

关键词是服务端业务场景的快捷入口：工厂从注册定义取得资源类型和动作，并结合资源 ID 构造 Request，调用方不需要重复传资源类型和动作。同一资源类型与动作仍只允许注册一条链；不能用另一关键字为同一操作注册更宽松的规则。

## 注册与装配

下面是后续业务装配片段，`contextFactory` 和策略实例由业务提供：

```go
factory, err := permission.NewFactory(
    contextFactory,
    []permission.Strategy{resourceStrategy, operationStrategy},
    []permission.ChainDefinition{{
        Key:          "envvault.secret.value.read",
        ResourceType: "envvault.secret.item",
        Action:       "value.read",
        Nodes: []permission.Node{
            {Key: "resource", StrategyKey: resourceStrategy.Key()},
            {Key: "operation", StrategyKey: operationStrategy.Key()},
        },
    }},
)
if err != nil {
    return err // 启动失败，不能继续使用一个空工厂或默认放行实现。
}
```

策略需实现以下契约；正常拒绝返回 DENY，依赖故障可返回 ERROR 或 Go error：

```go
type Strategy interface {
    Key() string
    Check(context.Context, permission.Context, permission.Node) (permission.Decision, error)
}
```

没有预置“全部允许”策略，也没有默认主体或默认上下文工厂。不会在包初始化时注册全局变量。后续真实策略通过依赖注入复用数据库和客户端，公共包不依赖业务 repository。

启动时拒绝：缺少工厂、空策略集合或链集合、空标识、空链、重复策略名、重复链名、重复资源/动作路由、重复节点名、未知策略、非法 options，以及校验器错误或 panic。同一策略可以在多条链或同一链的多个不同节点复用。

`Node.Order == nil` 继承策略顺序；显式 0、负数都有效。同顺序保持节点声明顺序。排序和 Validate 只在注册时执行，不在每次请求时重做。注册完成后不支持增删或动态替换策略。

## 业务权限检查策略

业务内容单独放在 `pkg/permission/biz`，依赖公共框架；公共框架不反向依赖业务实现。

- `RemoteStrategy`：策略标识为 `envvault.remote-permission`，将权限检查结果转换为责任链决策。
- `CheckPermissionFunc`：可注入的权限检查函数，接收标准 context、可信主体、资源类型与 ID、动作。
- `CheckBackendPermission`：预留的后端权限接口调用入口，目前返回 `ErrBackendNotImplemented`。接口地址、认证方式和响应协议确定后，在这里补充调用和结果解析。

后续装配时，可以把该策略加入指定业务链：

```go
remoteStrategy := biz.NewRemoteStrategy(biz.CheckBackendPermission)
factory, err := permission.NewFactory(
    contextFactory, // 业务提供，必须读取真实认证身份并确认资源目标。
    []permission.Strategy{remoteStrategy},
    []permission.ChainDefinition{{
        Key:          "envvault.secret.value.read",
        ResourceType: "envvault.secret.item",
        Action:       "value.read",
        Nodes: []permission.Node{
            {Key: "backend-permission", StrategyKey: biz.RemoteStrategyKey},
        },
    }},
)
if err != nil {
    return err
}
```

导入路径为 `env-vault/pkg/permission/biz`。这里的关键字仍是装配示例，未注册到现有应用，也未挂载到现有路由。后续可以注入具有客户端依赖的对象方法，替换 `CheckBackendPermission` 函数入口；检查函数须支持并发复用并遵守 context 超时。

后端明确返回允许时，当前策略返回 ALLOW；明确返回不允许时，返回 `REMOTE_PERMISSION_DENIED` 的 DENY，HTTP 映射为 403。接口不可用、响应无法确认或尚未实现时返回 error，由责任链按执行失败处理，HTTP 映射为 500。即使检查函数同时返回 true 和 error，也不会放行。

未配置检查函数会在注册阶段报错。前端传入的业务参数不会替代可信主体、资源和动作；不在扩展 Map 中传递凭证，也不在策略实例中保留当前请求状态。

### 组织详情接入

`POST /api/v1/org/info` 已接入 `biz.NewTenantAccessFactory` 注册的 `envvault.tenant.access` 链。链目标为 `envvault.tenant`，动作为 `access`。应用启动时创建工厂并注入组织服务；创建失败会阻止应用启动。

组织服务先读取组织记录取得真实 `TenantID`，再执行下面的校验，通过后才解析管理员名称并返回详情：

```go
if err := s.permissions.Check(readCtx, permissionbiz.TenantAccess, o.TenantID.String()); err != nil {
    return nil, err
}
```

认证中间件通过 `userctx.Set` 将已验证用户写入 Gin 和标准请求 context；`tenantAccessContext` 从标准 context 取得主体，不读取请求 body 中的 userId 或 tenantId。原有请求取消、截止时间和 traceId 保持传递。组织没有有效租户归属时按执行失败处理。

权限后端明确拒绝时，接口返回 HTTP 403、`{"code":403,"msg":"Forbidden","data":null}`。没有认证主体时返回 401；依赖故障、未注入工厂或尚未实现的检查入口返回 500，均不返回组织详情。

**当前 `CheckBackendPermission` 仍是 TODO，真实组织详情请求会返回 500，直到补充后端权限接口调用。** 测试通过注入检查函数分别验证允许、拒绝和故障路径，生产代码没有默认放行或本地临时权限规则。组织的其他操作尚未接入此链。

## 执行与失败

工厂解析服务端关键字，验证调用参数，调用上下文工厂，然后执行编译后的有序链。上下文工厂不得替换资源类型、ID 或动作。没有认证主体时返回认证失败，禁止默认账号或默认全部资源。

- ALLOW 只通过当前节点，继续执行后续节点。
- DENY 需要非空 reasonCode 和经过处理的 message，立即停止。
- ERROR、零值决策、未知结果、策略返回 error 或 panic 都立即失败，不会继续其他节点。
- 策略返回一个 Denied error 也属于执行失败；正常拒绝必须通过 DENY 决策表达。
- 上下文工厂可以返回 Unauthenticated、Denied、EvaluationError；其他 error 包装为执行失败。
- 任何 OR 条件由某一个业务策略完整实现，不能把 OR 两侧拆成两个都必须通过的节点。
- 成功只授权本次资源/动作，不产生同请求下其他资源的隐式授权。

所有扩展都接收标准 `context.Context`，须遵守取消和截止时间。框架在上下文准备与每个节点前后检查取消状态，不会将取消后的 ALLOW 当成成功。框架同步执行，不创建遗留后台任务；不能强行中断一个忽略 context 的业务实现，其 IO 客户端也必须支持超时。

## 并发和可信属性

工厂的注册结果只读；策略和上下文工厂需要支持并发，不能在实例字段保存当前用户、资源、节点或上次结果。

注册配置、参数、属性在交界处复制；每个节点收到独立的顶层 map 和 order 指针，节点修改不会影响下一节点或下一请求。`Values` 不接受空键或 nil 值。复制是浅复制，嵌套 Map、List、指针和对象必须由业务提供不可变值或独立快照，调用方不能在执行期间并发修改输入。不要在这些字段存放令牌、Secret 明文或密文。

参数和可信属性分开存放。工厂不会把 `Parameters` 中的同名值合并进 `Attributes`；身份与归属等事实必须由业务上下文工厂从可信来源取得。

`userctx.Set` 同时写入 Gin 和标准请求上下文，业务 ContextFactory 从标准 context 读取已验证身份。`userctx.WithUser` 仅供可信服务端调用方补充认证身份，禁止将前端提供的 userId 直接写入其中。

## HTTP 错误适配与日志

公共执行器返回 error，不写 HTTP 响应。可选 `response.AbortPermissionError(c, err)` 支持 Gin：

```go
if response.AbortPermissionError(c, err) {
    return
}
// 继续现有的其他业务错误处理。
```

| 错误类型 | HTTP / body.code | body.msg |
| --- | --- | --- |
| AuthenticationFailure | 401 | Unauthorized |
| DeniedFailure | 403 | Forbidden |
| EvaluationFailure | 500 | Internal Server Error |

沿用 `{code,msg,data}`，失败 data 为 null；不向 HTTP 响应添加 reasonCode，不反射策略消息和内部 cause。内部可通过 `errors.As` 获取 Failure，再读取 Kind、ReasonCode 和 Cause；Cause 不参与自动 unwrap，避免依赖错误中的 Denied 被误当作正常权限拒绝。业务包装 Failure 时可以使用 `%w`。

日志使用项目 `pkg/logger`，包含链、节点、策略、顺序、主体、目标和耗时，沿用已有 traceId。不会打印参数、属性、options、Token、原始异常或策略消息；内部 cause 通过 Failure 保留供受控诊断。Values 的字符串格式化隐藏扩展内容，但业务仍禁止直接将上下文序列化为 JSON 写入日志。

## 后续业务接入范围

目前仅组织详情接入了租户访问链；业务权限检查策略仍只有调用骨架，未实现管理员/成员/环境规则或权限中心接口调用，未引入 SpEL 或新的表达式语言、未增加跨请求权限缓存。

单资源 Check 不等于列表数据权限。后续列表应明确全部、无权限、有限范围，并在数据库分页和统计前过滤。批量修改须在任何副作用之前覆盖全部目标；创建操作须明确以哪个父资源授权。Folder/Secret 的 groupId 与单环境 id 也需由各业务关键字明确区分。

真实策略的授权规则、权限中心协议和首个接入接口在后续业务阶段确定；本期不据此改变现有权限语义。

## 验证

```bash
go test -race ./pkg/permission/... ./pkg/response
go test -race ./...
go vet ./...
```

测试覆盖注册冲突、nil 扩展、稳定排序与显式零值顺序、重复引用策略、节点配置启动校验、全节点通过、拒绝与故障短路、panic、未知关键字、主体缺失、目标被替换、取消、输入隔离、跨链并发复用，以及标准 HTTP 401/403/500 映射。测试使用隔离的测试策略，不代表已经实现任何业务授权规则。

业务策略测试额外覆盖可信目标传递、允许与拒绝、依赖错误不能转成放行、缺少检查函数时注册失败，以及未实现的后端调用始终按执行失败处理。
