# 升级与回滚

运行时安装是针对已有、显式目标执行的独立 Helm 工作流。变更 release 前，保留当前
release 回执、每个 Scenario Instance 的状态回执、Chart values 和 Helm history：

```sh
helm get values kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  -o yaml

helm history kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system
```

## 升级前检查

1. 使用发布的校验和、签名、证明和 `release-receipt.json` 验证新 CLI、镜像和 Chart。
2. 确认目标仍在 Kubernetes 1.30–1.36，DRA Scenario 在 1.34–1.36。
3. 使用新 CLI 对保存的每个 Scenario 执行客户端 dry-run。
4. 比较档案修订/摘要和兼容性锁；目录摘要变化是输入变化，不是自动迁移。
5. 确认所有场景已收敛，且没有 `CleanupBlocked`、`OwnershipConflict` 或重试条件。

## 升级

```sh
helm upgrade kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --reuse-values \
  --wait
```

rollout 后检查 Helm 状态和每个场景。不要为了测试升级而提交新修订；先确认控制器
在期望修订不变的情况下观察到已有状态。

## 回滚

如果控制器或 Chart rollout 失败，使用 `helm history` 中准确的旧修订：

```sh
helm rollback kasim-runtime replace-with-helm-revision \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

Helm 不回滚 CRD。不要手工改写 Scenario Instance 存储或所有权标签。回滚只影响共享
运行时 release，不应删除场景、Synthetic Node、用户工作负载或集群。再次变更前，
重新验证所有状态回执。
