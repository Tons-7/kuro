// Settings → About shows where things go and lets the data folder be moved.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'
let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
await page.goto(`${BASE}/settings?tab=About`, { waitUntil: 'domcontentloaded' })
await page.getByRole('tab', { name: 'About' }).click().catch(() => {})
const heading = page.getByRole('heading', { name: 'Where things go' })
check(await heading.isVisible({ timeout: 15000 }).catch(() => false), 'About lists where things go')
check(await page.getByText('History and settings', { exact: true }).first().isVisible().catch(() => false), 'the data folder row is shown')
const setup = await (await fetch(`${BASE}/api/setup`)).json()
for (const [label, key, path] of [
  ['History and settings', 'data_dir', setup.dataDir],
  ['Episode cache', 'cache_dir', setup.cacheDir],
  ['Programs', 'bin_dir', setup.binDir],
]) {
  const row = page.locator('dt', { hasText: label }).locator('..')
  check(await row.getByText(key, { exact: true }).isVisible().catch(() => false), `${label} names its ${key} setting`)
  check(await row.getByText(path).isVisible().catch(() => false), `${label} shows its folder`, path)
}
check(await page.getByText(setup.configPath).isVisible().catch(() => false), 'the config file is named')
await page.getByRole('button', { name: /somewhere else/i }).click()
await page.getByRole('button', { name: 'data', exact: true }).click()
check((await page.getByPlaceholder(/data/i).inputValue()) === 'data', 'the data shortcut fills the field')
await heading.scrollIntoViewIfNeeded()
await page.screenshot({ path: `${SHOTS}/about-folders.png` })
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
