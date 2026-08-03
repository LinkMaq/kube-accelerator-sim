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
   Deployments.
4. Run `apply --dry-run=server` against the exact target before the first
   persistent submission when the runtime is installed.
5. Submit with the native `kasim apply` command and retain JSON output under
   `dist/receipts/<scenario>/`.
6. Run `kasim status ... --watch -o json`, then inspect only Nodes labeled
   `simulation.kasim.io/scenario=<scenario>`.
7. When the user asks to see the whole cluster or open the UI, run `kasim ui`
   with no target flags when kubectl's current kubeconfig/context is the
   intended target; otherwise override it with `--kubeconfig` and/or
   `--context`. Verify the context printed before the URL. Keep the default
   loopback listener unless the user explicitly requests another listen host;
   then pass `--host` for that temporary process, surface the unencrypted-HTTP
   warning, and require restricted network access. Do not add a proxy, tunnel,
   or persistent service unless separately requested. Treat the complete
   fragment URL as a temporary read capability and never publish it.
8. Report the context, target fingerprint, Scenario UID and generation,
   resolved profiles, requested/observed pool totals, fidelity surfaces,
   diagnostics, and receipt paths.

Do not claim success from object creation alone. Require a `Ready` Snapshot
with the requested inventory and achieved fidelity surfaces.

For read-only inspection, distinguish scalar resource signals from native DRA
device identities. Keep health unknown unless the selected source reports it.
An Auxiliary Device Pool is a scheduling token and never proves a physical
NIC, link, CNI, network fabric, GPUDirect path, or data-plane connectivity.

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
