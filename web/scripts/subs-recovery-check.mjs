// Subtitles after the server reaps the session (a long AFK) and through the
// fullscreen and tab switching people try when they look wrong. Uses the
// position-coded marker episode: which cue is painted says exactly where the
// renderer thinks the clock is.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const EP = 3

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const api = async (path, init) => {
  const res = await fetch(BASE + path, { ...init, headers: { 'content-type': 'application/json' } })
  let body
  try {
    body = await res.json()
  } catch {}
  return { ok: res.ok, status: res.status, body }
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) })
const until = async (fn, ms = 10000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(250)
  }
  return false
}

await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  file = ((await api('/api/local/files')).body?.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show - 03'))
}
check(!!file, 'marker episode listed')
if (!file) process.exit(1)
await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: EP })
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })

// Headed: a headless tab is never hidden, and fullscreen needs a real window.
const browser = await chromium.launch({
  headless: process.env.HEADLESS === '1',
  args: ['--autoplay-policy=no-user-gesture-required'],
})
const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
const page = await context.newPage()
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text().slice(0, 200)))
page.on('pageerror', (e) => errors.push(`uncaught: ${e.message.slice(0, 200)}`))
const failed = []
const timeline = []
let t0 = Date.now()
const stamp = () => ((Date.now() - t0) / 1000).toFixed(1)
page.on('response', (r) => {
  const url = r.url().replace(BASE, '')
  if (r.status() >= 400) failed.push(`${r.status()} ${r.request().method()} ${url}`)
  if (/\/api\/stream|\/api\/play/.test(url) && !/\/subtitle\//.test(url)) {
    timeline.push(`${stamp()}s ${r.status()} ${url.replace(/\?.*/, '').slice(0, 60)}`)
  }
})
const state = () =>
  video.evaluate((v) => ({
    t: +v.currentTime.toFixed(1), paused: v.paused, ready: v.readyState, net: v.networkState,
    err: v.error?.code ?? null, src: (v.currentSrc || '').slice(-40),
  })).catch(() => null)

// Every reset of the element's timeline, with what caused it.
await page.addInitScript(() => {
  window.__events = []
  const note = (what) => window.__events.push(`${(performance.now() / 1000).toFixed(1)}s ${what}`)
  const load = HTMLMediaElement.prototype.load
  HTMLMediaElement.prototype.load = function () {
    note(`load() at t=${this.currentTime.toFixed(1)} from ${new Error().stack.split('\n')[2]?.trim().slice(0, 90)}`)
    return load.call(this)
  }
  document.addEventListener(
    'seeking',
    (e) => e.target instanceof HTMLVideoElement && note(`seeking to ${e.target.currentTime.toFixed(1)}`),
    true,
  )
  document.addEventListener('emptied', (e) => e.target instanceof HTMLVideoElement && note('emptied'), true)
  document.addEventListener('loadedmetadata', (e) => e.target instanceof HTMLVideoElement && note(`loadedmetadata t=${e.target.currentTime.toFixed(1)}`), true)
})
await page.goto(`${BASE}/watch/${ANIME}/${EP}`, { waitUntil: 'domcontentloaded' })
const video = page.locator('video')

const readSubtitle = () =>
  page.evaluate(() => {
    const v = document.querySelector('video')
    const canvases = document.querySelectorAll('canvas.JASSUB')
    const canvas = canvases[canvases.length - 1]
    if (!v || !canvas) return { ok: false, canvases: canvases.length, why: canvas ? 'no video' : 'no subtitle canvas' }
    const probe = document.createElement('canvas')
    probe.width = 320
    probe.height = 180
    const ctx = probe.getContext('2d', { willReadFrequently: true })
    try {
      ctx.drawImage(canvas, 0, 0, probe.width, probe.height)
    } catch (e) {
      return { ok: false, canvases: canvases.length, why: 'canvas unreadable: ' + e.message }
    }
    const d = ctx.getImageData(0, 0, probe.width, probe.height).data
    let sum = 0
    let weighted = 0
    for (let y = 0; y < probe.height; y++) {
      for (let x = 0; x < probe.width; x++) {
        const a = d[(y * probe.width + x) * 4 + 3]
        if (a > 40) {
          sum += a
          weighted += a * x
        }
      }
    }
    const time = v.currentTime
    if (sum === 0) return { ok: true, canvases: canvases.length, time, blank: true }
    const cx = (weighted / sum / probe.width) * 1280
    return { ok: true, canvases: canvases.length, time, blank: false, cue: Math.round((cx - 45) / 12) }
  })
const drift = (r) => (!r.ok || r.blank ? null : r.cue * 2 + 0.95 - r.time)

