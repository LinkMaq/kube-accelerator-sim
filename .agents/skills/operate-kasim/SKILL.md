---
name: operate-kasim
description: Operate kube-accelerator-sim from natural-language requests. Use when a user asks to install or upgrade the Kasim runtime in an existing Kubernetes cluster; start, mock, or deploy NVIDIA, AMD, Huawei Ascend, Hygon DCU, Cambricon, Biren, Iluvatar, Enflame, Moore Threads, MetaX, Intel, or other simulated accelerators; add RDMA or SR-IOV Auxiliary Device Pools; open the read-only kasim ui; inspect profiles, nodes, resources, DRA devices, status, and receipts; change simulated health or scale; safely stop or delete a Scenario Instance; or diagnose a Kasim deployment.
---

# Operate Kasim

Translate conversational accelerator intent into the native Helm, `kasim`,
and `kubectl` workflows of this repository. Preserve the product boundary:
Kasim projects Kubernetes-visible simulated capacity and does not provide real
drivers, device files, telemetry, or accelerator computation.

## Establish the target

1. Read the repository `AGENTS.md` and `CONTEXT.md` before acting.
2. Run the bundled doctor offline:

   ```sh
   python3 .agents/skills/operate-kasim/scripts/doctor.py
   ```

3. For lifecycle or mutation operations, obtain both an explicit kubeconfig
   path and exact context name. The read-only `kasim ui` command may use the
   standard current kubeconfig/current-context defaults when that is the
   user's intended target; record the context printed before the URL and stop
   if it is unexpected.
4. Treat the cluster as an existing Simulation Target. Do not make `kasim`
   create, upgrade, stop, or delete it. Provision a cluster only when the user
   separately and explicitly requests that infrastructure action.
5. Run the doctor again with the exact target before mutation:

   ```sh
   python3 .agents/skills/operate-kasim/scripts/doctor.py \
     --kubeconfig PATH --context NAME
   ```

Stop if the target identity changes, Kubernetes is outside 1.30–1.36, or the
requested fidelity is unavailable. Use `scheduling` on 1.30–1.36 and
`dra-control-plane` only on 1.34–1.36.

## Resolve conversational intent

Collect or safely infer the Scenario name, vendor profile, model, Node count,
accelerators per Node, healthy accelerators per Node, and fidelity. Default
`healthy` to total capacity and fidelity to `scheduling`. Do not guess a
vendor, model, target, or provisional-profile acceptance.

Read [intent-map.md](references/intent-map.md) to map phrases such as “安装”,
“启动两台 H100”, “模拟坏卡”, “扩到四台”, and “停止场景” to operations. Read
[operations.md](references/operations.md) before executing a connected or
lifecycle operation.

For one homogeneous pool, prefer `kasim apply demo`. For heterogeneous,
multi-pool, partitioned, or reusable configurations, use a Scenario document.
Start from `examples/` and preserve its exact profile revision and digest.
For RDMA or SR-IOV signals, start from
`examples/signals/auxiliary-rdma-sriov.yaml`; preserve the local Accelerator
Pool association and require the user-provided fully qualified resource name.
For every Ascend vNPU preset-template resource signal, start from
`examples/signals/ascend-vnpu-templates.yaml`.

Vendor discovery node labels (the gpu-operator/GFD set for NVIDIA,
`node.kubernetes.io/npu.chip.name` + `servertype` for Ascend) are opt-in per
Node Group: set `discoveryLabels: true` under `node:` in a Scenario document,
or pass `--discovery-labels` to the `apply demo` shortcut. Without the flag,
label keys are reserved with empty values by design; do not treat that as a
regression.

Always inspect the catalog before choosing identifiers:

```sh
"$KASIM_BIN" profile list -o json
"$KASIM_BIN" profile show PROFILE_ID -o json
```

Never invent resource names, model IDs, profile digests, identity labels, or
provider scope. Treat provisional profiles as rejected unless the user has
reviewed the evidence and explicitly accepts them.

## Execute the workflow

1. Locate a matching released `kasim` binary or build `./dist/kasim` from this
   checkout. Verify `kasim version -o json`.
2. Compile every new or edited Scenario offline with
   `apply --dry-run=client -o json`. Present the resolved profiles, resource
   names, topology, capacity, and fidelity boundary.
3. Install or upgrade the shared runtime separately with Helm only when the
   user asked for installation. In a source checkout, default to the local
   `./charts/kasim-runtime` Chart after the doctor confirms the CLI catalog
   matches the repository. Use a pinned OCI Chart release when the user asks
   for a published or reproducible deployment, or when no matching local
   build is available. Report the chosen source and version; ask about it only
   when multiple valid choices would materially change the result. Use the
   same explicit kubeconfig/context as the CLI, and wait for both controller
   Deployments. When the upgrade ships a new bundled catalog (new profile
   revision or digest), existing Scenario Instances that pin the old profile
   digest fail telemetry compilation and the telemetry Deployment never
   becomes ready, which fails the Helm upgrade with `Pending termination`.
   Upgrade in this order: delete every existing Scenario Instance with the
   guarded `kasim delete` first, run the Helm upgrade while no scenario
   exists (telemetry is ready on an empty cluster), then resubmit scenarios
   with the new `kasim` binary.
