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
An existing Kubernetes cluster, selected explicitly by the user, to which the CLI submits an accelerator simulation scenario. The cluster lifecycle is owned outside the product CLI.
_Avoid_: Managed cluster, embedded cluster

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
The owned, observable realization of one Scenario in a Simulation Target. Its identity bounds reconciliation, status reporting, updates, and cleanup so simulated state cannot be confused with pre-existing cluster state.
_Avoid_: Unowned mutation, anonymous mock state

**Fidelity Mode（保真模式）**:
A declared boundary of Kubernetes behavior that a Scenario Instance promises to exercise and report truthfully. A Fidelity Mode distinguishes observed scheduler or control-plane behavior from node-runtime protocols and real accelerator computation that were not exercised.
_Avoid_: Backend, implementation mode, hardware fidelity

**Vendor Profile（厂商配置档案）**:
A source-backed declaration of the Kubernetes-visible resource names, labels, device classes, and scheduling attributes exposed by one accelerator ecosystem. Vendor Profiles extend the vendor-neutral core without introducing vendor branches into it.
_Avoid_: Vendor-specific engine, hard-coded vendor path

**Accelerator Model（加速器型号）**:
A commercially distinct accelerator board or SKU represented within a Vendor Profile and selectable by a Scenario. Model support means its relevant Kubernetes-visible identity and scheduling attributes are source-backed, not merely that its marketing name appears in a catalog.
_Avoid_: Display-only model, invented per-model resource name
