# 待办事项

- [ ] `/api/v1/user/allocate` 执行 `remove` 前，检查用户负责的租户、组织、项目和文件夹等资源；必须先将管理员职责移交给其他用户后才能移除。当前应用层已预留 `ResponsibilityChecker` 扩展点，待负责人移交规则和上下级关系确定后实现。

- [ ] 增加用户锁定/解锁接口。更新 `user_info.is_blocked` 后必须同步刷新 Redis `user:blocked` 状态缓存；多实例部署时还需保证各实例及时观察到状态变化。

- [ ] `/api/v1/secret/history` 接入环境权限模型。当前请求已传递用户 ID，历史分页、批次和分组目标查询均预留 SQL 权限 scope；权限表落地后在 scope 中过滤无权环境，并与 `envList` 条件取交集。


- [ ] 移除非空校验 
  ALTER TABLE user_info
  ALTER COLUMN tenant_id DROP NOT NULL,
  ALTER COLUMN org_id DROP NOT NULL;