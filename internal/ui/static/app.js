const app = document.querySelector('#app')
const fragment = new URLSearchParams(location.hash.slice(1))
const token = fragment.get('token') ?? ''

const copy = {
  en: {
    title: 'Cluster Simulation Inventory', live: 'Live', loading: 'Loading', partial: 'Partial', stale: 'Stale',
    nodes: 'Nodes', kasim: 'Kasim nodes', other: 'Non-Kasim nodes', scalar: 'Scalar signals', dra: 'Native DRA devices',
    signals: 'Device and signal inventory', nodeList: 'Node inventory', search: 'Search every visible identity',
    allOrigins: 'All origins', kasimOnly: 'Kasim only', otherOnly: 'Non-Kasim only', all: 'All',
    capacity: 'Capacity', allocatable: 'Allocatable', requested: 'Requested', allocation: 'Allocation', health: 'Health',
    evidence: 'Evidence', unknown: 'Unknown / not reported', details: 'Evidence details', close: 'Close',
    noResults: 'No matching inventory.', language: '中文', reconnecting: 'Reconnecting', diagnostics: 'Source diagnostics',
    noToken: 'The URL has no inventory capability. Restart kasim ui and use the printed URL.',
    origin: 'Origin', scenario: 'Scenario', vendor: 'Vendor', model: 'Model', role: 'Role', representation: 'Representation',
    sourceState: 'Source state', completeSource: 'Available sources', gapSource: 'Sources with gaps', known: 'Known', unknownOnly: 'Unknown',
    intro: 'Counts stay separate from identities. Unknown health stays unknown.', home: 'Home · exact signals',
    noOwnership: 'No Kasim ownership', signalsCount: 'signals', identity: 'Identity', category: 'Category', associations: 'Associated accelerator pools',
    attributes: 'Observed attributes', pool: 'Scenario pool', truth: 'Truth boundary',
    showing: 'Showing', of: 'of', rows: 'rows',
    scalarBoundary: 'Scalar scheduling quantity only; no device ID is invented.',
    draBoundary: 'Native DRA identity. Allocation phases are API-server evidence; runtime use remains unknown.',
    auxBoundary: 'Auxiliary capacity is a scheduling token. It does not prove a NIC, link, CNI, fabric, or data path.',
  },
  zh: {
    title: '集群模拟清单', live: '实时', loading: '加载中', partial: '部分可用', stale: '数据已过期',
    nodes: '节点', kasim: 'Kasim 节点', other: '非 Kasim 节点', scalar: '标量信号', dra: '原生 DRA 设备',
    signals: '设备与信号清单', nodeList: '节点清单', search: '搜索全部可见标识', allOrigins: '全部节点来源',
    kasimOnly: '仅 Kasim', otherOnly: '仅非 Kasim', all: '全部', capacity: '容量', allocatable: '可分配',
    requested: '已请求', allocation: '分配阶段', health: '健康度', evidence: '证据', unknown: '未知 / 未报告',
    details: '证据详情', close: '关闭', noResults: '没有匹配的清单项。', language: 'English', reconnecting: '正在重连',
    diagnostics: '数据源诊断', noToken: 'URL 缺少清单访问能力，请重新启动 kasim ui 并使用终端输出的 URL。',
    origin: '节点来源', scenario: '场景', vendor: '厂商', model: '型号', role: '信号角色', representation: '表示方式',
    sourceState: '数据源状态', completeSource: '可用数据源', gapSource: '有缺口的数据源', known: '已知', unknownOnly: '未知',
    intro: '数量与设备标识分开呈现；未上报的健康信息始终保持未知。', home: '首页 · 精确信号',
    noOwnership: '无 Kasim 所有权', signalsCount: '个信号', identity: '标识', category: '类别', associations: '关联的加速卡池',
    attributes: '观测属性', pool: '场景资源池', truth: '真实性边界',
    showing: '当前显示', of: '/', rows: '行',
    scalarBoundary: '仅表示标量调度数量，不会虚构单设备 ID。',
    draBoundary: '保留原生 DRA 标识。分配阶段仅来自 API Server 证据，运行时使用情况仍未知。',
    auxBoundary: '辅助容量只是调度令牌，不证明网卡、链路、CNI、网络或数据通路可用。',
  },
}

