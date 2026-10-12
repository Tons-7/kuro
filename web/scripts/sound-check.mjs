// The Sound menu under the player: voice boost (also with V), night mode, headphones, mono, volume boost.
// Each change restarts the stream where it was, playback carries on, and the choice is kept for the next open.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = process.env.KURO_ANIME ?? '127230'
const LIB = process.env.KURO_LIB
const SHOTS = process.env.SHOTS ?? '.'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 30_000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(200)
  }
  return false
}
const api = async (path, init) => {
  const res = await fetch(BASE + path, { ...init, headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) } })
  return { status: res.status, body: await res.json().catch(() => null) }
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) })
const pref = async (key) => (await api('/api/prefs')).body?.effective?.[key]

// The harness's test file as episode 1.
await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  file = ((await api('/api/local/files')).body?.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show - 01'))
}
check(!!file, 'the test file is in the library')
await post('/api/local/assign', { id: file.id, animeId: Number(ANIME), episode: 1 })
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
const posts = []
page.on('request', (r) => {
  const m = r.method() === 'POST' && r.url().match(/\/api\/stream\/[^/]+\/sound\?(.*)$/)
  if (m) posts.push(decodeURIComponent(m[1]))
})

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
const video = page.locator('video')
const playing = () => video.evaluate((v) => v.readyState >= 3 && v.currentTime > 1 && !v.paused).catch(() => false)
const carriesOn = async (label) => {
  const from = await video.evaluate((v) => v.currentTime).catch(() => 0)
  check(await until(() => video.evaluate((v, t) => v.readyState >= 3 && v.currentTime > t + 1 && !v.paused, from), 60_000), label)
}
check(await until(playing, 60_000), 'the episode plays')

const button = page.getByRole('button', { name: /^Sound: / })
check(await button.isVisible(), 'the player offers the sound menu', await button.getAttribute('aria-label'))
const menu = page.getByRole('menu')
const row = (name, which = 'first') => menu.getByRole('menuitemradio', { name })[which]()
// The menu stays open after a choice, so several switches can be flipped in a row.
const openMenu = async () => {
  if ((await button.getAttribute('aria-expanded')) !== 'true') await button.click()
}

await openMenu()
await row('Medium').click()
check(await until(() => posts.at(-1) === 'voice=medium'), 'Medium asks the server for the lift', posts.join(' | '))
check(await until(() => button.getAttribute('aria-label').then((l) => l === 'Sound: Voice medium')), 'the button says what is on')
await carriesOn('playback carries on after the switch')

await openMenu()
await row('Night mode').click()
check(await until(() => posts.at(-1) === 'night=true'), 'night mode is one switch', posts.at(-1))
await openMenu()
await row('Mono').click()
check(await until(() => posts.at(-1) === 'mono=true'), 'mono is one switch', posts.at(-1))
await openMenu()
await row('High', 'last').click()
check(await until(() => posts.at(-1) === 'boost=high'), 'the volume boost is its own switch', posts.at(-1))
check(await until(() => button.getAttribute('aria-label').then((l) => l === 'Sound: Voice medium, Night, Mono, Boost high')), 'the button lists everything on')
await carriesOn('playback carries on with everything on')
await page.screenshot({ path: `${SHOTS}/sound-menu.png` })

// V cycles the voice alone; keys are the player's only once the menu is closed.
await page.keyboard.press('Escape')
await page.locator('video').click({ position: { x: 20, y: 20 }, force: true })
await page.keyboard.press('v')
check(await until(() => posts.at(-1) === 'voice=high'), 'V steps the voice up to High', posts.at(-1))
check(await until(async () => (await pref('playback.voice')) === 'high' && (await pref('playback.mono')) === 'true' && (await pref('playback.boost')) === 'high'), 'every choice is remembered')
await page.keyboard.press('v')
check(await until(() => posts.at(-1) === 'voice=off'), 'V rounds the voice back to Off', posts.at(-1))

// A fresh open honours what was remembered.
await page.reload({ waitUntil: 'domcontentloaded' })
check(await until(playing, 60_000), 'the episode plays again after a reload')
check(await until(() => button.getAttribute('aria-label').then((l) => l === 'Sound: Night, Mono, Boost high')), 'the remembered choices are in effect on open', await button.getAttribute('aria-label'))

// Back to defaults for the next check.
for (const [key, value] of [['playback.night', 'false'], ['playback.mono', 'false'], ['playback.boost', 'off']]) await post('/api/prefs', { key, value })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
