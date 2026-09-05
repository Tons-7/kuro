// The player's toggles must change the value they show. Reading the per-show
// value while writing the global one makes them disagree.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)

const api = async (path, init) => {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) },
  })
  let body
  try {
    body = await res.json()
  } catch {
    body = undefined
  }
  return { ok: res.ok, status: res.status, body }
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) })

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 8000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(150)
  }
  return false
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
  const files = await api('/api/local/files')
  file = (files.body?.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show'))
}
check(!!file, 'test file listed')
if (!file) process.exit(1)
await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: 1 })

// What the user set globally, in Settings.
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })
await post('/api/prefs', { key: 'playback.autonext', value: 'true' })
await post('/api/prefs', { key: 'playback.autoskip_op', value: 'false' })
// A per-show override, which only this show carries.
await post('/api/prefs', { key: 'playback.autoskip_op', value: 'true', animeId: ANIME })

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => page.getByRole('switch', { name: 'Auto play' }).isVisible(), 30_000), 'player controls rendered')

const state = async (name) =>
  page.getByRole('switch', { name }).getAttribute('aria-checked')

const shown = {
  'Auto play': await state('Auto play'),
  'Auto next': await state('Auto next'),
  'Skip opening': await state('Skip opening'),
}
console.log('   shown:', JSON.stringify(shown))

check(shown['Auto play'] === 'true', 'Auto play shows the setting the user turned on', shown['Auto play'])
check(shown['Auto next'] === 'true', 'Auto next shows the setting the user turned on', shown['Auto next'])
// What is in effect for this show, which is what the episode will actually do.
check(shown['Skip opening'] === 'true', 'Skip opening shows the override in effect', shown['Skip opening'])

// Clicking has to change what is shown. Writing the global while an override
// stands leaves the switch where it was, looking like nothing happened.
await page.getByRole('switch', { name: 'Skip opening' }).click()
check(
  await until(async () => (await state('Skip opening')) === 'false'),
  'clicking a switch backed by an override turns it off',
  await state('Skip opening'),
)
check(
  (await api(`/api/prefs${'?anime=' + ANIME}`)).body?.effective?.['playback.autoskip_op'] === 'false',
  'the override is what changed',
)
check(
  (await api('/api/prefs')).body?.effective?.['playback.autoskip_op'] === 'false',
  'the global setting was left alone',
)

// A key with no override still edits the global, so the player keeps setting
// the default for every other show.
await page.getByRole('switch', { name: 'Auto play' }).click()
check(
  await until(async () => (await state('Auto play')) === 'false'),
  'a switch with no override still flips',
)
check(
  (await api('/api/prefs')).body?.effective?.['playback.autoplay'] === 'false',
  'with no override the global setting is what changed',
)

await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