// Worst drift over a few samples; null when nothing was ever painted.
const measure = async (label) => {
  let worst = null
  let blank = 0
  let canvases = 0
  for (let i = 0; i < 6; i++) {
    await sleep(700)
    const r = await readSubtitle()
    canvases = Math.max(canvases, r.canvases ?? 0)
    const d = drift(r)
    if (d === null) {
      blank++
      continue
    }
    worst = Math.max(worst ?? 0, Math.abs(d))
  }
  console.log(`     ${label}: worst drift ${worst === null ? 'n/a' : worst.toFixed(2) + 's'}, ${blank}/6 blank, ${canvases} canvas`)
  return { worst, blank, canvases }
}

check(await until(() => video.evaluate((v) => v.currentTime > 2 && !v.paused).catch(() => false), 60000), 'marker episode plays')
let m = await measure('baseline')
check(m.worst !== null && m.worst < 1.6, 'subtitles in sync at the start', `${m.worst}`)
check(m.canvases === 1, 'one subtitle canvas', `${m.canvases}`)

// ---- the long AFK: paused, session reaped by the server, tab away and back
console.log('\n-- paused, session reaped, tab away, back and play --')
const pausedAt = await video.evaluate((v) => {
  v.pause()
  return v.currentTime
})
// What the idle reaper does after ten minutes, without the wait.
await page.evaluate(() => (window.__events.length = 0))
const closed = await api(`/api/stream/${ANIME}-${EP}`, { method: 'DELETE' })
check(closed.body?.closed === true, 'session closed server-side, as the reaper would', JSON.stringify(closed.body))
const other = await context.newPage()
await other.goto('about:blank')
await other.bringToFront()
await sleep(5000)
await page.bringToFront()
await sleep(1500)
// Where it was paused, read before the reopen can reset the element.
const before = pausedAt
t0 = Date.now()
timeline.length = 0
await video.evaluate((v) => {
  window.__trace = []
  const tick = () => window.__trace.push(+v.currentTime.toFixed(2))
  const id = setInterval(tick, 250)
  setTimeout(() => clearInterval(id), 8000)
  v.play().catch(() => {})
})
await sleep(8500)
console.log(`     currentTime after play (every 250ms): ${JSON.stringify(await page.evaluate(() => window.__trace.slice(0, 12)))}`)
console.log('     element events since the session was closed:')
for (const l of await page.evaluate(() => window.__events.slice(-14))) console.log('       ' + l)
const resumed = await until(() => video.evaluate((v, at) => !v.paused && v.currentTime > at + 1, before).catch(() => false), 52000)
check(resumed, `playback resumes from where it was paused (${pausedAt.toFixed(1)}s)`, JSON.stringify(await state()))
// Does it keep going once it has moved? Media time gained over 20 s of wall clock.
const t1 = (await state())?.t ?? 0
await sleep(20000)
const gained = ((await state())?.t ?? 0) - t1
check(gained > 15, 'playback runs at speed after recovery', `${gained.toFixed(1)}s of media in 20s`)
console.log('     timeline after play:')
for (const l of timeline.slice(0, 40)) console.log('       ' + l)
console.log(`     failed requests so far: ${failed.join(' | ') || 'none'}`)
m = await measure('after recovery')
check(m.worst !== null && m.worst < 1.6, 'subtitles in sync after recovery', `${m.worst}`)
check(m.canvases === 1, 'still one subtitle canvas', `${m.canvases}`)

// ---- the things people try: fullscreen in and out, tab away and back
console.log('\n-- fullscreen toggles and tab switches --')
for (let i = 0; i < 3; i++) {
  await page.mouse.move(640, 400)
  await page.keyboard.press('f')
  await sleep(1200)
  await other.bringToFront()
  await sleep(1500)
  await page.bringToFront()
  await sleep(800)
  await page.keyboard.press('f')
  await sleep(1200)
}
m = await measure('after toggles')
check(m.worst !== null && m.worst < 1.6, 'subtitles in sync after fullscreen and tab churn', `${m.worst}`)
check(m.blank < 6, 'subtitles still being drawn', `${m.blank}/6 blank`)
check(m.canvases === 1, 'no leaked subtitle canvases', `${m.canvases}`)

// Still tracking: the painted cue must advance with the clock. The readback
// misses some frames, so each reading waits for a painted one.
const painted = async () => {
  for (let i = 0; i < 12; i++) {
    const r = await readSubtitle()
    if (r.ok && !r.blank) return r
    await sleep(250)
  }
  return null
}
const a = await painted()
await sleep(4500)
const b = await painted()
check(!!a && !!b && b.cue > a.cue, 'the painted cue keeps advancing', `${a?.cue} → ${b?.cue} video=${JSON.stringify(await state())}`)
// The one 404 is the reaped playlist, which is how the player learns to reopen.
const unexpected = failed.filter((f) => !/404 GET \/api\/stream\/[^/]+\/playlist\.m3u8/.test(f))
check(unexpected.length === 0, 'no failed requests beyond the reaped playlist', unexpected.join(' | '))
check(errors.filter((e) => !/404/.test(e)).length === 0, 'no console errors', [...new Set(errors)].slice(0, 4).join(' | '))

await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
