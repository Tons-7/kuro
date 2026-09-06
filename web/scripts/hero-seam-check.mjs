// The home page scrolled a little, so the sticky header sits over the hero:
// screenshots of the seam, for a look.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.SHOTS ?? '.'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const api = async (p, init) => {
  const res = await fetch(BASE + p, { ...init, headers: { 'content-type': 'application/json' } })
  let body
  try {
    body = await res.json()
  } catch {}
  return { ok: res.ok, body }
}
const post = (p, b) => api(p, { method: 'POST', body: JSON.stringify(b) })

await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  file = ((await api('/api/local/files')).body?.items ?? []).find((f) => String(f.path ?? '').includes('Show - 01'))
}
await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: 1 })
await post('/api/progress', { animeId: ANIME, episode: 1, position: 60, duration: 180, played: 60 })

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
await page.getByRole('region', { name: /featured/i }).first().waitFor({ timeout: 60000 }).catch(() => {})
await sleep(1500)

for (const y of [0, 40, 120]) {
  await page.evaluate((to) => window.scrollTo(0, to), y)
  await sleep(600)
  await page.screenshot({ path: `${SHOTS}/seam-${y}.png`, clip: { x: 0, y: 0, width: 1400, height: 160 } })
}
// Back and forward: two navigations in, back twice, forward once.
await page.getByRole('link', { name: 'Browse' }).click()
await page.getByRole('link', { name: 'Schedule' }).click()
await sleep(500)
await page.getByRole('button', { name: 'Back' }).click()
await sleep(400)
console.log(`after back: ${page.url()}`)
await page.getByRole('button', { name: 'Back' }).click()
await sleep(400)
console.log(`after back again: ${page.url()}`)
await page.getByRole('button', { name: 'Forward' }).click()
await sleep(400)
console.log(`after forward: ${page.url()}`)
await page.screenshot({ path: `${SHOTS}/header.png`, clip: { x: 0, y: 0, width: 700, height: 80 } })
console.log(`shots in ${SHOTS}`)
await browser.close()
