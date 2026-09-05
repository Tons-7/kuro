// End-to-end check of session keepalive and recovery: the playlist is pinged
// while playing, a reaped session is noticed on tab return or play and
// reopened in place, and a frozen clock with data ahead gets poked — the
// alt-tab bugs that used to need a hard refresh.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const EP = 1
const shots = process.env.SHOTS ?? '.'

const api = async (path, init) => {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) },
  })
  const text = await res.text()
  let body
  try {
    body = JSON.parse(text)
  } catch {
    body = text
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

// ---------------------------------------------------------------- setup
for (let i = 0; i < 60; i++) {
  try {
    const r = await api('/api/setup')
    if (r.ok) break
  } catch {}
  await sleep(1000)
}

const paths = await post('/api/local/paths', { paths: [LIB] })
check(paths.ok, 'library path set', JSON.stringify(paths.body).slice(0, 120))
await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  const files = await api('/api/local/files')
  file = (files.body.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show'))
}
check(!!file, 'test file listed')
if (!file) process.exit(1)
const assign = await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: EP })
check(assign.ok, 'file assigned to episode', JSON.stringify(assign.body))
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('console', (m) => {
  // The deliberate 404s of a killed session log as resource errors; those are
  // the point of the check, not a failure.
  if (m.type() === 'error' && !/Failed to load resource|404/.test(m.text())) {
    errors.push(m.text().slice(0, 200))
  }
})
page.on('pageerror', (e) => errors.push(`uncaught: ${e.message.slice(0, 200)}`))

let playlistFetches = 0
let opens = 0
let streamOpen = null
// Every stream response, so a failure can say what the player was actually fed.
const streamHits = []
page.on('request', (r) => {
  if (r.url().includes('/playlist.m3u8')) playlistFetches++
  if (r.url().includes('/api/stream/open')) opens++
})
page.on('response', (r) => {
  const u = r.url()
  if (u.includes('/api/stream/')) {
    streamHits.push(`${r.status()} ${u.slice(u.indexOf('/api/stream/') + 12)}`)
  }
  if (u.includes('/api/stream/open') && r.ok()) {
    r.json().then((b) => { streamOpen = b }).catch(() => {})
  }
})

await page.goto(`${BASE}/watch/${ANIME}/${EP}`, { waitUntil: 'domcontentloaded' })

const state = () =>
  page.evaluate(() => {
    const v = document.querySelector('video')
    if (!v) return null
    const q = v.getVideoPlaybackQuality?.()
    return {
      t: Number(v.currentTime.toFixed(2)),
      paused: v.paused,
      ready: v.readyState,
      frames: q?.totalVideoFrames ?? -1,
      error: v.error ? `${v.error.code}: ${v.error.message}` : null,
    }
  })

let s = null
for (let i = 0; i < 45; i++) {
  await sleep(1000)
  s = await state()
  if (s && s.t > 2 && !s.paused) break
  if (s && s.paused && s.ready >= 3 && i > 3) {
    await page.evaluate(() => document.querySelector('video')?.play().catch(() => {}))
  }
}
check(!!s && s.t > 2 && !s.paused, 'playback started', JSON.stringify(s))
check(!!streamOpen?.id, 'stream session opened', streamOpen?.id ?? '')
const sid = streamOpen.id

// ---------------------------------------------------------------- keepalive
// The interval pings every 45s while playing; hls.js itself never re-reads a
// VOD playlist, so growth here is the keepalive.
await sleep(2000)
const before = playlistFetches
await sleep(50_000)
check(playlistFetches > before, 'playlist pinged while playing', `${before} → ${playlistFetches}`)
let alive = await api(`/api/stream/${sid}/playlist.m3u8`)
check(alive.ok, 'session still alive after the keepalive window', String(alive.status))

// ---------------------------------------------------------------- tab return
// Kill the session the way the reaper does, then come back to the tab: the
// visibility ping must notice and reopen without touching the player.
const preKill = await state()
const opensBefore = opens
await api(`/api/stream/${sid}`, { method: 'DELETE' })
alive = await api(`/api/stream/${sid}/playlist.m3u8`)
check(alive.status === 404, 'session killed for the check', String(alive.status))

