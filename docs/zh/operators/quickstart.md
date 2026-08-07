# 已有集群快速开始

本教程从一个已经存在的 Kubernetes 集群开始。`kasim` 不创建、升级、停止或
删除集群。生命周期命令使用显式 kubeconfig 和 context；只读 `kasim ui` 默认
遵循 kubectl 的 kubeconfig 加载规则和当前 context。

目标集群必须是 Kubernetes 1.30–1.36。这个范围全部支持 `scheduling`；只有
1.34–1.36 支持 `dra-control-plane`。

## 1. 构建或解压 CLI

从源码构建：

```sh
go build -trimpath -o ./dist/kasim ./cmd/kasim
./dist/kasim version -o json
./dist/kasim --help
```

使用发布制品时，请先按[发布验证](release-verification.md)校验归档。控制器镜像和
Helm Chart 发布在 GitHub Packages：

```sh
docker pull ghcr.io/linkmaq/kube-accelerator-sim-controller:0.4.0
helm pull oci://ghcr.io/linkmaq/charts/kasim-runtime --version 0.4.0
```

## 2. 离线检查并编译

查看目录和客户端 dry-run 都不会访问 Kubernetes：

```sh
./dist/kasim profile list -o json
./dist/kasim profile show nvidia -o json
./dist/kasim apply -f ./examples/single-node-single-accelerator.yaml \
  --dry-run=client \
  -o json
```

编译回执包含规范化 Scenario 摘要、目录摘要以及准确的档案修订和摘要。档案摘要
变化应被视为需要评审的输入变化。

## 3. 安装共享运行时

仅在命名空间不存在时创建它，然后使用同一个显式 kubeconfig/context 安装：

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  create namespace kasim-system

helm upgrade --install kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

使用已发布的不可变 OCI Chart：

```sh
helm upgrade --install kasim-runtime \
  oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.4.0 \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

检查 release 和控制器 rollout：

```sh
helm status kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system

kubectl --kubeconfig ./target.kubeconfig --context target \
  --namespace kasim-system \
  rollout status deployment/kasim-runtime-kasim-runtime-controller

kubectl --kubeconfig ./target.kubeconfig --context target \
  --namespace kasim-system \
  rollout status deployment/kasim-runtime-kasim-runtime-telemetry
```

运行时安装与 `kasim apply` 有意分离。CLI 不调用 Helm，也不隐式安装集群级资源。

## 4. 提交并观察 Scenario

```sh
./dist/kasim apply -f ./examples/single-node-single-accelerator.yaml \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json | tee apply-receipt.json

./dist/kasim status single-node-single-accelerator \
  --kubeconfig ./target.kubeconfig \
  --context target \
  --watch \
  -o json | tee status-receipt.json
```

只查看该 Scenario 标记的 Synthetic Node：

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  get nodes \
  -l simulation.kasim.io/scenario=single-node-single-accelerator \
  -o wide
```

状态回执是 readiness、期望/观察修订、实例 UID、解析后的档案、保真表面、诊断和
对象数量的权威依据。

转发默认只读 Prometheus 端点，可以检查这些节点的模拟原厂指标结构：

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  --namespace kasim-system \
  port-forward service/kasim-runtime-kasim-runtime-telemetry 9400:9400

curl --fail http://127.0.0.1:9400/metrics
```

证据覆盖、ServiceMonitor 发现、标签和数值语义见
[模拟厂商 Prometheus 遥测](simulated-vendor-telemetry.md)。

需要快速查看 Kasim 与真实节点上的设备信号时，启动本地只读清单：

```sh
./dist/kasim ui --open
./dist/kasim ui --help
```

临时访问能力、证据规则、筛选和部分数据源行为见[集群清单 UI 指南](cluster-inventory-ui.md)。

## 5. 修改健康数量和副本数

从最新状态回执复制准确的 `instanceUID` 和 `desiredGeneration`：

```sh
INSTANCE_UID='replace-with-exact-status-instance-uid'
GENERATION='replace-with-exact-positive-desired-generation'

./dist/kasim health single-node-single-accelerator \
  --group workers \
  --pool accelerator \
  --healthy 0 \
  --instance-uid "$INSTANCE_UID" \
  --expected-generation "$GENERATION" \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

再次获取状态并使用新修订号扩容：

```sh
./dist/kasim status single-node-single-accelerator \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json

./dist/kasim scale single-node-single-accelerator \
  --group workers \
  --replicas 3 \
  --instance-uid "$INSTANCE_UID" \
  --expected-generation 'replace-with-new-positive-generation' \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

这两个命令创建类型化 Scenario 修订，不直接 patch Node，也不驱逐已绑定的 Pod。

## 6. 安全删除准确的 Scenario

取得最终状态回执后，使用其中准确的 UID 和修订号：

```sh
./dist/kasim delete single-node-single-accelerator \
  --instance-uid 'replace-with-exact-status-instance-uid' \
  --expected-generation 'replace-with-exact-positive-generation' \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json | tee delete-receipt.json
```

删除只处理能够证明属于该实例 UID 的白名单对象。若非所属 Pod 仍绑定在 Synthetic
Node 上，会返回 `CleanupBlocked`；请按[故障排查与安全](troubleshooting-security.md)
处理。项目不提供强制或通配删除。

## 7. 单独卸载共享运行时

先安全删除全部 Scenario Instance，再卸载 Helm release：

```sh
helm uninstall kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

Chart 会保留 CRD，不会删除命名空间、Scenario Instance、用户工作负载、无关 KWOK
对象或 Kubernetes 集群。删除专用命名空间前，请单独确认其中没有需要保留的内容。
