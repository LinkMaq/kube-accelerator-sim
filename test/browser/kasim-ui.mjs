import {spawn} from 'node:child_process'
import {mkdir} from 'node:fs/promises'
import net from 'node:net'
import process from 'node:process'
import {chromium} from 'playwright'

const assert = (condition, message) => {
  if (!condition) throw new Error(message)
}

const freePort = () => new Promise((resolve, reject) => {
  const server = net.createServer()
  server.once('error', reject)
  server.listen(0, '127.0.0.1', () => {
    const {port} = server.address()
    server.close(error => error ? reject(error) : resolve(port))
  })
})

async function startFixture(port) {
  const fixtureBinary = process.env.KASIM_UI_FIXTURE_BIN
  const command = fixtureBinary || 'go'
  const args = fixtureBinary
    ? [`--port=${port}`]
    : ['run', './internal/tools/uifixture', `--port=${port}`]
  const child = spawn(command, args, {
    cwd: process.cwd(),
    env: process.env,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  let stdout = '', stderr = ''
  child.stdout.on('data', chunk => { stdout += chunk })
  child.stderr.on('data', chunk => { stderr += chunk })
  const accessURL = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`fixture startup timed out: ${stderr}`)), 30_000)
    const inspect = () => {
      const match = stdout.match(/http:\/\/127\.0\.0\.1:\d+\/#token=[A-Za-z0-9_-]+/)
      if (!match) return
      clearTimeout(timeout)
      resolve(match[0])
    }
    child.stdout.on('data', inspect)
    child.once('exit', code => {
      clearTimeout(timeout)
      reject(new Error(`fixture exited ${code}: ${stderr}`))
    })
  })
  return {
    accessURL,
    stop: async () => {
      if (child.exitCode !== null) return
      child.kill('SIGTERM')
      await new Promise(resolve => {
        const timeout = setTimeout(() => { child.kill('SIGKILL'); resolve() }, 7_000)
        child.once('exit', () => { clearTimeout(timeout); resolve() })
      })
    },
  }
}

const port = await freePort()
const fixture = await startFixture(port)
const browser = await chromium.launch({headless: true})
await mkdir('test-results/kasim-ui', {recursive: true})

try {
  const page = await browser.newPage({viewport: {width: 1440, height: 900}})
  const requests = [], consoleErrors = []
  page.on('request', request => requests.push(request.url()))
  page.on('console', message => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })
  await page.goto(fixture.accessURL, {waitUntil: 'domcontentloaded'})
  await page.waitForFunction(() => document.querySelectorAll('tbody tr').length === 100)

  assert(await page.locator('html').getAttribute('lang') === 'en', 'desktop locale is not English')
  assert(await page.locator('.summary strong').first().textContent() === '1001', '1,001-Node summary is missing')
  assert(await page.locator('tbody tr').count() === 100, 'visible ledger is not bounded to 100 rows')
  assert(await page.locator('.omitted').textContent().then(text => text.includes('1003')), 'omitted row count is missing')
  const pageText = await page.locator('body').innerText()
  for (const required of ['NVIDIA H100', 'AMD Instinct MI300X', 'Ascend 910B', 'rdma/rdma_shared_device_a', 'gpu.nvidia.com / dra-pool / gpu-0', 'Unknown / not reported', 'real-control-plane']) {
    assert(pageText.includes(required), `desktop evidence is missing ${required}`)
  }
  assert(pageText.includes('Partial'), 'partial source status is not visible')
  const origin = new URL(fixture.accessURL).origin
  const token = new URL(fixture.accessURL).hash.slice('#token='.length)
  assert(requests.every(url => new URL(url).origin === origin), `cross-origin request observed: ${requests.join(', ')}`)
  assert(requests.every(url => !url.includes(token)), 'capability leaked into an HTTP request URL')

  await page.screenshot({path: 'test-results/kasim-ui/evidence-first-desktop.png'})

  await page.locator('[data-origin="kasim"]').click()
  await page.waitForFunction(() => new URL(location.href).searchParams.get('origin') === 'kasim')
  await page.locator('[data-origin="non-kasim"]').click()
  await page.waitForFunction(() => document.querySelectorAll('tbody tr').length === 1)
  assert((await page.locator('tbody').innerText()).includes('real-control-plane'), 'Non-Kasim filter lost the real Node')
  await page.evaluate(() => history.back())
  await page.waitForFunction(() => new URL(location.href).searchParams.get('origin') === 'kasim')
  assert(await page.locator('tbody tr').count() === 100, 'back navigation did not restore Kasim filter')

  await page.locator('[data-origin="all"]').click()
  await page.locator('#search').fill('kasim-0999')
  await page.waitForFunction(() => document.querySelectorAll('tbody tr').length === 1)
  const filteredURL = new URL(page.url())
  assert(filteredURL.searchParams.get('q') === 'kasim-0999', 'search is not URL-backed')
  assert(filteredURL.hash === new URL(fixture.accessURL).hash, 'filtering changed the capability fragment')
  await page.locator('#search').fill('')
  await page.waitForFunction(() => document.querySelectorAll('tbody tr').length === 100)

  const firstRow = page.locator('[data-row="0"]')
  await firstRow.focus()
  await page.keyboard.press('Enter')
  await page.locator('dialog').waitFor({state: 'visible'})
  assert(await page.locator('#close-detail').evaluate(element => element === document.activeElement), 'detail did not receive focus')
  await page.screenshot({path: 'test-results/kasim-ui/detail-drawer.png'})
  await page.keyboard.press('Escape')
  await page.locator('dialog').waitFor({state: 'detached'})
  assert(await firstRow.evaluate(element => element === document.activeElement), 'focus did not return to the keyboard trigger')

  await page.locator('#language').click()
  await page.waitForFunction(() => document.documentElement.lang === 'zh-CN')
  assert((await page.locator('h1').textContent()).includes('集群模拟清单'), 'Chinese locale did not render')
  await page.setViewportSize({width: 390, height: 844})
  const inventoryBox = await page.locator('.inventory').boundingBox()
  const summaryBox = await page.locator('.summary').boundingBox()
  assert(inventoryBox && summaryBox && inventoryBox.y < summaryBox.y, 'mobile ledger is not before summary')
  assert(await page.locator('tbody tr').count() === 100, 'mobile ledger exceeded its DOM bound')
  await page.screenshot({path: 'test-results/kasim-ui/chinese-partial-mobile.png'})

  const noScript = await browser.newContext({javaScriptEnabled: false, viewport: {width: 390, height: 844}})
  const noScriptPage = await noScript.newPage()
  await noScriptPage.goto(fixture.accessURL, {waitUntil: 'domcontentloaded'})
  assert((await noScriptPage.locator('noscript').innerText()).includes('requires JavaScript'), 'no-JavaScript fallback is missing')
  await noScript.close()

  assert(consoleErrors.length === 0, `browser console errors: ${consoleErrors.join('; ')}`)
  process.stdout.write(`kasim ui browser contract: PASS (${requests.length} same-origin requests)\n`)
} finally {
  await browser.close()
  await fixture.stop()
}
