// Are subtitles still in sync after leaving the tab and coming back?
//
// The episode's subtitle track is 90 position-coded markers: cue i is a block
// drawn at x = 40 + 12i of a 1280-wide frame, shown from 2i to 2i+1.9s. So the
// centroid of whatever JASSUB has painted says exactly which cue is on screen,
// and comparing that against the video's clock measures sync directly rather
// than by eye.
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
  const text = await res.text()
  try {
    return { ok: res.ok, body: JSON.parse(text) }
  } catch {
    return { ok: res.ok, body: text }
  }
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) })

// ---------------------------------------------------------------- setup
await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  const files = await api('/api/local/files')
  file = (files.body.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show - 03'))
}
check(!!file, 'marker episode listed')
if (!file) process.exit(1)
check((await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: EP })).ok, 'assigned as episode 3')
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })

// Headed by default: headless Chromium reports every tab as visible and keeps
// presenting frames to a background one, so the scenario under test cannot
// happen there.
const browser = await chromium.launch({
  headless: process.env.HEADLESS === '1',
  args: ['--autoplay-policy=no-user-gesture-required'],
})
const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
const page = await context.newPage()
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text().slice(0, 200)))
page.on('pageerror', (e) => errors.push(`uncaught: ${e.message.slice(0, 200)}`))

// Every seek the page performs, so a watchdog that fires while nobody is
// looking cannot go unnoticed.
await page.addInitScript(() => {
  window.__seeks = []
  document.addEventListener(
    'seeked',
    (e) => {
      const v = e.target
      if (v instanceof HTMLVideoElement) {
        window.__seeks.push({ t: Number(v.currentTime.toFixed(3)), hidden: document.visibilityState === 'hidden' })
      }
    },
    true,
  )
})

await page.goto(`${BASE}/watch/${ANIME}/${EP}`, { waitUntil: 'domcontentloaded' })

