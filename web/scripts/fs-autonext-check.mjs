// Fullscreen with auto play and auto next both on: the episode should advance
// once, stay fullscreen, and keep playing without a click.
import { copyFileSync } from 'node:fs'
import { join } from 'node:path'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.MANUAL_SHOTS ?? '.'

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
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 10000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(150)
  }
  return false
}

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

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

// Episode 2 is where auto-next lands and it has to be playable.
copyFileSync(join(LIB, 'Kuro Test Show - 01.mkv'), join(LIB, 'second.mkv'))
await post('/api/local/scan', {})
let second
for (let i = 0; i < 30 && !second; i++) {
  await sleep(1000)
  second = (await api('/api/local/files')).body?.items?.find((f) => String(f.path ?? '').includes('second.mkv'))
}
check(!!second, 'second episode scanned')
if (!second) process.exit(1)
await post('/api/local/assign', { id: second.id, animeId: ANIME, episode: 2 })

// The combination under test.
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })
await post('/api/prefs', { key: 'playback.autonext', value: 'true' })
await post('/api/prefs', { key: 'playback.skip_filler', value: 'false' })

// STRICT=1 keeps the browser's real autoplay policy, which is what a viewer has.
const browser = await chromium.launch({
  args: process.env.STRICT ? [] : ['--autoplay-policy=no-user-gesture-required'],
})
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.split('\n')[0].slice(0, 160)))

const video = page.locator('video')
const fullscreen = () => page.evaluate(() => !!document.fullscreenElement)

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode 1 loads')
check(await until(() => video.evaluate((v) => !v.paused), 20_000), 'auto play started episode 1 by itself')

// A real key press, so the browser sees a genuine user gesture.
await page.mouse.move(640, 400)
await page.keyboard.press('f')
check(await until(fullscreen, 10_000), 'f entered fullscreen')

await page.evaluate(() => {
  document.querySelector('.group\\/player').dataset.mark = 'kept'
})

// Run the episode out.
await video.evaluate((v) => {
  v.currentTime = v.duration - 0.6
  void v.play()
})

const card = page.getByRole('dialog', { name: 'Up next' })
check(await until(() => card.isVisible(), 20_000), 'up-next card appears in fullscreen')
check(await fullscreen(), 'still fullscreen while the card counts down')
await page.screenshot({ path: `${SHOTS}/fs-countdown.png` })

check(await until(() => page.url().endsWith(`/watch/${ANIME}/2`), 25_000), 'auto-next advanced to episode 2', page.url())
await sleep(2500)

check(await fullscreen(), 'STILL FULLSCREEN on the next episode')
check(
  await page.evaluate(() => document.querySelector('.group\\/player')?.dataset.mark === 'kept'),
  'the player element was not remounted',
)
check(await until(() => video.evaluate((v) => !v.paused), 25_000), 'episode 2 is playing without a click',
  await video.evaluate((v) => `paused=${v.paused} t=${v.currentTime.toFixed(1)} ready=${v.readyState}`).catch(() => '?'))
check(await video.evaluate((v) => v.currentTime < 30), 'episode 2 started near the beginning',
  await video.evaluate((v) => `t=${v.currentTime.toFixed(1)}`))
await page.screenshot({ path: `${SHOTS}/fs-next-episode.png` })

// It must advance exactly one episode, not skip ahead.
await sleep(6000)
check(page.url().endsWith(`/watch/${ANIME}/2`), 'it did not run on past episode 2', page.url())
check(await fullscreen(), 'still fullscreen six seconds later')
check(errors.length === 0, 'no page errors', errors.join(' | '))

// An episode whose release cannot be resolved: the failure has to be shown
// over the player, not in place of it, or it takes fullscreen with it.
console.log('\n-- next episode with no resolvable release --')
if (!(await fullscreen())) {
  await page.mouse.move(640, 400)
  await page.keyboard.press('f')
  await until(fullscreen, 10_000)
}
check(await fullscreen(), 'fullscreen before the unresolvable episode')
await page.evaluate(() => {
  const el = document.querySelector('.group\\/player')
  if (el) el.dataset.mark = 'kept2'
})

// Client-side, the way auto-next moves: a full page load would drop fullscreen
// on its own and prove nothing.
await page.evaluate((target) => {
  window.history.pushState({}, '', target)
  window.dispatchEvent(new PopStateEvent('popstate'))
}, `/watch/${ANIME}/4242`)
check(await until(() => page.url().endsWith('/4242'), 10_000), 'navigated client-side', page.url())

// Wait for the search to give up: that is when the failure is shown.
const gaveUp = await until(
  () => page.getByText(/Nothing found|No release|couldn't find|not found|no release/i).first().isVisible().catch(() => false),
  150_000,
)
check(gaveUp, 'the search reported failure')
check(await fullscreen(), 'STILL FULLSCREEN after the episode failed to resolve')
check(
  await page.evaluate(() => document.querySelector('.group\\/player')?.dataset.mark === 'kept2'),
  'the player element survived the failure',
)
check(
  await page.getByRole('button', { name: 'Choose a release' }).isVisible(),
  'the ways out are reachable from inside the player',
)
await page.screenshot({ path: `${SHOTS}/fs-no-release.png` })

await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
