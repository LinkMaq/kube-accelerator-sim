---
layout: home

hero:
  name: kube-accelerator-sim
  text: Accelerator capacity, without accelerator hardware.
  tagline: Simulate Capacity. Validate Scheduling. Project source-backed GPU, NPU, DCU, and accelerator contracts into an existing Kubernetes cluster.
  image:
    src: /kasim-logo.png
    alt: Kasim logo
  actions:
    - theme: brand
      text: Start with an existing cluster
      link: /operators/quickstart
    - theme: alt
      text: Explore scenarios
      link: /operators/scenario-examples

features:
  - icon: ◈
    title: Evidence-backed vendors
    details: Exact Kubernetes resource contracts for NVIDIA, AMD, Ascend, Hygon, Cambricon, and a broad accelerator catalog.
  - icon: ◎
    title: Safe target selection
    details: Lifecycle commands name both kubeconfig and context; the read-only UI can use your current target. The CLI never owns Kubernetes cluster lifecycle.
  - icon: ⎈
    title: Scheduling and stable DRA
    details: Scalar scheduling across Kubernetes 1.30–1.36 and stable resource.k8s.io/v1 projection on 1.34–1.36.
  - icon: ↻
    title: Receipt-driven lifecycle
    details: Ready snapshots, immutable revisions, optimistic preconditions, bounded ownership, and safe cleanup.
  - icon: ◫
    title: Read-only local inventory
    details: One loopback command shows Kasim and non-Kasim Nodes, exact accelerator and auxiliary signals, native DRA identities, and evidence gaps.
---

<section class="kasim-surface">
  <p class="kasim-eyebrow">One declarative path</p>
  <h2>From a Scenario to scheduler-visible capacity</h2>
  <p class="kasim-lead">
    Kasim compiles a source-backed Scenario, validates the exact Simulation
    Target, projects owned control-plane objects, and reports what Kubernetes
    actually observed.
  </p>
  <div class="kasim-flow">
    <div class="kasim-flow-step">
      <span>01 / describe</span>
      <strong>Choose contracts</strong>
      <p>Select Vendor Profiles, models, capacity, health, and topology.</p>
    </div>
    <div class="kasim-flow-step">
      <span>02 / compile</span>
      <strong>Resolve evidence</strong>
      <p>Pin profile revisions, digests, resource names, and fidelity.</p>
    </div>
    <div class="kasim-flow-step">
      <span>03 / project</span>
      <strong>Reconcile safely</strong>
      <p>Create only Scenario-owned Synthetic Nodes and control-plane surfaces.</p>
    </div>
    <div class="kasim-flow-step">
      <span>04 / observe</span>
      <strong>Trust receipts</strong>
      <p>Require a Ready snapshot before claiming the requested inventory.</p>
    </div>
  </div>
</section>

<section class="kasim-surface">
  <p class="kasim-eyebrow">Bounded compatibility</p>
  <h2>A tested range, not an open-ended promise</h2>
  <p class="kasim-lead">
    The 0.1 release freezes Kubernetes compatibility to 1.30–1.36. Stable DRA
    control-plane projection begins at 1.34.
  </p>
  <div class="kasim-compat" aria-label="Supported Kubernetes versions">
    <span class="kasim-version">1.30 scheduling</span>
    <span class="kasim-version">1.31 scheduling</span>
    <span class="kasim-version">1.32 scheduling</span>
    <span class="kasim-version">1.33 scheduling</span>
    <span class="kasim-version active">1.34 scheduling + DRA</span>
    <span class="kasim-version active">1.35 scheduling + DRA</span>
    <span class="kasim-version active">1.36 scheduling + DRA</span>
  </div>
</section>

<section class="kasim-surface">
  <p class="kasim-eyebrow">Truthful fidelity</p>
  <h2>Clear about what is—and is not—being simulated</h2>
  <div class="kasim-boundary">
    <div class="in">
      <h3>Validated surfaces</h3>
      <ul>
        <li>Capacity, allocatable accounting, and placement</li>
        <li>Vendor identity labels and resource contracts</li>
        <li>Health, scale, revisions, ownership, and cleanup</li>
        <li>Stable DRA inventory and scheduler allocation</li>
      </ul>
    </div>
    <div class="out">
      <h3>Deliberately out of scope</h3>
      <ul>
        <li>Physical device access or accelerator computation</li>
        <li>CUDA, ROCm, CANN, firmware, or physical vendor telemetry</li>
        <li>NUMA topology, CDI injection, and node preparation</li>
        <li>Kubernetes cluster creation or lifecycle management</li>
      </ul>
    </div>
  </div>
</section>
