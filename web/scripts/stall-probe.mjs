// Diagnosis, not a release check: plays a real episode (STALL_FILE), noting where it stops and what is buffered.
import { copyFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const SRC = process.env.STALL_FILE
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const EP = 4

const api = async (path, init) => {
  const res = await fetch(BASE + path, { ...init, headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) } })
  const text = await res.text()
  try {
    return { ok: res.ok, body: JSON.parse(text) }
  } catch {
    return { ok: res.ok, body: text }
  }
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const copy = join(LIB, 'Kuro Real Show - 04.mkv')
if (!existsSync(copy)) copyFileSync(SRC, copy)
await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let file
for (let i = 0; i < 60 && !file; i++) {
  await sleep(1000)
  file = ((await api('/api/local/files?perPage=200')).body.items ?? []).find((f) => String(f.path).includes('Kuro Real Show'))
}
if (!file) throw new Error('real file not scanned')
await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: EP })
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })
if (process.env.STALL_SOUND !== 'off') await post('/api/prefs', { key: 'playback.headphones', value: 'true' })

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const sessionLog = []
page.on('console', (m) => /hls|stall|segment/i.test(m.text()) && sessionLog.push(m.text().slice(0, 160)))
await page.goto(`${BASE}/watch/${ANIME}/${EP}`, { waitUntil: 'domcontentloaded' })

const state = () =>
  page.evaluate(() => {
    const v = document.querySelector('video')
    if (!v) return null
    const ranges = []
    for (let i = 0; i < v.buffered.length; i++) ranges.push([+v.buffered.start(i).toFixed(3), +v.buffered.end(i).toFixed(3)])
    return { t: +v.currentTime.toFixed(3), paused: v.paused, ready: v.readyState, duration: v.duration, ranges }
  })

// Playing means the clock moves within `ms`; otherwise the state it stopped in.
const playsOn = async (ms) => {
  const a = await state()
  for (let waited = 0; waited < ms; waited += 500) {
    await sleep(500)
    const b = await state()
    if (b && a && b.t > a.t + 0.5 && !b.paused) return { ok: true, at: b }
  }
  return { ok: false, at: await state() }
}

for (let i = 0; i < 120 && !(await state())?.t; i++) await sleep(500)
const started = await playsOn(60_000)
console.log('start', JSON.stringify(started))

// STALL_WATCH=minutes: no seeks, just watching straight through, noting every stretch the clock stood still.
const watch = Number(process.env.STALL_WATCH ?? 0)
if (watch > 0) {
  let last = await state()
  let still = 0
  let stalls = 0
  const end = Date.now() + watch * 60_000
  while (Date.now() < end) {
    await sleep(1000)
    const s = await state()
    if (!s) continue
    if (!s.paused && s.t === last.t) {
      still++
      if (still === 5) {
        stalls++
        console.log(`STALL at ${s.t}s, ${Math.round((Date.now() - (end - watch * 60_000)) / 1000)}s in: ${JSON.stringify(s)}`)
      }
    } else {
      if (still >= 5) console.log(`  resumed after ${still}s at ${s.t}`)
      still = 0
    }
    if (Math.round(s.t) % 60 === 0) console.log(`  t=${s.t} ranges=${JSON.stringify(s.ranges)}`)
    last = s
  }
  console.log(`\n${stalls} stall(s) in ${watch} min of watching; ended at ${last.t}s`)
  if (sessionLog.length) console.log(sessionLog.slice(-20).join('\n'))
  await browser.close()
  process.exit(0)
}

// Let the first pass run ahead, then seek to just after where the playlist says each segment starts.
await sleep(Number(process.env.STALL_WARM ?? 90) * 1000)
const targets = (process.env.STALL_TARGETS ?? '30,60,90,120,150,200,40,80,170').split(',').map(Number)
let stalls = 0
for (const k of targets) {
  const to = k * 6 + 0.05
  await page.evaluate((x) => {
    document.querySelector('video').currentTime = x
  }, to)
  const r = await playsOn(20_000)
  if (!r.ok) stalls++
  console.log(`${r.ok ? 'plays' : 'STALL'} after seek to segment ${k} (${to}s): ${JSON.stringify(r.at)}`)
}
console.log(`\n${stalls} stall(s) in ${targets.length} seeks`)
if (sessionLog.length) console.log(sessionLog.slice(-20).join('\n'))
await browser.close()
