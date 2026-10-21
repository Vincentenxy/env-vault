
kubectl apply -f env-vault-statefulset.yaml

滚动更新
kubectl -n env-vault rollout restart statefulset/env-vault

查看进度
kubectl -n env-vault rollout status statefulset/env-vault

## 应用配置存储

- 数据库、Redis 和测试主密钥从 env-vault-config 的 config.yaml 读取
- 节点通信 Token 从 env-vault-config 的 master-key-peer-token 注入环境变量，供应用和就绪探针使用
- JWT 公私钥直接填写在 config.yaml 的 auth.local.private_key 和 public_key 中，使用 YAML 多行文本格式
- 所有应用配置统一存放在 env-vault-config，仅挂载 config.yaml，不再挂载 JWT 文件
- 程序仍兼容 private_key_file 和 public_key_file，文件路径配置优先，使用内联内容时应移除或清空路径字段
- Higress 的 qiuer-net 仍为 TLS Secret，由 Ingress 的 tls.secretName 引用

从旧 Secret 方案迁移时，先 apply，再等待 StatefulSet 滚动更新成功
确认没有旧 Pod 后，可删除不再引用的应用 Secret，不要删除 TLS Secret

```bash
kubectl -n env-vault delete secret env-vault-runtime env-vault-jwt --ignore-not-found
```

若曾部署过独立的 env-vault-runtime 或 env-vault-jwt ConfigMap，滚动更新成功后也可以清理
普通 apply 不会自动删除已经从 YAML 移除的资源

```bash
kubectl -n env-vault delete configmap env-vault-runtime env-vault-jwt --ignore-not-found
```

以后只修改 ConfigMap 的内容仍需手动滚动重启，应用启动时读取配置