4. Enable Prometheus scraping whenever the target supports it. Check for the
   Prometheus Operator API `monitoring.coreos.com/v1 ServiceMonitor`
   (`kubectl api-resources --api-group=monitoring.coreos.com`) on the exact
   target. When the CRD exists, install or upgrade the runtime with
   `--set telemetry.serviceMonitor.enabled=true` and verify afterward that the
   `*-telemetry` ServiceMonitor exists and Prometheus reports the telemetry
   target up. When the CRD is absent, state that the ServiceMonitor was not
   created and port-forwarding remains the only telemetry access path.
5. Run `apply --dry-run=server` against the exact target before the first
   persistent submission when the runtime is installed.
6. Submit with the native `kasim apply` command and retain JSON output under
   `dist/receipts/<scenario>/`.
7. Run `kasim status ... --watch -o json`, then inspect only Nodes labeled
   `simulation.kasim.io/scenario=<scenario>`.
8. When the user asks to see the whole cluster or open the UI, run `kasim ui`
   with no target flags when kubectl's current kubeconfig/context is the
   intended target; otherwise override it with `--kubeconfig` and/or
   `--context`. Verify the context printed before the URL. Keep the default
   loopback listener unless the user explicitly requests another listen host;
   then pass `--host` for that temporary process, surface the unencrypted-HTTP
   warning, and require restricted network access. Do not add a proxy, tunnel,
   or persistent service unless separately requested. Treat the complete
   fragment URL as a temporary read capability and never publish it.
9. Report the context, target fingerprint, Scenario UID and generation,
   resolved profiles, requested/observed pool totals, fidelity surfaces,
   diagnostics, and receipt paths.

Do not claim success from object creation alone. Require a `Ready` Snapshot
with the requested inventory and achieved fidelity surfaces.

For read-only inspection, distinguish scalar resource signals from native DRA
device identities. Keep health unknown unless the selected source reports it.
An Auxiliary Device Pool is a scheduling token and never proves a physical
NIC, link, CNI, network fabric, GPUDirect path, or data-plane connectivity.

## Deliver images to the target node without Docker

The Simulation Target may run containerd only (kubeadm default) with no Docker
daemon, and the operator workstation may lack a working Docker or SSH client.
Delivery path that only needs `kubectl` plus local cross-compilation:

1. Cross-compile: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` both
   binaries with the same ldflags as the Makefile.
2. Create a privileged Pod on the target node (`nodeName`, `tolerations:
   [{operator: Exists}]`, `securityContext.privileged: true`) mounting the
   host root at `/host`.
3. Fetch the pinned distroless base image through the registry API from the
   Pod (anonymous token + manifest by digest), `kubectl cp` the blobs local,
   assemble an OCI layout tar (base layers + one application layer with the
   binaries; config carries `User 65532:65532`, the entrypoint, exposed ports,
   and OCI labels), `kubectl cp` it back, then
   `chroot /host ctr -n k8s.io images import <tar>` and
   `ctr -n k8s.io images tag ... <repo>@sha256:<manifest-digest>` so a
   digest-pinned Helm value resolves. containerd v2 unpacks lazily (CRI
   deferred unpack), so imported images start without an explicit unpack.

## Resolve CleanupBlocked from platform DaemonSets

`CleanupBlocked: cleanup is blocked by bound Pods` lists any non-terminal Pod
bound to an owned Synthetic Node. Cluster platform DaemonSets without node
selectors (exporters, log collectors, proxy pods) schedule onto every Ready
Synthetic Node and permanently block scenario deletion. The scoped fix is to
patch each offending DaemonSet template with a required nodeAffinity of
`simulation.kasim.io/scenario DoesNotExist`; Pods on the real node are
unaffected and Pods on Synthetic Nodes are removed by the DaemonSet
controller. Report the patch to the user; do not delete user workloads or
broaden deletion scope.

## Revise and remove safely

Before `health`, `scale`, a file-based revision, or `delete`, fetch a fresh
status receipt. Use the exact `instanceUID` and current `desiredGeneration`
from that receipt as mutation preconditions. Never reuse stale values.

Do not patch Synthetic Nodes directly. Do not evict or delete user workloads,
remove finalizers, relabel ownership, or invent a force-delete path. If Kasim
returns `CleanupBlocked` or `Overcommitted`, preserve the receipt, identify the
bounded objects, and explain the safe next action without broadening scope.

Uninstall the Helm runtime only after every Scenario Instance has been safely
removed and only when the user explicitly asks. Never delete the Kubernetes
cluster as part of runtime cleanup.

## Conversation behavior

- If the user asks only for a plan, explanation, or status, stay read-only.
- If a requested write is fully specified and scoped to the selected target,
  execute it without repeatedly asking for confirmation.
- Ask one concise question only when the missing target, vendor/model, desired
  topology, or provisional evidence choice would materially change the result.
- Explain that “启动设备” means publishing and reconciling logical accelerator
  capacity visible to Kubernetes, not starting physical hardware.
- End with the achieved state, exact limitations, and copyable status command.
