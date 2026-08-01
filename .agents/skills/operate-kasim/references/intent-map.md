# Conversational intent map

Use this map to translate user language into one native operation. Preserve
the distinction between runtime installation, Scenario lifecycle, and cluster
lifecycle.

| User intent | Operation | Required inputs |
| --- | --- | --- |
| “安装/部署 Kasim” | Install or upgrade `kasim-runtime` with Helm | kubeconfig, context |
| “启动/模拟 N 台某型号设备” | Compile and apply a homogeneous `demo` Scenario | profile, model, nodes, cards per Node |
| “部署多厂商/混合卡集群” | Compile and apply a Scenario YAML | groups, profiles, models, topology |
| “列出支持的厂家/型号” | `profile list` and selected `profile show` | none; offline |
| “看看有哪些卡/节点” | `status` plus scenario-labeled Node inventory | Scenario name, target |
| “模拟坏卡/恢复卡” | `health` typed revision | Scenario, group, pool, healthy count |
| “扩到/缩到 N 台” | `scale` typed revision | Scenario, group, replicas |
| “更新这个场景文件” | File-based revision | fresh UID/generation and revised file |
| “停止/删除设备场景” | Safe `delete` | fresh UID/generation |
| “卸载 Kasim” | Remove Helm runtime after all Scenarios are gone | explicit uninstall request |
| “安装 Kubernetes” | Separate infrastructure task, never a `kasim` command | explicit user authorization and version |
| “安装 CUDA/ROCm/CANN 驱动” | Explain out-of-scope physical runtime work | physical hardware and driver requirements |

## Defaults that are safe to infer

- Use `scheduling` unless the user explicitly needs stable DRA behavior.
- Set healthy units equal to total units when no failure is requested.
- Use one Accelerator Pool per homogeneous `demo` request.
- Use a stable DNS-safe Scenario name derived from the requested topology.
- In a source checkout, use `./charts/kasim-runtime` when the doctor confirms
  the local CLI catalog matches the repository.
- Use a pinned OCI Chart version when the user requests a published or
  reproducible installation, or when a matching local build is unavailable.

## Choices that must remain explicit

- Kubeconfig path and context for every connected operation.
- Vendor profile and model when more than one matches the phrase.
- Acceptance of a provisional profile, including `kunlunxin-hami`.
- Destructive Scenario deletion or Helm uninstall when the user asked only to
  stop observing, inspect, or diagnose.
- Any operation on the Kubernetes cluster lifecycle.

## Example phrases

- “在 target 集群启动两台 H100，每台八卡” → inspect `nvidia`, compile a
  two-Node/eight-unit `demo`, server dry-run, apply, and watch status.
- “同时模拟英伟达、昇腾和海光” → start from the corresponding files in
  `examples/vendors/`, produce one heterogeneous Scenario, then compile it.
- “把 nvidia-workers 扩到四台” → fetch fresh status, extract UID/generation,
  run `scale`, and watch the new revision.
- “让一台卡坏掉” is ambiguous because health is per Node Group/Pool; ask for
  the Scenario group/pool and desired healthy count per Synthetic Node.
