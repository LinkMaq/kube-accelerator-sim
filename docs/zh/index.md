---
layout: home

hero:
  name: kube-accelerator-sim
  text: 无需加速器硬件，也能模拟加速器容量。
  tagline: 模拟容量，验证调度。把有来源依据的 GPU、NPU、DCU 等加速器资源契约投射到已有 Kubernetes 集群。
  image:
    src: /kasim-logo.png
    alt: Kasim 标志
  actions:
    - theme: brand
      text: 在已有集群中开始
      link: /zh/operators/quickstart
    - theme: alt
      text: 查看场景示例
      link: /zh/operators/scenario-examples

features:
  - icon: ◈
    title: 有证据支撑的厂商契约
    details: 覆盖 NVIDIA、AMD、昇腾、海光、寒武纪等主流生态的准确 Kubernetes 资源名和型号信息。
  - icon: ◎
    title: 安全选择目标集群
    details: 生命周期命令显式指定 kubeconfig 和 context；只读 UI 可使用当前目标。CLI 不负责 Kubernetes 集群生命周期。
  - icon: ⎈
    title: 调度与稳定版 DRA
    details: Kubernetes 1.30–1.36 支持标量资源调度，1.34–1.36 支持稳定版 resource.k8s.io/v1 控制平面投射。
  - icon: ↻
    title: 由回执驱动的生命周期
    details: Ready 快照、不可变修订、乐观并发前置条件、边界清晰的所有权和安全清理。
  - icon: ◫
    title: 只读本地清单
    details: 一条回环命令即可查看 Kasim 与真实节点、准确的加速卡和辅助信号、原生 DRA 标识与证据缺口。
---

<section class="kasim-surface">
  <p class="kasim-eyebrow">一条声明式路径</p>
  <h2>从 Scenario 到调度器可见容量</h2>
  <p class="kasim-lead">
    Kasim 编译有来源依据的 Scenario，校验明确的 Simulation Target，
    投射归属于场景的控制平面对象，并报告 Kubernetes 实际观察到的结果。
  </p>
  <div class="kasim-flow">
    <div class="kasim-flow-step">
      <span>01 / 描述</span>
      <strong>选择资源契约</strong>
      <p>选择 Vendor Profile、型号、容量、健康数量和拓扑。</p>
    </div>
    <div class="kasim-flow-step">
      <span>02 / 编译</span>
      <strong>解析证据</strong>
      <p>固定档案修订、摘要、资源名称和保真模式。</p>
    </div>
    <div class="kasim-flow-step">
      <span>03 / 投射</span>
      <strong>安全调谐</strong>
      <p>只创建归属于 Scenario 的 Synthetic Node 和控制平面资源。</p>
    </div>
    <div class="kasim-flow-step">
      <span>04 / 观察</span>
      <strong>以回执为准</strong>
      <p>只有 Ready 状态快照才能证明目标资源清单已经就绪。</p>
    </div>
  </div>
</section>

<section class="kasim-surface">
  <p class="kasim-eyebrow">有边界的兼容性</p>
  <h2>经过测试的范围，而不是无限承诺</h2>
  <p class="kasim-lead">
    0.1 版本把 Kubernetes 兼容范围固定为 1.30–1.36；稳定版 DRA
    控制平面投射从 1.34 开始。
  </p>
  <div class="kasim-compat" aria-label="支持的 Kubernetes 版本">
    <span class="kasim-version">1.30 调度</span>
    <span class="kasim-version">1.31 调度</span>
    <span class="kasim-version">1.32 调度</span>
    <span class="kasim-version">1.33 调度</span>
    <span class="kasim-version active">1.34 调度 + DRA</span>
    <span class="kasim-version active">1.35 调度 + DRA</span>
    <span class="kasim-version active">1.36 调度 + DRA</span>
  </div>
</section>

<section class="kasim-surface">
  <p class="kasim-eyebrow">诚实的保真边界</p>
  <h2>明确说明模拟了什么，以及没有模拟什么</h2>
  <div class="kasim-boundary">
    <div class="in">
      <h3>已验证的表面</h3>
      <ul>
        <li>容量、可分配量统计和调度放置</li>
        <li>厂商身份标签和资源契约</li>
        <li>健康度、扩缩容、修订、所有权和清理</li>
        <li>稳定版 DRA 清单和调度器分配</li>
      </ul>
    </div>
    <div class="out">
      <h3>明确不在范围内</h3>
      <ul>
        <li>物理设备访问或加速器计算</li>
        <li>CUDA、ROCm、CANN、固件或物理厂商遥测</li>
        <li>NUMA 拓扑、CDI 注入和节点准备</li>
        <li>Kubernetes 集群创建或生命周期管理</li>
      </ul>
    </div>
  </div>
</section>