// Reads the subtitle canvas and reports which cue is painted on it.
const readSubtitle = () =>
  page.evaluate(() => {
    const v = document.querySelector('video')
    const canvas = document.querySelector('canvas.JASSUB')
    if (!v || !canvas) return { ok: false, why: canvas ? 'no video' : 'no subtitle canvas' }

    const probe = document.createElement('canvas')
    probe.width = 320
    probe.height = 180
    const ctx = probe.getContext('2d', { willReadFrequently: true })
    try {
      ctx.drawImage(canvas, 0, 0, probe.width, probe.height)
    } catch (e) {
      return { ok: false, why: 'canvas unreadable: ' + e.message }
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
    if (sum === 0) return { ok: true, time, blank: true }
    // Normalised centroid → the cue's x in the 1280-wide script, → its index.
    const cx = (weighted / sum / probe.width) * 1280
    return { ok: true, time, blank: false, cue: Math.round((cx - 45) / 12), cx }
  })

// Which cue should be on screen at this instant, or null inside a gap.
const expectedCue = (t) => {
  const i = Math.floor(t / 2)
  return t - i * 2 < 1.9 ? i : null
}

// ---------------------------------------------------------------- baseline
let s = null
for (let i = 0; i < 45; i++) {
  await sleep(1000)
  s = await page.evaluate(() => {
    const v = document.querySelector('video')
    return v ? { t: v.currentTime, paused: v.paused, ready: v.readyState } : null
  })
  if (s && s.t > 2 && !s.paused) break
  if (s && s.paused && s.ready >= 3 && i > 3) {
    await page.evaluate(() => document.querySelector('video')?.play().catch(() => {}))
  }
}
check(!!s && s.t > 2, 'marker episode plays', JSON.stringify(s))

// Presented frames, which is what drives subtitle drawing. Counting them is
// how this proves it really reproduced "the tab stopped rendering".
await page.evaluate(() => {
  const v = document.querySelector('video')
  window.__frames = 0
  const tick = () => {
    window.__frames++
    v.requestVideoFrameCallback(tick)
  }
  v.requestVideoFrameCallback(tick)
})

let probe = await readSubtitle()
check(probe.ok, 'subtitle canvas is readable', probe.why ?? '')
if (!probe.ok) {
  await browser.close()
  process.exit(1)
}

// Sync while simply playing: sample a few times and take the worst drift.
const drift = (r) => (r.blank || r.cue === null ? null : r.cue * 2 + 0.95 - r.time)
let worst = 0
let samples = 0
for (let i = 0; i < 6; i++) {
  await sleep(700)
  const r = await readSubtitle()
  const d = drift(r)
  if (d === null) continue
  samples++
  worst = Math.max(worst, Math.abs(d))
}
check(samples >= 3, 'sampled the canvas while playing', `${samples} samples`)
check(worst < 1.6, 'subtitles track the clock during normal playback', `worst offset ${worst.toFixed(2)}s (a cue spans 1.9s)`)

// ---------------------------------------------------------------- hidden tab
const other = await context.newPage()
await other.goto('about:blank')

const hideFor = async (seconds, { paused = false } = {}) => {
  if (paused) await page.evaluate(() => document.querySelector('video').pause())
  const before = await page.evaluate(() => ({
    t: document.querySelector('video').currentTime,
    frames: window.__frames,
  }))
  await other.bringToFront()
  const visibility = await page.evaluate(() => document.visibilityState)
  await sleep(seconds * 1000)
  await page.bringToFront()
  // The moment of return, before any new frame can have been presented.
  const immediate = await readSubtitle()
  const frames = await page.evaluate(() => window.__frames)
  await sleep(1200)
  const settled = await readSubtitle()
  return { before, visibility, immediate, settled, frames }
}

// Playing while the tab is in the background: the clock runs on, and no video
// frame is presented, so nothing redraws the subtitles until we come back.
let away = await hideFor(20)
const ran = away.immediate.time - away.before.t
const drawn = away.frames - away.before.frames
// visibilityState is the coarse signal and Playwright does not always flip it;
// what the subtitles actually depend on is frames being presented, so that is
// what has to have stopped for this to be the scenario people report.
console.log(`     backgrounded: visibilityState=${away.visibility}, ${drawn} frames presented over ${ran.toFixed(1)}s of media`)
check(ran > 10, 'playback carried on while the tab was in the background', `${ran.toFixed(1)}s of media`)
check(
  drawn < ran * 5,
  'frame presentation really stalled while backgrounded',
  `${drawn} frames for ${ran.toFixed(1)}s (playing would be ~${Math.round(ran * 24)})`,
)

const immediateDrift = drift(away.immediate)
const settledDrift = drift(away.settled)
console.log(
  `     on return: t=${away.immediate.time.toFixed(2)} cue=${away.immediate.cue}` +
    ` (expected ${expectedCue(away.immediate.time)})` +
    ` | 1.2s later: t=${away.settled.time.toFixed(2)} cue=${away.settled.cue}` +
    ` (expected ${expectedCue(away.settled.time)})`,
)
check(
  settledDrift === null || Math.abs(settledDrift) < 1.6,
  'subtitles are back in sync shortly after returning',
  settledDrift === null ? 'blank' : `${settledDrift.toFixed(2)}s`,
)
check(
  immediateDrift === null || Math.abs(immediateDrift) < 1.6,
  'subtitles are in sync at the instant of return (no stale cue)',
  immediateDrift === null ? 'blank' : `${immediateDrift.toFixed(2)}s off`,
)

// Paused in the background, which is what most people do.
await page.evaluate(() => document.querySelector('video').play().catch(() => {}))
await sleep(1500)
away = await hideFor(8, { paused: true })
const pausedDrift = drift(away.immediate)
check(
  pausedDrift === null || Math.abs(pausedDrift) < 1.6,
  'a paused player comes back showing the right cue',
  pausedDrift === null ? 'blank' : `${pausedDrift.toFixed(2)}s`,
)

// ---------------------------------------------------------------- watchdog
// A seek immediately before the tab goes away: no frames are presented while
// hidden, which must not read as a stuck decoder and provoke a recovery seek.
await page.evaluate(() => {
  window.__seeks.length = 0
  const v = document.querySelector('video')
  v.play().catch(() => {})
  v.currentTime = 60
})
await sleep(500)
await other.bringToFront()
await sleep(12000)
await page.bringToFront()
await sleep(1500)

const seeks = await page.evaluate(() => window.__seeks)
// The deliberate seek to 60 is the only one anybody asked for. Anything else is
// the recovery watchdog firing at a decoder that was never stuck.
const uninvited = seeks.filter((s) => Math.abs(s.t - 60) > 0.005)
check(
  uninvited.length === 0,
  'nothing seeks behind the viewer while the tab is away',
  uninvited.length ? JSON.stringify(uninvited.slice(0, 4)) : `seeks: ${JSON.stringify(seeks)}`,
)

const after = await readSubtitle()
const afterDrift = drift(after)
check(
  afterDrift === null || Math.abs(afterDrift) < 1.6,
  'still in sync after a seek followed by a tab switch',
  afterDrift === null ? 'blank' : `${afterDrift.toFixed(2)}s`,
)
check(errors.length === 0, 'no console errors', [...new Set(errors)].slice(0, 4).join(' | '))

await browser.close()
console.log(failures === 0 ? '\nALL PASSED' : `\n${failures} FAILED`)
process.exit(failures === 0 ? 0 : 1)