const defaults = {origin: 'all', scenario: 'all', vendor: 'all', model: 'all', role: 'all', representation: 'all', health: 'all', source: 'all'}
let state = {lang: 'en', query: '', ...defaults, snapshot: null, error: token ? '' : 'noToken', detail: null, focusDetail: null}

const esc = value => String(value ?? '').replace(/[&<>'"]/g, character => ({'&':'&amp;', '<':'&lt;', '>':'&gt;', "'":'&#39;', '"':'&quot;'})[character])
const t = key => copy[state.lang][key] ?? key
const known = factValue => factValue?.state === 'known'
const fact = (factValue, fallback = '—') => known(factValue) ? String(factValue.value ?? 0) : fallback

function readURL() {
  const params = new URLSearchParams(location.search)
  state.lang = params.get('lang') === 'zh' || (!params.has('lang') && navigator.language.startsWith('zh')) ? 'zh' : 'en'
  state.query = params.get('q') ?? ''
  for (const key of Object.keys(defaults)) state[key] = params.get(key) ?? defaults[key]
}

function setURL(mode = 'replace') {
  const next = new URLSearchParams()
  if (state.lang === 'zh') next.set('lang', 'zh')
  if (state.query) next.set('q', state.query)
  for (const [key, fallback] of Object.entries(defaults)) if (state[key] !== fallback) next.set(key, state[key])
  const method = mode === 'push' ? 'pushState' : 'replaceState'
  history[method](null, '', `${location.pathname}${next.size ? `?${next}` : ''}${location.hash}`)
}

function status(snapshot) {
  if (!snapshot) return ['loading', t('loading')]
  if (snapshot.freshness === 'stale') return ['stale', t('stale')]
  if (snapshot.freshness === 'reconnecting') return ['partial', t('reconnecting')]
  if (snapshot.completeness !== 'complete') return ['partial', t('partial')]
  return ['live', t('live')]
}

function sourceHasGap(name) {
  const source = (state.snapshot?.sources ?? []).find(item => item.name === name)
  return !source || source.availability !== 'available' || !['fresh', 'resyncing'].includes(source.freshness)
}

function allRows() {
  const rows = []
  for (const node of state.snapshot?.nodes ?? []) for (const signal of node.signals ?? []) rows.push({node, signal})
  return rows
}

function flattenedSignals() {
  const query = state.query.trim().toLowerCase()
  return allRows().filter(({node, signal}) => {
    if (state.origin === 'kasim' && node.ownership !== 'kasim') return false
    if (state.origin === 'non-kasim' && node.ownership === 'kasim') return false
    if (state.scenario !== 'all' && fact(node.scenario, '') !== state.scenario) return false
    if (state.vendor !== 'all' && fact(signal.vendor, '') !== state.vendor) return false
    if (state.model !== 'all' && fact(signal.model, '') !== state.model) return false
    if (state.role !== 'all' && signal.role !== state.role) return false
    if (state.representation !== 'all' && signal.representation !== state.representation) return false
    if (state.health === 'known' && !known(signal.health)) return false
    if (state.health === 'unknown' && known(signal.health)) return false
    if (state.source === 'available' && sourceHasGap(signal.source)) return false
    if (state.source === 'gap' && !sourceHasGap(signal.source)) return false
    if (!query) return true
    const device = signal.device ? `${signal.device.driver} ${signal.device.pool} ${signal.device.device}` : ''
    const attributes = Object.entries(signal.attributes ?? {}).flat().join(' ')
    return `${node.name} ${fact(node.scenario, '')} ${signal.resourceName ?? ''} ${device} ${signal.pool ?? ''} ${signal.category ?? ''} ${fact(signal.vendor, '')} ${fact(signal.model, '')} ${attributes}`.toLowerCase().includes(query)
  })
}

function signalIdentity(signal) {
  if (signal.device) return `${signal.device.driver} / ${signal.device.pool} / ${signal.device.device}`
  return signal.resourceName ?? '—'
}

function diagnostics(snapshot) {
  const gaps = (snapshot?.sources ?? []).filter(source => source.availability !== 'available' || source.freshness !== 'fresh')
  if (!gaps.length && !(snapshot?.diagnostics ?? []).length) return ''
  return `<aside class="diagnostic"><strong>${esc(t('diagnostics'))}</strong>${gaps.map(source =>
    `<span><code>${esc(source.name)}</code> ${esc(source.availability)} · ${esc(source.mode)} · ${esc(source.freshness)}</span>`
  ).join('')}${(snapshot?.diagnostics ?? []).map(item => `<span>${esc(item.code)}: ${esc(item.message)}</span>`).join('')}</aside>`
}

function uniqueOption(field, getter) {
  return [...new Set(allRows().map(getter).filter(Boolean))].sort().map(value => `<option value="${esc(value)}" ${state[field] === value ? 'selected' : ''}>${esc(value)}</option>`).join('')
}

function selectFilter(field, label, options) {
  return `<label><span>${esc(label)}</span><select data-filter="${esc(field)}"><option value="all">${esc(t('all'))}</option>${options}</select></label>`
}

function filters() {
  const fixed = (field, values) => values.map(value => `<option value="${esc(value)}" ${state[field] === value ? 'selected' : ''}>${esc(value)}</option>`).join('')
  return `<div class="filters"><label class="search"><span>${esc(t('search'))}</span><input id="search" value="${esc(state.query)}" placeholder="${esc(t('search'))}"></label>
    ${selectFilter('scenario', t('scenario'), uniqueOption('scenario', row => fact(row.node.scenario, '')))}
    ${selectFilter('vendor', t('vendor'), uniqueOption('vendor', row => fact(row.signal.vendor, '')))}
    ${selectFilter('model', t('model'), uniqueOption('model', row => fact(row.signal.model, '')))}
    ${selectFilter('role', t('role'), fixed('role', ['accelerator', 'auxiliary', 'unclassified']))}
    ${selectFilter('representation', t('representation'), fixed('representation', ['scalar-extended-resource', 'dra-device']))}
    ${selectFilter('health', t('health'), `<option value="known" ${state.health === 'known' ? 'selected' : ''}>${esc(t('known'))}</option><option value="unknown" ${state.health === 'unknown' ? 'selected' : ''}>${esc(t('unknownOnly'))}</option>`)}
    ${selectFilter('source', t('sourceState'), `<option value="available" ${state.source === 'available' ? 'selected' : ''}>${esc(t('completeSource'))}</option><option value="gap" ${state.source === 'gap' ? 'selected' : ''}>${esc(t('gapSource'))}</option>`)}
    <div class="segmented" aria-label="${esc(t('origin'))}"><button data-origin="all" class="${state.origin === 'all' ? 'active' : ''}">${esc(t('allOrigins'))}</button><button data-origin="kasim" class="${state.origin === 'kasim' ? 'active' : ''}">${esc(t('kasimOnly'))}</button><button data-origin="non-kasim" class="${state.origin === 'non-kasim' ? 'active' : ''}">${esc(t('otherOnly'))}</button></div>
  </div>`
}

function metric(label, value) {
  return `<div><span>${esc(label)}</span><strong>${Number.isInteger(value) ? value : '—'}</strong></div>`
}

function ledger(rows) {
  if (!rows.length) return `<p class="empty">${esc(t('noResults'))}</p>`
  const visible = rows.slice(0, 100)
  return `<div class="table-wrap"><table><caption class="sr-only">${esc(t('signals'))}</caption><thead><tr><th>Node / origin</th><th>${esc(t('vendor'))} / ${esc(t('model'))}</th><th>Signal / identity</th><th>${esc(t('capacity'))}</th><th>${esc(t('allocatable'))}</th><th>${esc(t('requested'))}</th><th>${esc(t('allocation'))}</th><th>${esc(t('health'))}</th><th>${esc(t('evidence'))}</th></tr></thead><tbody>${visible.map(({node, signal}, index) => `<tr tabindex="0" data-row="${index}">
    <td data-label="Node"><strong>${esc(node.name)}</strong><span class="badge ${esc(node.ownership)}">${node.ownership === 'kasim' ? 'Kasim' : 'Non-Kasim'}</span><span class="badge ${esc(signal.role)}">${esc(signal.role)}</span></td>
    <td data-label="${esc(t('vendor'))}"><strong>${esc(fact(signal.vendor, t('unknown')))}</strong><span>${esc(fact(signal.model, t('unknown')))}</span></td>
    <td data-label="Signal"><code>${esc(signalIdentity(signal))}</code><span>${esc(signal.representation)}${signal.category ? ` · ${esc(signal.category)}` : ''}</span></td>
    <td data-label="${esc(t('capacity'))}">${esc(fact(signal.capacity))}</td><td data-label="${esc(t('allocatable'))}">${esc(fact(signal.allocatable))}</td><td data-label="${esc(t('requested'))}">${esc(fact(signal.requested))}</td>
    <td data-label="${esc(t('allocation'))}">${esc(fact(signal.allocation))}</td><td data-label="${esc(t('health'))}" class="${known(signal.health) ? '' : 'unknown'}">${esc(fact(signal.health, t('unknown')))}</td>
    <td data-label="${esc(t('evidence'))}"><span>${esc(signal.source)}</span><button data-open="${index}" aria-label="${esc(`${t('details')}: ${node.name}, ${signalIdentity(signal)}`)}">${esc(t('details'))}</button></td>
  </tr>`).join('')}</tbody></table></div>${rows.length > visible.length ? `<p class="omitted">${esc(t('showing'))} ${visible.length} ${esc(t('of'))} ${rows.length} ${esc(t('rows'))}</p>` : ''}`
}

function detailDialog() {
  if (state.detail === null) return ''
  const row = flattenedSignals()[state.detail]
  if (!row) return ''
  const {node, signal} = row
  const attributes = Object.entries(signal.attributes ?? {})
  const boundary = signal.role === 'auxiliary' ? t('auxBoundary') : signal.representation === 'scalar-extended-resource' ? t('scalarBoundary') : t('draBoundary')
  return `<dialog open aria-modal="true"><form method="dialog"><header><div><span class="badge ${esc(node.ownership)}">${esc(node.ownership)}</span><h2>${esc(node.name)}</h2></div><button id="close-detail" aria-label="${esc(t('close'))}">×</button></header><dl>
    <dt>${esc(t('identity'))}</dt><dd><code>${esc(signalIdentity(signal))}</code></dd><dt>${esc(t('vendor'))} / ${esc(t('model'))}</dt><dd>${esc(fact(signal.vendor, t('unknown')))} · ${esc(fact(signal.model, t('unknown')))}</dd>
    <dt>${esc(t('representation'))} / ${esc(t('role'))}</dt><dd>${esc(signal.representation)} · ${esc(signal.role)}</dd>
    ${signal.pool ? `<dt>${esc(t('pool'))}</dt><dd>${esc(signal.pool)}</dd>` : ''}${signal.category ? `<dt>${esc(t('category'))}</dt><dd>${esc(signal.category)}</dd>` : ''}
    ${signal.associations?.length ? `<dt>${esc(t('associations'))}</dt><dd>${signal.associations.map(esc).join(', ')}</dd>` : ''}
    <dt>${esc(t('allocation'))}</dt><dd>${esc(fact(signal.allocation, t('unknown')))}</dd><dt>${esc(t('health'))}</dt><dd>${esc(fact(signal.health, t('unknown')))}</dd>
    ${attributes.length ? `<dt>${esc(t('attributes'))}</dt><dd>${attributes.map(([key, value]) => `<code>${esc(key)}=${esc(value)}</code>`).join('<br>')}</dd>` : ''}
    <dt>${esc(t('truth'))}</dt><dd>${esc(boundary)}</dd></dl></form></dialog>`
}

function render() {
  document.documentElement.lang = state.lang === 'zh' ? 'zh-CN' : 'en'
  if (state.error) { app.innerHTML = `<main id="main" class="error"><h1>Kasim</h1><p>${esc(t(state.error))}</p></main>`; return }
  const snapshot = state.snapshot
  const summary = snapshot?.summary ?? {}
  const [statusClass, statusText] = status(snapshot)
  const rows = flattenedSignals()
  app.innerHTML = `<header class="app-header"><div class="brand"><span aria-hidden="true">K</span><div><strong>Kasim</strong><small>${esc(t('title'))}</small></div></div>
    <div class="target"><strong>${esc(snapshot?.target?.contextName ?? t('loading'))}</strong><small>${esc(snapshot?.target?.kubernetesVersion ?? '')}</small></div>
    <div class="actions"><span class="status ${statusClass}" aria-live="polite"><i></i>${esc(statusText)}</span><button id="language">${esc(t('language'))}</button></div></header>
    <main id="main"><section class="intro"><div><p class="eyebrow">Evidence-first inventory</p><h1>${esc(t('title'))}</h1><p>${esc(t('intro'))}</p></div>${diagnostics(snapshot)}</section>
    <section class="summary" aria-label="Inventory summary">${metric(t('nodes'), summary.nodes)}${metric(t('kasim'), summary.kasimNodes)}${metric(t('other'), summary.nonKasimNodes)}${metric(t('scalar'), summary.scalarSignals)}${metric(t('dra'), summary.draDevices)}</section>
    <section class="inventory"><div class="section-title"><div><p class="eyebrow">${esc(t('home'))}</p><h2>${esc(t('signals'))}</h2></div></div>${filters()}${ledger(rows)}</section>
    <section class="nodes"><h2>${esc(t('nodeList'))}</h2>${(snapshot?.nodes ?? []).map(node => `<article><strong>${esc(node.name)}</strong><b class="${esc(node.ownership)}">${node.ownership === 'kasim' ? 'Kasim' : 'Non-Kasim'}</b><small>${esc(fact(node.scenario, t('noOwnership')))} · ${node.signals?.length ?? 0} ${esc(t('signalsCount'))}</small></article>`).join('')}</section></main>${detailDialog()}`
  bind()
}

function closeDetail() {
  const focus = state.focusDetail
  state.detail = null
  render()
  if (focus !== null) document.querySelector(focus)?.focus()
}

function openDetail(index, focusSelector) { state.detail = index; state.focusDetail = focusSelector; render(); document.querySelector('#close-detail')?.focus() }

function bind() {
  document.querySelector('#language')?.addEventListener('click', () => { state.lang = state.lang === 'en' ? 'zh' : 'en'; setURL('push'); render() })
  document.querySelector('#search')?.addEventListener('input', event => { state.query = event.target.value; setURL(); render() })
  document.querySelectorAll('[data-filter]').forEach(select => select.addEventListener('change', () => { state[select.dataset.filter] = select.value; setURL('push'); render() }))
  document.querySelectorAll('[data-origin]').forEach(button => button.addEventListener('click', () => { state.origin = button.dataset.origin; setURL('push'); render() }))
  document.querySelectorAll('[data-open]').forEach(button => button.addEventListener('click', event => { event.stopPropagation(); openDetail(Number(button.dataset.open), `[data-open="${button.dataset.open}"]`) }))
  document.querySelectorAll('[data-row]').forEach(row => row.addEventListener('keydown', event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetail(Number(row.dataset.row), `[data-row="${row.dataset.row}"]`) } }))
  document.querySelector('#close-detail')?.addEventListener('click', event => { event.preventDefault(); closeDetail() })
}

async function consumeStream() {
  const response = await fetch('/api/v1/stream', {headers: {Authorization: `Bearer ${token}`}, cache: 'no-store'})
  if (!response.ok || !response.body) throw new Error(`inventory stream: ${response.status}`)
  const reader = response.body.getReader(), decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const {value, done} = await reader.read()
    if (done) throw new Error('inventory stream ended')
    buffer += decoder.decode(value, {stream: true})
    const lines = buffer.split('\n'); buffer = lines.pop() ?? ''
    for (const line of lines) if (line) { state.snapshot = JSON.parse(line); render() }
  }
}

async function poll() {
  try {
    const response = await fetch('/api/v1/snapshot', {headers: {Authorization: `Bearer ${token}`}, cache: 'no-store'})
    if (!response.ok) throw new Error(`inventory snapshot: ${response.status}`)
    state.snapshot = await response.json(); render()
  } catch { /* retain the stale last-known view */ }
}

window.addEventListener('keydown', event => { if (event.key === 'Escape' && state.detail !== null) closeDetail() })
window.addEventListener('popstate', () => { readURL(); state.detail = null; render() })
readURL(); render()
if (token) consumeStream().catch(() => { poll(); setInterval(poll, 30000) })
