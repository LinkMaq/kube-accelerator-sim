const variants = {
  A: 'Evidence-first ledger',
  B: 'Node workspace',
  C: 'Ecosystem matrix',
}

const copy = {
  en: {
    title: 'Cluster Simulation Inventory',
    target: 'kind-kasim · Kubernetes 1.36.3',
    prototype: 'THROWAWAY PROTOTYPE',
    live: 'Live', partial: 'Partial data', stale: 'Stale snapshot',
    lastUpdated: 'updated 4s ago',
    kasimNodes: 'Kasim nodes', otherNodes: 'Non-Kasim nodes',
    acceleratorUnits: 'Accelerator units', nativeDevices: 'Native DRA devices',
    auxiliary: 'Auxiliary tokens', scenarios: 'Ready scenarios',
    search: 'Search node, vendor, model, resource or device ID',
    allOrigins: 'All origins', kasimOnly: 'Kasim only', otherOnly: 'Non-Kasim only',
    signals: 'Device and signal inventory', nodes: 'Node inventory',
    diagnostics: 'Source diagnostics', details: 'Evidence details',
    capacity: 'Capacity', allocatable: 'Allocatable', requested: 'Requested',
    health: 'Health', source: 'Evidence', association: 'Association',
    unknown: 'Unknown', simulated: 'Simulated availability', reported: 'Reported',
    close: 'Close', language: '中文', mode: 'Data state',
    noResults: 'No matching inventory.',
  },
  zh: {
    title: '集群模拟清单',
    target: 'kind-kasim · Kubernetes 1.36.3',
    prototype: '一次性原型',
    live: '实时', partial: '部分数据', stale: '快照已过期',
    lastUpdated: '4 秒前更新',
    kasimNodes: 'Kasim 节点', otherNodes: '非 Kasim 节点',
    acceleratorUnits: '加速器单位', nativeDevices: '原生 DRA 设备',
    auxiliary: '辅助资源令牌', scenarios: '就绪场景',
    search: '搜索节点、厂商、型号、资源或设备 ID',
    allOrigins: '全部来源', kasimOnly: '仅 Kasim', otherOnly: '仅非 Kasim',
    signals: '设备与信号清单', nodes: '节点清单',
    diagnostics: '数据源诊断', details: '证据详情',
    capacity: '容量', allocatable: '可分配', requested: '已请求',
    health: '健康度', source: '证据', association: '关联',
    unknown: '未知', simulated: '模拟可用性', reported: '已报告',
    close: '关闭', language: 'English', mode: '数据状态',
    noResults: '没有匹配的清单项。',
  },
}

const nodes = [
  { id: 'kasim-h100-0', origin: 'kasim', scenario: 'multi-vendor-lab', ready: true, profile: 'nvidia', signals: 2 },
  { id: 'kasim-mi300x-0', origin: 'kasim', scenario: 'multi-vendor-lab', ready: true, profile: 'amd', signals: 1 },
  { id: 'kasim-ascend-0', origin: 'kasim', scenario: 'ascend-training', ready: true, profile: 'huawei', signals: 1 },
  { id: 'worker-real-01', origin: 'external', scenario: '', ready: true, profile: 'unclassified', signals: 1 },
]

