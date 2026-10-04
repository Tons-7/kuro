// Reports, on a downloading episode, the seconds a line should be on screen and the subtitle layer drew nothing.
//   KURO_EXTRA_CONFIG='[[indexer]] ...' KURO_SHOW_ID=235 EPISODE=1 [SEEK_TO=600] \
//     node scripts/run-player-check.mjs subs-lag-probe.mjs
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ID = Number(process.env.KURO_SHOW_ID)
const EP = Number(process.env.EPISODE ?? 1)
const SECONDS = Number(process.env.SECONDS ?? 100)
const SEEK_TO = Number(process.env.SEEK_TO ?? 0)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

await fetch(`${BASE}/api/prefs`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ key: 'playback.autoplay', value: 'true' }),
})

// HEADED=1: a real window with the GPU, where the renderer's canvas behaves as it does for a viewer.
const browser = await chromium.launch({
  headless: process.env.HEADED !== '1',
  args: ['--autoplay-policy=no-user-gesture-required'],
})
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const t0 = Date.now()
const since = () => ((Date.now() - t0) / 1000).toFixed(1).padStart(6)

// What the viewer has: the clock, the buffered video, and whether the subtitle layer holds any drawn pixels.
const look = () =>
  page
    .evaluate(() => {
      const v = document.querySelector('video')
      if (!v) return null
      let end = v.currentTime
      for (let i = 0; i < v.buffered.length; i++) {
        if (v.buffered.start(i) <= v.currentTime + 0.5 && v.buffered.end(i) >= v.currentTime) end = v.buffered.end(i)
      }
      const layer = document.querySelector('canvas.JASSUB')
      let drawn = -1
      if (layer) {
        const copy = document.createElement('canvas')
        copy.width = 320
        copy.height = 180
        const g = copy.getContext('2d')
        g.drawImage(layer, 0, 0, 320, 180)
        const px = g.getImageData(0, 0, 320, 180).data
        drawn = 0
        for (let i = 3; i < px.length; i += 4) if (px[i] > 0) drawn++
      }
      return { at: v.currentTime, buffered: end, paused: v.paused, stalled: v.readyState < 3, drawn }
    })
    .catch(() => null)

const cuesOf = (ass) =>
  [...ass.matchAll(/^Dialogue:\s*[^,]*,(\d+):(\d+):(\d+(?:\.\d+)?),(\d+):(\d+):(\d+(?:\.\d+)?),.*$/gm)].map((m) => ({
    start: Number(m[1]) * 3600 + Number(m[2]) * 60 + Number(m[3]),
    end: Number(m[4]) * 3600 + Number(m[5]) * 60 + Number(m[6]),
  }))

let trackUrl = ''
let loads = 0
page.on('response', async (res) => {
  const url = res.url()
  if (url.includes('/api/stream/open')) console.log(`${since()}s  stream opened (${res.status()})`)
  if (url.endsWith('/fonts')) {
    const body = await res.json().catch(() => ({}))
    if (body.ready) console.log(`${since()}s  fonts ready: ${body.fonts?.length ?? 0}`)
  }
  if (!url.includes('/subtitle/')) return
  trackUrl ||= url.split('?')[0]
  // No query: a fresh renderer loading the track, at the start and after every rebuild.
  if (!url.includes('?')) console.log(`${since()}s  renderer load #${++loads} of the track (${res.status()})`)
})

await page.goto(`${BASE}/watch/${ID}/${EP}`, { waitUntil: 'domcontentloaded' })
const seen = []
let playing = false
for (let i = 0; i < SECONDS * 2; i++) {
  await sleep(500)
  const now = await look()
  if (!now) continue
  if (now.at > 0.5 && !playing) {
    playing = true
    console.log(`${since()}s  playback started`)
  }
  if (SEEK_TO && i === 70) {
    console.log(`${since()}s  seeking to ${SEEK_TO}s`)
    await page.locator('video').evaluate((v, t) => (v.currentTime = t), SEEK_TO)
  }
  if (now.paused) await page.locator('video').evaluate((v) => v.play().catch(() => {})).catch(() => {})
  if (playing) seen.push({ when: since(), ...now })
}

// Judged against the fullest track, asked for at the end.
const last = seen.at(-1) ?? { at: 0, buffered: 0 }
const cues = trackUrl ? cuesOf(await (await fetch(`${trackUrl}?at=${Math.floor(last.at)}`)).text()) : []
const due = (t) => cues.some((c) => t >= c.start + 0.3 && t <= c.end - 0.3)
let missed = 0
let shown = 0
let run = null
const report = () => {
  if (run) console.log(`${run.from}s..${run.to}s  MISSING for ${(run.n / 2).toFixed(1)}s of playback at ${run.a.toFixed(1)}s..${run.b.toFixed(1)}s (layer: ${run.why})`)
  run = null
}
for (const s of seen) {
  // Only moving picture counts: a stalled video shows whatever line it stopped on.
  if (s.stalled || !due(s.at)) {
    report()
    continue
  }
  if (s.drawn > 0) {
    shown++
    report()
    continue
  }
  missed++
  run ??= { from: s.when, a: s.at, n: 0, why: s.drawn < 0 ? 'no canvas' : 'empty' }
  run.to = s.when
  run.b = s.at
  run.n++
}
report()
console.log(
  `${since()}s  done: playhead ${last.at.toFixed(0)}s, ${cues.length} cues known, a line was due in ${shown + missed} samples: drawn ${shown}, missing ${missed}`,
)
await browser.close()
