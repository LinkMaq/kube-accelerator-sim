# 故障排查与安全

## 常见失败

| 诊断 | 含义 | 安全处理方式 |
| --- | --- | --- |
| `InvocationInvalid` 要求目标参数 | 联网命令缺少 `--kubeconfig` 或 `--context` | 同时提供两者；CLI 不回退到环境或当前 context |
| `TargetInvalid` 或目标指纹变化 | context 解析结果或 `kube-system` 身份变化 | 停止操作，核对 API Server、CA 摘要和集群身份 |
| Kubernetes 版本被拒绝 | 服务端不在 1.30–1.36 | 使用受支持目标，不绕过预检 |
| DRA 不可用 | 稳定版 DRA 不存在或版本不在 1.34–1.36 | 若符合测试目标则改用 `scheduling`，否则更换集群 |
| provisional 档案被拒绝 | 场景未显式接受临时证据 | 审查来源后再显式接受，或选择 verified 档案 |
| `OwnershipConflict` | 目标对象属于其他 UID 或缺少准确所有权 | 调查该对象，不要随意重新标记或接管 |
| `CleanupBlocked` | 非所属 Pod 仍绑定在 Synthetic Node | 按下述有边界流程处理 |
| `Overcommitted` | 新可分配量低于已有请求 | 保持工作负载不动，再决定是否恢复容量 |

始终保留 JSON 诊断和最新状态回执。网络错误发生在请求被接受之后，并不等于请求被拒绝。

## 安全处理删除阻塞

`kasim delete` 从不删除或驱逐非所属 Pod。收到 `CleanupBlocked` 后，从状态回执复制
准确的 Node/Pod 引用并检查该 Node：

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  get pods --all-namespaces \
  --field-selector spec.nodeName=replace-with-blocked-node \
  -o wide
```

确认每个工作负载的所有者，通过其正常运维流程协调迁移或删除。不要修改模拟器所有权
标签、删除 finalizer、patch Node 或寻找强制参数。外部 Pod 消失后，重新取得状态，
再使用准确实例 UID 和当前修订号重试删除：

```sh
./dist/kasim status replace-with-instance-name \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json

./dist/kasim delete replace-with-instance-name \
  --instance-uid 'replace-with-exact-status-instance-uid' \
  --expected-generation 'replace-with-exact-positive-generation' \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

## 权限模型

只把用户和自动化绑定到 observer 或 lifecycle-operator。不要向 CLI 提交者授予控制器、
KWOK controller、Stage installer、CRD/RBAC 变更、Secret、模拟身份、Pod 驱逐或
ServiceAccount token 权限。使用独立管理身份进行 Helm 安装与升级。

## 保真度与数据边界

Scenario 文件只保存可移植期望状态，不包含 kubeconfig、凭据、原始 Kubernetes 对象、
生成设备 ID 或任意 patch。回执会脱敏凭据，并分别报告规范 API 地址、目标指纹和
CA 摘要。所有生成的设备身份都是确定性模拟器身份，不是厂商硬件序列号。