const inventory = [
  { id: 'h100-scalar', node: 'kasim-h100-0', origin: 'kasim', role: 'accelerator', vendor: 'NVIDIA', model: 'H100', representation: 'Extended resource', identity: 'nvidia.com/gpu', capacity: '8', allocatable: '6', requested: '4', health: 'simulated', evidence: 'Kasim Scenario + Node status', association: 'h100-pool', unit: 'devices' },
  { id: 'rdma-scalar', node: 'kasim-h100-0', origin: 'kasim', role: 'auxiliary', vendor: 'RDMA shared plugin', model: 'Shared HCA tokens', representation: 'Extended resource', identity: 'rdma/rdma_shared_device_a', capacity: '8', allocatable: '8', requested: '2', health: 'unknown', evidence: 'Auxiliary Device Pool + Node status', association: 'h100-pool', unit: 'shared tokens' },
  { id: 'amd-dra-0', node: 'kasim-mi300x-0', origin: 'kasim', role: 'accelerator', vendor: 'AMD', model: 'MI300X', representation: 'DRA device', identity: 'gpu.amd.com / pool-0 / gpu-0-128', capacity: '1', allocatable: '—', requested: 'allocated', health: 'unknown', evidence: 'resource.k8s.io/v1 ResourceSlice', association: 'mi300x-pool', unit: 'native device' },
  { id: 'amd-dra-1', node: 'kasim-mi300x-0', origin: 'kasim', role: 'accelerator', vendor: 'AMD', model: 'MI300X', representation: 'DRA device', identity: 'gpu.amd.com / pool-0 / gpu-1-129', capacity: '1', allocatable: '—', requested: 'unallocated', health: 'unknown', evidence: 'resource.k8s.io/v1 ResourceSlice', association: 'mi300x-pool', unit: 'native device' },
  { id: 'ascend-scalar', node: 'kasim-ascend-0', origin: 'kasim', role: 'accelerator', vendor: 'Huawei Ascend', model: 'Ascend 910B', representation: 'Extended resource', identity: 'huawei.com/Ascend910', capacity: '8', allocatable: '8', requested: '0', health: 'simulated', evidence: 'Kasim Scenario + Node status', association: 'ascend-pool', unit: 'devices' },
  { id: 'external-gpu', node: 'worker-real-01', origin: 'external', role: 'unclassified', vendor: 'Unclassified', model: 'Not inferred', representation: 'Extended resource', identity: 'nvidia.com/gpu', capacity: '2', allocatable: '2', requested: '1', health: 'unknown', evidence: 'Observed Node status only', association: 'none', unit: 'units' },
]

const app = document.querySelector('#app')
const params = new URLSearchParams(location.search)
let state = {
  variant: variants[params.get('variant')] ? params.get('variant') : 'A',
  lang: params.get('lang') === 'zh' ? 'zh' : 'en',
  mode: ['live', 'partial', 'stale'].includes(params.get('mode')) ? params.get('mode') : 'live',
  query: params.get('q') ?? '',
  origin: params.get('origin') ?? 'all',
  node: params.get('node') ?? 'kasim-h100-0',
  detail: null,
}

