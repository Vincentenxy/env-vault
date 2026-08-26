# 待办事项

- [ ] `/api/v1/user/allocate` 执行 `remove` 前，检查用户负责的租户、组织、项目和文件夹等资源；必须先将管理员职责移交给其他用户后才能移除。当前应用层已预留 `ResponsibilityChecker` 扩展点，待负责人移交规则和上下级关系确定后实现。

- [ ] 增加用户锁定/解锁接口。更新 `user_info.is_blocked` 后必须同步刷新 Redis `user:blocked` 状态缓存；多实例部署时还需保证各实例及时观察到状态变化。

- [ ] `/api/v1/secret/history` 接入环境权限模型。当前请求已传递用户 ID，历史分页、批次和分组目标查询均预留 SQL 权限 scope；权限表落地后在 scope 中过滤无权环境，并与 `envList` 条件取交集。

- [ ] shamri 秘钥分片管理
- [ ] k8s集群内部自谦证书，使用k8s csr
  - 创建RBAC权限：给Pod一个ServiceAccount，授权它可以创建和批准CertificateSigningRequest资源
  - Pod启动时自动生成私钥和CSR：Pod启动脚本里调用openssl生成私钥和CSR（证书签名请求），通过K8s API提交
  - K8s自动签发：Kubernetes内置的控制器会自动签发一个有效期很短（比如24小时）的证书

- [ ] 针对不同的文件夹，使用不同的key粗略，目前仅支持大写字符、数字、下划线，后续要支持设置用户密码，或者指定的类型，跟文件夹走，做对应的文件夹层级的格式校验