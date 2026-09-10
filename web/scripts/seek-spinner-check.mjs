// A short arrow-key seek is instant, so it must not flash the buffering
// spinner — least of all while paused, where the frame just sat there anyway.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.MANUAL_SHOTS ?? process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 20000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return true
    await sleep(150)
  }
  return false
}
const api = async (p, init) => {
  const res = await fetch(BASE + p, {
    ...init,
    headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) },
  })
  let body
  try {
    body = await res.json()
  } catch {}
  return { ok: res.ok, body }
}
const post = (p, b) => api(p, { method: 'POST', body: JSON.stringify(b) })

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
check(!!file, 'test file listed')
if (!file) process.exit(1)
await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: 1 })
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.split('\n')[0].slice(0, 160)))

const video = page.locator('video')
const spinner = page.locator('.group\\/player .animate-spin')
// Sampled fast: the old bug was a flash, not a lasting spinner.
const spinnerDuring = async (ms) => {
  const end = Date.now() + ms
  let seen = false
  while (Date.now() < end) {
    if (await spinner.first().isVisible().catch(() => false)) seen = true
    await sleep(40)
  }
  return seen
}

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode loads')
check(await until(() => video.evaluate((v) => !v.paused), 20_000), 'it starts playing')

// Far enough in that a ten-second step back stays inside what is buffered.
await video.evaluate((v) => {
  v.currentTime = 40
})
check(await until(() => video.evaluate((v) => v.currentTime > 39 && v.readyState >= 3), 30_000),
  'buffered and playing well past the start')
check(!(await spinnerDuring(700)), 'no spinner sitting there before the seek')

await page.mouse.move(640, 400)
await page.keyboard.press('k')
check(await until(() => video.evaluate((v) => v.paused), 5000), 'k paused it')
const before = await video.evaluate((v) => v.currentTime)

await page.keyboard.press('ArrowLeft')
const flashed = await spinnerDuring(1200)
check(!flashed, 'a paused arrow-key seek shows no spinner')

const after = await video.evaluate((v) => v.currentTime)
check(after < before - 3, 'and it actually moved back', `${before.toFixed(1)} → ${after.toFixed(1)}`)
check(await video.evaluate((v) => v.paused), 'and it stayed paused')
await page.screenshot({ path: `${SHOTS}/paused-seek.png` })

// Playing, the same seek is just as quiet.
await page.keyboard.press('k')
check(await until(() => video.evaluate((v) => !v.paused), 5000), 'k resumed it')
await page.keyboard.press('ArrowRight')
check(!(await spinnerDuring(1200)), 'a short seek while playing shows no spinner either')
check(await until(() => video.evaluate((v) => !v.paused), 5000), 'still playing after the seek')

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