function esc(value) {
  return String(value).replace(/[&<>'"]/g, character => ({ '&':'&amp;', '<':'&lt;', '>':'&gt;', "'":'&#39;', '"':'&quot;' })[character])
}

function setURL() {
  const next = new URLSearchParams()
  next.set('variant', state.variant)
  if (state.lang === 'zh') next.set('lang', 'zh')
  if (state.mode !== 'live') next.set('mode', state.mode)
  if (state.query) next.set('q', state.query)
  if (state.origin !== 'all') next.set('origin', state.origin)
  if (state.node !== 'kasim-h100-0') next.set('node', state.node)
  history.replaceState(null, '', `?${next}`)
}

function t(key) { return copy[state.lang][key] ?? key }
function originLabel(origin) { return origin === 'kasim' ? 'Kasim' : state.lang === 'zh' ? '非 Kasim' : 'Non-Kasim' }
function modeLabel() { return t(state.mode) }

function filteredInventory() {
  const query = state.query.trim().toLowerCase()
  return inventory.filter(item => {
    if (state.origin !== 'all' && item.origin !== state.origin) return false
    if (!query) return true
    return Object.values(item).join(' ').toLowerCase().includes(query)
  })
}

function header() {
  return `<header class="app-header">
    <div class="brand"><span class="mark" aria-hidden="true">K</span><div><strong>Kasim</strong><small>${esc(t('prototype'))}</small></div></div>
    <div class="target"><span>${esc(t('title'))}</span><small>${esc(t('target'))}</small></div>
    <div class="header-actions">
      <span class="status status-${state.mode}"><i></i>${esc(modeLabel())} · ${esc(t('lastUpdated'))}</span>
      <button id="language" class="quiet">${esc(t('language'))}</button>
      <label class="mode-control"><span class="sr-only">${esc(t('mode'))}</span><select id="mode"><option value="live">Live</option><option value="partial">Partial</option><option value="stale">Stale</option></select></label>
    </div>
  </header>`
}

function summaryBand() {
  const metrics = [
    [t('scenarios'), '2', '2 / 2 Ready'],
    [t('kasimNodes'), '3', '3 Ready'],
    [t('otherNodes'), '1', 'observed, not inferred'],
    [t('acceleratorUnits'), '22', 'scalar units only'],
    [t('nativeDevices'), '2', 'DRA v1 identities'],
    [t('auxiliary'), '8', 'shared tokens, not NICs'],
  ]
  return `<section class="summary-band" aria-label="Inventory summary">${metrics.map(([label,value,note]) => `<div><span>${esc(label)}</span><strong>${value}</strong><small>${esc(note)}</small></div>`).join('')}</section>`
}

function filters() {
  return `<div class="filters"><label class="search"><span class="sr-only">${esc(t('search'))}</span><input id="search" value="${esc(state.query)}" placeholder="${esc(t('search'))}"></label><div class="segmented" role="group" aria-label="Node origin"><button data-origin="all" class="${state.origin === 'all' ? 'active' : ''}">${esc(t('allOrigins'))}</button><button data-origin="kasim" class="${state.origin === 'kasim' ? 'active' : ''}">${esc(t('kasimOnly'))}</button><button data-origin="external" class="${state.origin === 'external' ? 'active' : ''}">${esc(t('otherOnly'))}</button></div></div>`
}

function badge(item) {
  return `<span class="badge ${item.origin}">${esc(originLabel(item.origin))}</span><span class="badge ${item.role}">${esc(item.role)}</span>`
}

function health(item) {
  if (item.health === 'simulated') return `<span class="health simulated">◆ ${esc(t('simulated'))}</span>`
  return `<span class="health unknown">? ${esc(t('unknown'))}</span>`
}

function ledgerTable(items = filteredInventory()) {
  if (!items.length) return `<p class="empty">${esc(t('noResults'))}</p>`
  return `<div class="table-wrap"><table><caption class="sr-only">${esc(t('signals'))}</caption><thead><tr><th>Node / origin</th><th>Vendor / model</th><th>Signal / device identity</th><th>${esc(t('capacity'))}</th><th>${esc(t('allocatable'))}</th><th>${esc(t('requested'))}</th><th>${esc(t('health'))}</th><th>${esc(t('source'))}</th></tr></thead><tbody>${items.map(item => `<tr data-detail="${item.id}" tabindex="0"><td data-label="Node"><strong>${esc(item.node)}</strong><span>${badge(item)}</span></td><td data-label="Vendor / model"><strong>${esc(item.vendor)}</strong><span>${esc(item.model)}</span></td><td data-label="Signal"><code>${esc(item.identity)}</code><span>${esc(item.representation)} · ${esc(item.unit)}</span></td><td data-label="${esc(t('capacity'))}">${esc(item.capacity)}</td><td data-label="${esc(t('allocatable'))}">${esc(item.allocatable)}</td><td data-label="${esc(t('requested'))}">${esc(item.requested)}</td><td data-label="${esc(t('health'))}">${health(item)}</td><td data-label="${esc(t('source'))}"><span>${esc(item.evidence)}</span><button class="row-action" data-open="${item.id}">${esc(t('details'))}</button></td></tr>`).join('')}</tbody></table></div>`
}

function diagnostics() {
  const message = state.mode === 'live'
    ? 'All required sources are live. Legacy DRA schemas are not interpreted.'
    : state.mode === 'partial'
      ? 'ResourceClaims: list forbidden. Allocation is unavailable, never zero.'
      : 'Pod watch disconnected 41s ago. Last-known requests remain visible.'
  return `<aside class="diagnostic ${state.mode}"><strong>${esc(t('diagnostics'))}</strong><span>${esc(message)}</span><code>${state.mode === 'live' ? '6 sources live' : state.mode === 'partial' ? 'resourceclaims: forbidden' : 'pods: reconnecting'}</code></aside>`
}

function variantA() {
  return `<main id="main" class="layout-a"><section class="intro"><div><p class="eyebrow">Evidence-first inventory</p><h1>${esc(t('title'))}</h1><p>Counts stay separate from identities. Unknown health stays unknown.</p></div>${diagnostics()}</section>${summaryBand()}<section class="inventory-section"><div class="section-title"><div><p class="eyebrow">Home · visible without drill-down</p><h2>${esc(t('signals'))}</h2></div>${filters()}</div>${ledgerTable()}</section><section class="node-strip"><h2>${esc(t('nodes'))}</h2>${nodes.map(node => `<button data-node="${node.id}"><span>${esc(node.id)}</span>${node.origin === 'kasim' ? '<b>Kasim</b>' : '<b class="external">Non-Kasim</b>'}<small>${esc(node.scenario || 'No Kasim ownership')} · ${node.signals} signals</small></button>`).join('')}</section></main>`
}

function variantB() {
  const selected = nodes.find(node => node.id === state.node) ?? nodes[0]
  const items = filteredInventory().filter(item => item.node === selected.id)
  const first = items[0] ?? inventory[0]
  return `<main id="main" class="layout-b"><section class="workspace-head"><div><p class="eyebrow">Node operations workspace</p><h1>${esc(t('title'))}</h1></div>${diagnostics()}</section><div class="workspace"><aside class="node-rail"><div class="rail-summary"><strong>4</strong><span>Nodes · 3 Kasim</span></div><label class="search"><span class="sr-only">${esc(t('search'))}</span><input id="search" value="${esc(state.query)}" placeholder="${esc(t('search'))}"></label>${nodes.map(node => `<button data-node="${node.id}" class="${node.id === selected.id ? 'selected' : ''}"><span><strong>${esc(node.id)}</strong><small>${esc(node.scenario || 'Non-Kasim')}</small></span><i class="origin-dot ${node.origin}"></i></button>`).join('')}</aside><section class="node-focus"><div class="focus-title"><div>${selected.origin === 'kasim' ? '<span class="badge kasim">Kasim owned</span>' : '<span class="badge external">Non-Kasim</span>'}<h2>${esc(selected.id)}</h2><p>${esc(selected.scenario || 'Cluster-observed signals; hardware is not inferred.')}</p></div><div class="node-vitals"><span>Ready <b>True</b></span><span>Signals <b>${selected.signals}</b></span></div></div>${ledgerTable(items)}</section><aside class="evidence-inspector"><p class="eyebrow">Pinned inspector</p><h2>${esc(first.vendor)} ${esc(first.model)}</h2><dl><dt>Representation</dt><dd>${esc(first.representation)}</dd><dt>Native identity</dt><dd><code>${esc(first.identity)}</code></dd><dt>${esc(t('association'))}</dt><dd>${esc(first.association)}</dd><dt>${esc(t('source'))}</dt><dd>${esc(first.evidence)}</dd><dt>${esc(t('health'))}</dt><dd>${health(first)}</dd></dl><p class="truth-note">Requested is a scheduler reservation. It is not runtime utilization.</p></aside></div></main>`
}

function variantC() {
  const ecosystems = [
    ['NVIDIA', 'H100', '8 scalar units', 'RDMA · 8 shared tokens', 'kasim-h100-0'],
    ['AMD', 'MI300X', '2 native DRA devices', '1 allocated · runtime unknown', 'kasim-mi300x-0'],
    ['Huawei', 'Ascend 910B', '8 scalar units', 'healthy availability 8', 'kasim-ascend-0'],
    ['Unclassified', 'Not inferred', '2 scalar units', 'health not reported', 'worker-real-01'],
  ]
  return `<main id="main" class="layout-c"><section class="matrix-head"><div><p class="eyebrow">Ecosystem comparison</p><h1>${esc(t('title'))}</h1><p>Compare published scheduling surfaces without mixing cards, partitions, devices, and shared tokens.</p></div>${summaryBand()}</section>${diagnostics()}<section class="ecosystem-matrix" aria-label="Accelerator ecosystem matrix">${ecosystems.map(([vendor,model,primary,secondary,node], index) => `<article><header><span>${String(index + 1).padStart(2,'0')}</span><div><strong>${esc(vendor)}</strong><small>${esc(model)}</small></div>${node.startsWith('kasim') ? '<b class="badge kasim">Kasim</b>' : '<b class="badge external">Non-Kasim</b>'}</header><div class="matrix-value"><strong>${esc(primary.split(' ')[0])}</strong><span>${esc(primary.substring(primary.indexOf(' ') + 1))}</span></div><p>${esc(secondary)}</p><button data-node="${esc(node)}">${esc(node)} →</button></article>`).join('')}</section><section class="inventory-section compact"><div class="section-title"><div><p class="eyebrow">Evidence ledger</p><h2>${esc(t('signals'))}</h2></div>${filters()}</div>${ledgerTable()}</section></main>`
}

function switcher() {
  return `<nav class="prototype-switcher" aria-label="Prototype variants"><button id="previous" aria-label="Previous variant">←</button><span><small>Variant ${state.variant}</small><strong>${esc(variants[state.variant])}</strong></span><button id="next" aria-label="Next variant">→</button></nav>`
}

function detailDialog() {
  const item = inventory.find(entry => entry.id === state.detail)
  if (!item) return ''
  return `<dialog id="detail-dialog" open><form method="dialog"><header><div><span>${badge(item)}</span><h2>${esc(item.vendor)} · ${esc(item.model)}</h2></div><button id="close-detail" aria-label="${esc(t('close'))}">×</button></header><dl><dt>Node</dt><dd>${esc(item.node)}</dd><dt>Native signal</dt><dd><code>${esc(item.identity)}</code></dd><dt>Representation</dt><dd>${esc(item.representation)}</dd><dt>${esc(t('association'))}</dt><dd>${esc(item.association)}</dd><dt>${esc(t('source'))}</dt><dd>${esc(item.evidence)}</dd><dt>Truth boundary</dt><dd>${item.role === 'auxiliary' ? 'Scheduling signal only; no NIC, link, driver, CNI, or data-plane claim.' : item.representation === 'Extended resource' ? 'Scalar quantity only; no device ID is invented.' : 'Native DRA identity; runtime use remains unknown.'}</dd></dl></form></dialog>`
}

function render() {
  document.documentElement.lang = state.lang === 'zh' ? 'zh-CN' : 'en'
  app.innerHTML = `${header()}${state.variant === 'A' ? variantA() : state.variant === 'B' ? variantB() : variantC()}${switcher()}${detailDialog()}`
  document.querySelector('#mode').value = state.mode
  bind()
}

function cycle(delta) {
  const keys = Object.keys(variants)
  const current = keys.indexOf(state.variant)
  state.variant = keys[(current + delta + keys.length) % keys.length]
  state.detail = null
  setURL(); render()
}

function bind() {
  document.querySelector('#previous').addEventListener('click', () => cycle(-1))
  document.querySelector('#next').addEventListener('click', () => cycle(1))
  document.querySelector('#language').addEventListener('click', () => { state.lang = state.lang === 'en' ? 'zh' : 'en'; setURL(); render() })
  document.querySelector('#mode').addEventListener('change', event => { state.mode = event.target.value; setURL(); render() })
  document.querySelector('#search')?.addEventListener('input', event => { state.query = event.target.value; setURL(); render() })
  document.querySelectorAll('[data-origin]').forEach(button => button.addEventListener('click', () => { state.origin = button.dataset.origin; setURL(); render() }))
  document.querySelectorAll('[data-node]').forEach(button => button.addEventListener('click', () => { state.node = button.dataset.node; state.variant = 'B'; setURL(); render() }))
  document.querySelectorAll('[data-open]').forEach(button => button.addEventListener('click', event => { event.stopPropagation(); state.detail = button.dataset.open; render() }))
  document.querySelectorAll('tr[data-detail]').forEach(row => row.addEventListener('dblclick', () => { state.detail = row.dataset.detail; render() }))
  document.querySelector('#close-detail')?.addEventListener('click', event => { event.preventDefault(); state.detail = null; render() })
}

window.addEventListener('keydown', event => {
  if (['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName) || document.activeElement?.isContentEditable) return
  if (event.key === 'ArrowLeft') cycle(-1)
  if (event.key === 'ArrowRight') cycle(1)
  if (event.key === 'Escape' && state.detail) { state.detail = null; render() }
})

render()
