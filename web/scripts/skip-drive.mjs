// Drives the real player to see the opening and ending markers.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.MANUAL_SHOTS ?? '.'

const api = async (p, init) => {
  const res = await fetch(BASE + p, { ...init, headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) } })
  let body
  try {
    body = await res.json()
  } catch {}
  return { ok: res.ok, body }
}
const post = (p, b) => api(p, { method: 'POST', body: JSON.stringify(b) })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 10000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(150)
  }
  return false
}
const say = (l, v) => console.log(`  ${l}: ${v}`)

for (let i = 0; i < 60; i++) {
  try {
    if ((await api('/api/setup')).ok) break
  } catch {}
  await sleep(1000)
}
await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  file = (await api('/api/local/files')).body?.items?.find((f) => String(f.path ?? '').includes('Kuro Test Show'))
}
await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: 1 })
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
await until(() => page.locator('video').evaluate((v) => v.readyState >= 1), 60_000)

const dur = Math.round(await page.locator('video').evaluate((v) => v.duration))
say('episode length ffprobe reported', `${dur}s`)
const marks = await api(`/api/episode/skips?anime=${ANIME}&episode=1&duration=${dur}`)
say('source', marks.body?.source)
say('ranges', JSON.stringify(marks.body?.ranges))

// The markers a viewer actually sees on the bar.
await page.mouse.move(400, 300)
await until(() => page.locator('div.group\\/scrub div[title^="Opening"]').first().isVisible(), 20_000)
say('opening marker', await page.locator('div.group\\/scrub div[title^="Opening"]').first().getAttribute('title'))
say('ending marker', await page.locator('div.group\\/scrub div[title^="Ending"]').first().getAttribute('title').catch(() => 'absent'))
await page.locator('.group\\/player').screenshot({ path: `${SHOTS}/skip-markers.png` })

// Sitting inside the opening offers the button, and it jumps past it.
await page.locator('video').evaluate((v) => {
  v.currentTime = 20
  void v.play()
})
const button = page.getByRole('button', { name: /Skip opening/i })
say('skip button appears inside the opening', await until(() => button.isVisible(), 15_000))
await page.locator('.group\\/player').screenshot({ path: `${SHOTS}/skip-button.png` })
await button.click()
say('after clicking, position', `${Math.round(await page.locator('video').evaluate((v) => v.currentTime))}s (opening ended at 40)`)

// Auto-skip: with the preference on, no click needed.
await post('/api/prefs', { key: 'playback.autoskip_op', value: 'true' })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
await until(() => page.locator('video').evaluate((v) => v.readyState >= 1), 60_000)
await page.locator('video').evaluate((v) => {
  v.currentTime = 15
  void v.play()
})
say('auto-skip jumped the opening', await until(() => page.locator('video').evaluate((v) => v.currentTime >= 40), 20_000))

console.log(`\npage errors: ${errors.length ? errors.join(' | ') : 'none'}`)
await browser.close()