await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
let reopened = false
for (let i = 0; i < 20 && !reopened; i++) {
  await sleep(1000)
  reopened = opens > opensBefore
}
check(reopened, 'tab return reopens the lost session', `opens ${opensBefore} → ${opens}`)
alive = await api(`/api/stream/${sid}/playlist.m3u8`)
check(alive.ok, 'session exists again', String(alive.status))
// Near where it was, not merely moving: a rebuild that restarts from zero
// also "advances".
for (let i = 0; i < 15; i++) {
  await sleep(1000)
  s = await state()
  if (s.t > preKill.t && !s.paused) break
}
check(
  s.t > preKill.t && s.t - preKill.t < 25 && !s.paused && s.error === null,
  'playback carried on near the lost position',
  `${JSON.stringify(s)} preKill=${preKill.t}`,
)

// A seek into unproduced segments proves the rebuilt session really serves.
await page.evaluate(() => {
  const v = document.querySelector('video')
  v.currentTime = 120
  void v.play()
})
let sought = null
for (let i = 0; i < 40; i++) {
  await sleep(1000)
  sought = await state()
  if (sought.t > 121 && !sought.paused && sought.ready >= 3) break
}
check(sought.t > 121 && sought.error === null, 'rebuilt session serves a seek', JSON.stringify(sought))
await page.screenshot({ path: `${shots}/recovery-after-reopen.png` })

// ---------------------------------------------------------------- unpause
// Paused past the reaper: the play press itself must notice and reopen.
await page.evaluate(() => document.querySelector('video')?.pause())
await sleep(1000)
const pausedAt = await state()
const opensPaused = opens
await api(`/api/stream/${sid}`, { method: 'DELETE' })
await sleep(1000)
await page.evaluate(() => document.querySelector('video')?.play().catch(() => {}))
reopened = false
for (let i = 0; i < 20 && !reopened; i++) {
  await sleep(1000)
  reopened = opens > opensPaused
}
check(reopened, 'pressing play reopens the lost session', `opens ${opensPaused} → ${opens}`)
for (let i = 0; i < 15; i++) {
  await sleep(1000)
  s = await state()
  if (s.t > pausedAt.t && !s.paused) break
}
check(
  s.t > pausedAt.t - 10 && s.t < pausedAt.t + 30 && s.error === null,
  'playback resumed near the paused position',
  `${JSON.stringify(s)} pausedAt=${pausedAt.t} hits: ${streamHits.slice(-15).join(' ')}`,
)

// ---------------------------------------------------------------- watchdog
// Back to the middle so the episode cannot end mid-check.
await page.evaluate(() => {
  const v = document.querySelector('video')
  v.currentTime = 60
  void v.play()
})
for (let i = 0; i < 30; i++) {
  await sleep(1000)
  s = await state()
  if (s.t > 61 && !s.paused && s.ready >= 3) break
}
check(s.t > 61 && !s.paused, 'playing again before the watchdog check', JSON.stringify(s))

// Freeze what the clock reports while the video really plays on: the watchdog
// must poke it (the pause/unpause the user used to do by hand).
const poked = await page.evaluate(async () => {
  const v = document.querySelector('video')
  const frozen = v.currentTime
  let set = null
  Object.defineProperty(v, 'currentTime', {
    configurable: true,
    get: () => frozen,
    set: (x) => { set = x },
  })
  for (let i = 0; i < 16 && set === null; i++) {
    await new Promise((r) => setTimeout(r, 500))
  }
  delete v.currentTime
  return { frozen, set }
})
// Either the watchdog's +0.05 or, now that hls.js is really in charge, its
// own gap controller's nudge — both poke the clock forward.
check(
  poked.set !== null && poked.set > poked.frozen && poked.set < poked.frozen + 1,
  'watchdog nudges a frozen clock',
  JSON.stringify(poked),
)
// The escalation may have detached the media mid-sample; wait for the clock
// to actually run again.
let seen = (await state()).t
for (let i = 0; i < 15; i++) {
  await sleep(1000)
  s = await state()
  if (s.t > seen && s.t > 1) break
  seen = Math.max(seen, s.t)
}
check(
  s.t > 1 && !s.paused && s.error === null,
  'playback healthy after the watchdog test',
  JSON.stringify(s),
)

check(errors.length === 0, 'no console errors', [...new Set(errors)].slice(0, 4).join(' | '))

await browser.close()
console.log(failures === 0 ? '\nALL PASSED' : `\n${failures} FAILED`)
process.exit(failures === 0 ? 0 : 1)
