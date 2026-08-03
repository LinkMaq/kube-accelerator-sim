# 在已有集群中安装运行时

`kasim` 从不创建集群，也不调用 Helm。安装者必须显式选择 kubeconfig/context，
在需要时创建命名空间，并将共享运行时作为独立操作安装：

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  create namespace kasim-system

helm upgrade --install kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

支持 Kubernetes 1.30–1.36。`scheduling` 覆盖整个范围；稳定版
`resource.k8s.io/v1` DRA 控制平面投射要求 1.34–1.36。Chart 会拒绝范围外的服务端。

## 权限角色

Chart 定义了默认拒绝、职责分离的身份：

| 身份 | 用途 | 能否修改 Node、Lease 或 DRA 清单 |
| --- | --- | --- |
| observer | 读取和观察 Scenario Instance | 不能 |
| operator | 提交、更新、观察和删除准确实例；读取必要的目标身份和模拟器对象 | 不能 |
| controller | 调谐准确归属于模拟器的资源 | 可以，但受应用层所有权检查约束 |
| KWOK controller | 维护固定版本的模拟 Node/Pod 表面 | 仅限其准确运行时表面 |
| Stage installer | Helm hook 安装/删除五个固定名称 Stage | 仅限这五个 Stage |

生成的 ClusterRole 名为 `<release>-kasim-runtime-observer` 和
`<release>-kasim-runtime-operator`。CLI 提交者不应获得控制器、CRD/RBAC/Namespace
变更、Secret、模拟身份、Pod 驱逐或 ServiceAccount token 权限。

## 所有权与卸载

产品和 KWOK Stage CRD 使用所有权根 `kasim-runtime/v1alpha1`。已有 CRD 如果没有
准确且兼容的所有权根，安装会失败；Chart 不会静默接管。Helm 同样拒绝被其他
release 拥有的同名运行时对象。

卸载会保留 CRD，只删除该 release 准确拥有的 ServiceAccount、Role、Binding、
Deployment、ConfigMap 和五个 Stage。Scenario Instance、Synthetic Node、用户工作
负载、无关 KWOK 资源及集群本身都不属于卸载范围。

## 供应链输入

控制器使用固定摘要的多架构构建/运行时基础镜像。发布流程同时提供 CLI 校验和、
SBOM、来源证明、控制器镜像和 OCI/TGZ Chart；安装前请执行[发布验证](release-verification.md)。
