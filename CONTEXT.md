# Kubernetes Accelerator Simulation

This context models how Kubernetes sees and schedules simulated accelerator capacity in clusters without physical accelerator hardware. It covers vendor-neutral simulation concepts, not real accelerator computation.

## Language

**Accelerator（加速器）**:
A Kubernetes-schedulable specialized compute device class, including GPUs, NPUs, DCUs, and equivalent vendor-specific devices. The umbrella term does not imply a physical form factor, vendor, or compute API.
_Avoid_: GPU, graphics card, 显卡 when referring to the vendor-neutral category

**Accelerator Simulation（加速器模拟）**:
A representation of accelerator capacity, availability, health, and placement behavior that is observable by the Kubernetes control plane and scheduler without requiring physical accelerator hardware. It does not imply real device access or accelerated computation inside a container.
_Avoid_: Hardware emulation, compute emulation

**Simulation Target（模拟目标集群）**:
An existing Kubernetes cluster selected by the user. Lifecycle commands name it explicitly before submitting an accelerator simulation scenario; the read-only UI may resolve it from the user's current kubeconfig. The cluster lifecycle is owned outside the product CLI.
_Avoid_: Managed cluster, embedded cluster

**Cluster Simulation Inventory（集群模拟清单）**:
A read-only, target-scoped view of the Nodes, Scenario Instances, accelerator signals, device evidence, auxiliary signals, and scheduling usage observable in one Simulation Target. It includes both Synthetic Nodes and pre-existing non-Kasim Nodes, while making Kasim ownership and evidence provenance visually explicit.
_Avoid_: Management console, Kasim-only node list, inferred hardware inventory

**Synthetic Node（模拟节点）**:
A Kubernetes Node created for a Scenario Instance and owned entirely by it, used to expose simulated accelerator capacity without mutating pre-existing real Nodes in the Simulation Target.
_Avoid_: Patched real node, unowned fake node

**End-to-End Test Harness（端到端测试设施）**:
Test-only infrastructure that may create and destroy a disposable lightweight Kubernetes cluster to exercise the complete user workflow. Its cluster lifecycle responsibilities do not belong to the product CLI.
_Avoid_: CLI cluster manager

**Scenario（模拟场景）**:
A declarative, reproducible description of simulated accelerator capacity and its distribution across a Simulation Target. CLI shortcuts and scenario files are alternative inputs that compile to this same model.
_Avoid_: Command-specific configuration, vendor-specific command model

**Scenario Instance（场景实例）**:
The target-scoped, owned, and observable realization of a Scenario. A Scenario Instance has a stable identity and advances through immutable Scenario Revisions, bounding reconciliation, status, updates, and cleanup.
_Avoid_: Unowned mutation, anonymous mock state

**Scenario Revision（场景修订版）**:
An immutable, normalized desired state accepted for a Scenario Instance. Capacity, health, placement, or scale changes create a new revision instead of an imperative side channel or embedded time script.
_Avoid_: Mutable command state, workflow timeline

**Node Group（节点组）**:
A named homogeneous template and replica count for Synthetic Nodes in a Scenario. Different capacity, placement, or Accelerator composition is represented by a different Node Group rather than per-node overrides.
_Avoid_: Individual node manifest, backend node template

**Accelerator Pool（加速器池）**:
A named homogeneous set of simulated Accelerator units repeated on every Synthetic Node in a Node Group, with one Vendor Profile, Accelerator Model, Resource Contract, variant, total count, and healthy count.
_Avoid_: Arbitrary resource map, physical device inventory

**Fidelity Mode（保真模式）**:
A declared boundary of Kubernetes behavior that a Scenario Instance promises to exercise and report truthfully. A Fidelity Mode distinguishes observed scheduler or control-plane behavior from node-runtime protocols and real accelerator computation that were not exercised.
_Avoid_: Backend, implementation mode, hardware fidelity

**Vendor Profile（厂商配置档案）**:
A versioned, source-backed declaration of the Kubernetes-visible contracts exposed by one Accelerator ecosystem or named integration. A Vendor Profile records provenance explicitly so a community integration cannot be mistaken for a vendor default.
_Avoid_: Vendor-specific engine, hard-coded vendor path

**Accelerator Model（加速器型号）**:
A commercially distinct accelerator board or SKU represented within a Vendor Profile and selectable by a Scenario. Model support means its relevant Kubernetes-visible identity and scheduling attributes are source-backed, not merely that its marketing name appears in a catalog.
_Avoid_: Display-only model, invented per-model resource name

**Resource Contract（资源契约）**:
One source-backed Kubernetes scheduling surface within a Vendor Profile, expressed either through scalar extended resources or DRA. It preserves exact resource names, labels, attributes, variants, provider scope, and evidence without deriving them from an Accelerator Model name.
_Avoid_: Guessed resource mapping, backend configuration

**Auxiliary Device Signal（辅助设备信号）**:
A source-backed Kubernetes-visible resource, label, or DRA device associated with an accelerator scheduling environment, such as an RDMA resource signal. Simulation of an Auxiliary Device Signal promises only the declared Kubernetes scheduling surface, not a physical network interface, driver, CNI path, or working data plane.
_Avoid_: Real RDMA device, inferred network capability, accelerator pool

**Auxiliary Device Pool（辅助设备池）**:
A named homogeneous set of Auxiliary Device Signals repeated on every Synthetic Node in a Node Group, with one source-backed Resource Contract, explicit Accelerator Pool associations, total quantity, and schedulable availability. It shares the Node Group lifecycle but remains distinct from Accelerator capacity and physical device inventory.
_Avoid_: Accelerator Pool, arbitrary resource map, physical NIC inventory

**Profile Class（档案等级）**:
The trust and distribution class of a Vendor Profile: verified, provisional, or custom. It describes the evidence behind emitted Kubernetes contracts, independently of whether an Accelerator Model is current, older-but-supported, or merely cataloged.
_Avoid_: Model popularity, hardware certification

**Simulated Vendor Telemetry（模拟厂商遥测）**:
A Prometheus observation surface for Synthetic Nodes whose metric-family names,
types, native label keys, and units come from a source-backed Telemetry Contract
while every value is generated by Kasim. It is explicitly not a physical sensor
reading, vendor exporter process, driver observation, performance result, or
accelerator-compute claim.
_Avoid_: Vendor telemetry, physical telemetry, hardware monitoring

**Telemetry Contract（遥测契约）**:
An immutable, evidence-classified declaration of one exporter schema: exact
Prometheus metric families, types, units, native labels, product gates, and
unsupported behavior. It is versioned separately from scheduling Vendor
Profiles and Resource Contracts so telemetry evidence can evolve without
changing a Scenario Revision or its scheduling identity.
_Avoid_: Metric-name guess, vendor Adapter, scheduling Resource Contract
