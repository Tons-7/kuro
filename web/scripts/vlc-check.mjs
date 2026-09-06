// VLC as the default player, driven through its HTTP interface: progress is
// saved, Stop keeps the resume point, the end of an episode marks it watched
// and auto-next opens the following one in a new VLC.
import { execSync } from 'node:child_process'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 10000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(500)
  }
  return false
}
const api = async (p, init) => {
  const res = await fetch(BASE + p, { ...init, headers: { 'content-type': 'application/json' } })
  let body
  try {
    body = await res.json()
  } catch {}
  return { ok: res.ok, body }
}
const post = (p, b) => api(p, { method: 'POST', body: JSON.stringify(b) })
const vlcPids = () => {
  try {
    return execSync('tasklist /FI "IMAGENAME eq vlc.exe" /FO CSV /NH', { encoding: 'utf8' })
      .split('\n')
      .filter((l) => l.toLowerCase().includes('vlc.exe'))
      .map((l) => l.split('","')[1])
  } catch {
    return []
  }
}
const vlcRunning = () => vlcPids().length > 0
const episode = async (n) => ((await api(`/api/episodes?id=${ANIME}`)).body?.items ?? []).find((e) => e.number === n)
const resume = async (n) => (await post('/api/play', { animeId: ANIME, episode: n, external: false })).body?.startAt ?? -1

await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let files = []
for (let i = 0; i < 30 && files.length < 2; i++) {
  await sleep(1000)
  files = ((await api('/api/local/files')).body?.items ?? []).filter((f) => /Kuro Test Show - 0[13]/.test(String(f.path ?? '')))
}
check(files.length === 2, 'two local test episodes listed')
if (files.length < 2) process.exit(1)
// Files 01 and 03 become episodes 1 and 2 so auto-next has somewhere to go.
for (const f of files) {
  await post('/api/local/assign', { id: f.id, animeId: ANIME, episode: f.path.includes('01') ? 1 : 2 })
}

check(!!(await api('/api/setup')).body?.vlc, 'VLC was found on this machine')
check(!vlcRunning(), 'no VLC running before the test')
await post('/api/prefs', { key: 'playback.player', value: 'vlc' })
await post('/api/prefs', { key: 'playback.autonext', value: 'true' })

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

// Play from the start, let it run, then Stop. The test episode's opening
// chapter ends at 40 s, so the saved position proves both tracking and the
// auto-skip seek through VLC.
await post('/api/progress', { animeId: ANIME, episode: 1, position: 0, duration: 180, played: 0 })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => page.getByText('Playing in VLC').isVisible().catch(() => false), 30000), 'the page says it is playing in VLC')
check(await until(vlcRunning, 15000), 'a VLC process is running')
await sleep(14000)
await page.getByRole('button', { name: /stop/i }).click()
check(await until(() => !vlcRunning(), 15000), 'Stop ends VLC')
await sleep(1500)
const saved = await resume(1)
check(saved >= 40 && saved <= 60, 'the position VLC reached, past the skipped opening, was saved', String(saved))
check(!(await episode(1))?.watched, 'a stopped episode is not watched')

// Resume near the end: VLC reaches the end, the episode is watched, and
// auto-next opens episode 2 in a new VLC.
await post('/api/progress', { animeId: ANIME, episode: 1, position: 150, duration: 180, played: 10 })
check(!(await episode(1))?.watched, 'episode 1 is not watched before the ending plays')
const play = (await post('/api/play', { animeId: ANIME, episode: 1, external: true })).body ?? {}
check(play.player === 'vlc' && Math.round(play.startAt) === 150, 'play resumes in VLC at the saved point', JSON.stringify({ player: play.player, startAt: play.startAt }))
check(await until(vlcRunning, 15000), 'VLC opened for the ending')
const ending = new Set(vlcPids())
check(await until(async () => (await episode(1))?.watched === true, 60000), 'reaching the end marks the episode watched')
check(await until(() => vlcPids().some((p) => !ending.has(p)) && vlcPids().length === 1, 30000), 'auto-next opened episode 2 in a new VLC')
await sleep(8000)
const two = await episode(2)
check((two?.position ?? 0) > 3, 'episode 2 is being tracked', String(two?.position))
await post('/api/stop', { animeId: ANIME, episode: 2 })
check(await until(() => !vlcRunning(), 15000), 'API stop ends VLC')

// Back to the browser player.
await post('/api/prefs', { key: 'playback.player', value: 'browser' })
await page.goto(`${BASE}/watch/${ANIME}/2`, { waitUntil: 'domcontentloaded' })
check(await until(() => page.locator('video').evaluate((v) => v.readyState >= 1).catch(() => false), 60000), 'browser playback works again')
check(!vlcRunning(), 'VLC was not launched for the browser player')
check(errors.length === 0, 'no page errors', errors.join(' | '))

await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
