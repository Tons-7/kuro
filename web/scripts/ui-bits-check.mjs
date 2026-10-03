// Popular panel episode counts, the Recently released hover, and the
// "download ahead" setting.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

// Popular: "TV · 10/13 eps" while airing, "13 eps" once over.
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
const popular = page.locator('section', { has: page.getByRole('heading', { name: 'Popular' }) })
await popular.locator('ol li').first().waitFor({ timeout: 60000 })
const rows = await popular.locator('ol li').allInnerTexts()
check(rows.some((r) => /\d+ eps?\b/.test(r)), 'popular rows show episode counts', rows.slice(0, 3).join(' | ').replace(/\n/g, ' '))
check(!rows.some((r) => /\d+ ep\b(?!s)/.test(r) && !/1 ep\b/.test(r)), 'old "N ep" wording gone')
await popular.screenshot({ path: `${SHOTS}/popular-panel.png` })

// Recently released: the hover is the full panel.
await page.goto(`${BASE}/recent`, { waitUntil: 'domcontentloaded' })
const card = page.locator('a[href^="/watch/"]').first()
await card.waitFor({ timeout: 60000 })
await page.mouse.move(0, 0)
await card.hover({ position: { x: 20, y: 20 } })
const panel = page.locator('div.fixed.z-50.rounded-2xl')
const shown = await panel.waitFor({ timeout: 5000 }).then(() => true, () => false)
const text = shown ? await panel.innerText() : await page.locator('body').innerText()
check(shown, 'hover panel opens on a released card')
check(/\beps?\b/i.test(text) && /Play ep \d+/.test(text), 'it has the episode count and plays this episode', text.slice(0, 160).replace(/\n/g, ' | '))
const box = await panel.boundingBox().catch(() => null)
if (box) await page.screenshot({ path: `${SHOTS}/released-hover.png`, clip: box })

// Settings: Off / Next episode / Next 2 episodes, and it sticks.
await page.goto(`${BASE}/settings`, { waitUntil: 'domcontentloaded' })
const select = page.locator('select', { has: page.locator('option', { hasText: 'Next 2 episodes' }) })
await select.waitFor({ timeout: 30000 })
await select.selectOption('2')
await page.waitForTimeout(800)
await page.reload({ waitUntil: 'domcontentloaded' })
await select.waitFor({ timeout: 30000 })
check((await select.inputValue()) === '2', 'next 2 episodes is saved', await select.inputValue())
const prefs = await (await fetch(`${BASE}/api/prefs`)).json().catch(() => ({}))
const eff = prefs.effective ?? {}
check(eff['cache.prefetch_next'] === 'true' && eff['cache.prefetch_count'] === '2', 'both settings written', JSON.stringify([eff['cache.prefetch_next'], eff['cache.prefetch_count']]))
await select.selectOption('0')
await page.waitForTimeout(800)
const off = await (await fetch(`${BASE}/api/prefs`)).json().catch(() => ({}))
check(off.effective?.['cache.prefetch_next'] === 'false', 'off turns it off')
await select.scrollIntoViewIfNeeded()
await page.screenshot({ path: `${SHOTS}/download-ahead.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
