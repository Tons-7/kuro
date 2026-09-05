// End-to-end check of the player fixes against a local-library episode:
// subtitles keep filling, seeking past the produced segments keeps frames
// coming, the scrubber drags, progress carries played time, a peek at the
// ending does not count as watched, and the resume point survives it.
import { spawnSync } from 'node:child_process'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
// A real catalogue id, so the series page resolves; the file is assigned to it
// as episode 1 for the duration of the run.
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
  return { ok: res.ok, status: res.status, body, headers: res.headers }
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

let paths = await post('/api/local/paths', { paths: [LIB] })
check(paths.ok, 'library path set', JSON.stringify(paths.body).slice(0, 120))
let scan = await post('/api/local/scan', {})
check(scan.ok, 'library scan started', JSON.stringify(scan.body).slice(0, 160))
// The scan runs in the background; the file shows up when it has been walked.
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  const files = await api('/api/local/files')
  file = (files.body.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show'))
}
check(!!file, 'test file listed', JSON.stringify(file ?? {}).slice(0, 200))
if (!file) process.exit(1)
const assign = await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: EP })
check(assign.ok, 'file assigned to episode', JSON.stringify(assign.body))
// Nothing automatic is on by default; the check is about the pipeline.
const autoplay = await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })
check(autoplay.ok, 'autoplay enabled for the run', JSON.stringify(autoplay.body))

// ---------------------------------------------------------------- downloads
// Seeded straight into the database, since nothing is really downloaded here.
// Checked first: the sweep forgets rows the engine does not hold two minutes in.
if (process.env.KURO_DB && process.env.KURO_REPO) {
  const seed = spawnSync('go', ['run', './scripts/e2e-seed', '-db', process.env.KURO_DB, '-anime', String(ANIME)], {
    cwd: process.env.KURO_REPO,
    stdio: 'inherit',
  })
  check(seed.status === 0, 'download rows seeded')
}

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text().slice(0, 200)))
page.on('pageerror', (e) => errors.push(`uncaught: ${e.message.slice(0, 200)}`))

await page.goto(`${BASE}/downloads`, { waitUntil: 'domcontentloaded' })
const downloadedBadge = page.getByText('Downloaded', { exact: true })
const cachedBadge = page.getByText('Cached', { exact: true })
await downloadedBadge.first().waitFor({ timeout: 15_000 }).catch(() => {})
check((await downloadedBadge.count()) === 1, 'a kept download shows as Downloaded')
check((await cachedBadge.count()) === 1, 'a watched episode shows as Cached')
await page.screenshot({ path: `${shots}/downloads-tiers.png` })

// Keep moves the cached one over; the budget no longer sees it.
await page.locator('li', { has: cachedBadge }).getByRole('button', { name: 'Keep', exact: true }).click()
let keptBoth = false
for (let i = 0; i < 20 && !keptBoth; i++) {
  await sleep(250)
  keptBoth = (await downloadedBadge.count()) === 2
}
check(keptBoth, 'Keep moves a cached episode to the downloaded tier')
const usage = await api('/api/cache')
check(
  usage.ok && usage.body.kept === 2 && usage.body.bytes === 0 && usage.body.keptBytes > 0,
  'kept downloads are outside the cache budget',
  JSON.stringify({ bytes: usage.body.bytes, kept: usage.body.kept, keptBytes: usage.body.keptBytes }),
)
await page.screenshot({ path: `${shots}/downloads-kept.png` })

// Settings offer the auto-delete rule, and the downloads toggle only once it is on.
await page.goto(`${BASE}/settings`, { waitUntil: 'domcontentloaded' })
await page.getByRole('tab', { name: 'Quality' }).click()
const autodelete = page.getByRole('combobox').filter({ has: page.locator('option[value="keep2"]') })
check(await autodelete.isVisible().catch(() => false), 'auto-delete watched setting is offered')
check(!(await page.getByText('Also delete downloaded episodes').isVisible()), 'downloads toggle hidden while auto-delete is off')
await autodelete.selectOption('keep2')
const toggleShown = await page
  .getByText('Also delete downloaded episodes')
  .waitFor({ timeout: 5_000 })
  .then(() => true)
  .catch(() => false)
check(toggleShown, 'downloads toggle appears once auto-delete is on')
const saved = await api('/api/prefs')
check(saved.body?.effective?.['cache.autodelete'] === 'keep2', 'auto-delete choice is saved', JSON.stringify(saved.body?.effective?.['cache.autodelete']))
await page.screenshot({ path: `${shots}/settings-cache.png` })
await autodelete.selectOption('off')

// ---------------------------------------------------------------- playback
// Progress goes out by sendBeacon, whose body the request API cannot read;
// record it at the source instead.
await page.addInitScript(() => {
  window.__beacons = []
  const real = navigator.sendBeacon.bind(navigator)
  navigator.sendBeacon = (url, body) => {
    if (String(url).includes('/api/progress') && body instanceof Blob) {
      void body.text().then((t) => window.__beacons.push(JSON.parse(t)))
    }
    return real(url, body)
  }
})
const progressReports = []
const collectReports = async () => {
  const got = await page.evaluate(() => window.__beacons ?? []).catch(() => [])
  progressReports.splice(0, progressReports.length, ...got)
}
const prepared = []
page.on('request', (r) => {
  if (r.url().includes('/api/prepare')) {
    try {
      prepared.push(JSON.parse(r.postData() ?? '{}'))
    } catch {}
  }
})
const subtitleResponses = []
let streamOpen = null
page.on('response', (r) => {
  if (r.url().includes('/subtitle/')) {
    subtitleResponses.push({ status: r.status(), complete: r.headers()['x-kuro-complete'] })
  }
  if (r.url().includes('/api/stream/open')) {
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
      dur: Number.isFinite(v.duration) ? Number(v.duration.toFixed(1)) : null,
      paused: v.paused,
      ready: v.readyState,
      frames: q?.totalVideoFrames ?? -1,
      dropped: q?.droppedVideoFrames ?? -1,
      error: v.error ? `${v.error.code}: ${v.error.message}` : null,
      subCanvas: document.querySelectorAll('canvas').length,
    }
  })

// Wait for playback to start; press play if autoplay did not take.
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
check(!!s && s.error === null, 'no media error at start', s?.error ?? '')

// Frames keep coming while playing.
const f0 = s.frames
await sleep(3000)
s = await state()
check(s.frames > f0, 'video frames advance while playing', `${f0} → ${s.frames}`)
check(s.subCanvas >= 1, 'subtitle canvas attached', `canvases=${s.subCanvas}`)

// Dual-audio: the file has a Japanese and an English track. The server opens
// on Japanese for a sub viewer, and switching re-encodes onto the other one.
check(
  streamOpen?.audio?.length === 2 && streamOpen.audioTrack === 0,
  'stream opens on the Japanese track of a dual-audio file',
  JSON.stringify({ audio: streamOpen?.audio?.length, track: streamOpen?.audioTrack }),
)
check(
  await page.locator('button[title="Audio"]').isVisible().catch(() => false),
  'audio picker shown for a dual-audio file',
)

// Subtitle track was fetched and the server labels it complete (a local file).
await sleep(6000)
check(subtitleResponses.length > 0, 'subtitle track fetched', JSON.stringify(subtitleResponses.slice(0, 3)))
check(
  subtitleResponses.some((r) => r.status === 200 && r.complete === 'true'),
  'server reports the track complete for a whole file',
  JSON.stringify(subtitleResponses.slice(0, 3)),
)

// Seek well past anything produced: segment ~25 of 30.
await page.evaluate(() => {
  const v = document.querySelector('video')
  v.currentTime = 150
})
let after = null
for (let i = 0; i < 40; i++) {
  await sleep(1000)
  after = await state()
  if (after.t > 151 && !after.paused && after.ready >= 3) break
}
check(after.t > 151, 'playback resumed after a seek into unproduced segments', JSON.stringify(after))
const fSeek = after.frames
await sleep(3000)
after = await state()
check(after.frames > fSeek, 'video frames advance after the seek (no black picture)', `${fSeek} → ${after.frames}`)
check(after.error === null, 'no media error after the seek', after.error ?? '')
await page.screenshot({ path: `${shots}/after-seek.png` })

// A non-black picture: sample the video element's pixels.
const luminance = await page.evaluate(async () => {
  const v = document.querySelector('video')
  const c = document.createElement('canvas')
  c.width = 64
  c.height = 36
  const ctx = c.getContext('2d')
  ctx.drawImage(v, 0, 0, 64, 36)
  const d = ctx.getImageData(0, 0, 64, 36).data
  let sum = 0
  for (let i = 0; i < d.length; i += 4) sum += d[i] + d[i + 1] + d[i + 2]
  return sum / (d.length / 4) / 3
})
check(luminance > 20, 'picture is not black after the seek', `mean luminance ${luminance.toFixed(1)}`)

// Seek back near the start: the encoder is up near 2:30, so this is a backward
// seek it must relaunch for rather than wait on the forward pass (the stall the
// friend hit). It must deliver, not spin.
await page.evaluate(() => {
  const v = document.querySelector('video')
  v.currentTime = 6
  void v.play()
})
let back = null
for (let i = 0; i < 40; i++) {
  await sleep(1000)
  back = await state()
  if (back.t > 6.5 && back.t < 60 && !back.paused && back.ready >= 3) break
}
check(back && back.t > 6.5 && back.t < 60, 'playback resumes after seeking backward', JSON.stringify(back))
check(back && back.error === null, 'no media error after the backward seek', back?.error ?? '')
const bFrames = back.frames
await sleep(3000)
back = await state()
check(back.frames > bFrames, 'frames advance after the backward seek', `${bFrames} → ${back.frames}`)

// Drag the scrubber: pointer down at 20%, move to 40%, release.
const bar = page.locator('.group\\/scrub')
const box = await bar.boundingBox()
check(!!box, 'scrubber present')
if (box) {
  const y = box.y + box.height / 2
  await page.mouse.move(box.x + box.width * 0.2, y)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width * 0.3, y, { steps: 5 })
  await page.mouse.move(box.x + box.width * 0.4, y, { steps: 5 })
  // While dragging the fill follows the pointer, not the clock.
  const fillWidth = await page.evaluate(() => {
    const fill = document.querySelector('.group\\/scrub .bg-accent-500')
    return fill ? parseFloat(fill.style.width) : -1
  })
  check(Math.abs(fillWidth - 40) < 3, 'bar follows the pointer while dragging', `fill=${fillWidth}%`)
  await page.mouse.up()
  await sleep(2500)
  const dragged = await state()
  check(
    Math.abs(dragged.t - 0.4 * dragged.dur) < 6,
    'release seeks to where the finger left the bar',
    `t=${dragged.t} want≈${(0.4 * dragged.dur).toFixed(1)}`,
  )
}

// Progress reports carry played time and exclude the seeks.
await page.evaluate(() => document.querySelector('video').pause())
await sleep(800)
await collectReports()
check(progressReports.length > 0, 'progress reported', `${progressReports.length} reports`)
const withPlayed = progressReports.filter((p) => typeof p.played === 'number')
check(withPlayed.length === progressReports.length, 'every report carries played', JSON.stringify(progressReports.slice(-2)))
const totalPlayed = progressReports.reduce((n, p) => n + (p.played ?? 0), 0)
check(totalPlayed > 5 && totalPlayed < 60, 'played counts time played, not seeks', `played=${totalPlayed.toFixed(1)}s`)
check(
  progressReports.every((p) => (p.played ?? 0) < 120),
  'no single report claims more than it can',
  JSON.stringify(progressReports.slice(-1)),
)

// The resume point is where we paused.
const paused = await state()
let detail = await api(`/api/anime/${ANIME}`)
check(
  detail.body.resume && Math.abs(detail.body.resume.position - paused.t) < 3 && detail.body.resume.episode === EP,
  'series page offers to continue at the paused position',
  JSON.stringify(detail.body.resume),
)
let eps = await api(`/api/episodes?id=${ANIME}`)
let row = (eps.body.items ?? []).find((e) => e.number === EP)
check(row && row.resumable && !row.watched, 'episode row is resumable and not watched', JSON.stringify(row))

// Peek at the ending: jump to 95% and let it run out.
await page.evaluate(() => {
  const v = document.querySelector('video')
  v.currentTime = v.duration - 6
  void v.play()
})
let ended = false
for (let i = 0; i < 40; i++) {
  await sleep(1000)
  ended = await page.evaluate(() => document.querySelector('video')?.ended ?? false)
  if (ended) break
}
check(ended, 'played out to the end after the peek')
await sleep(1500)

detail = await api(`/api/anime/${ANIME}`)
eps = await api(`/api/episodes?id=${ANIME}`)
row = (eps.body.items ?? []).find((e) => e.number === EP)
check(row && !row.watched, 'a peek at the ending did not mark the episode watched', JSON.stringify(row))
check(
  detail.body.resume && Math.abs(detail.body.resume.position - paused.t) < 3,
  'the resume point survived the peek',
  JSON.stringify(detail.body.resume),
)

// Switching audio re-encodes onto the other track. Done last on this page: it
// drops the session's segments, which the player recovers from by reloading
// but a direct API poke does not, so nothing here demuxes the old stream after.
if (streamOpen?.id) {
  const sid = streamOpen.id
  const audio = (await api(`/api/stream/${sid}/audio?track=1`, { method: 'POST' })).body
  check(audio?.audioTrack === 1 && audio?.changed, 'switching selects the English track', JSON.stringify(audio))
  const same = (await api(`/api/stream/${sid}/audio?track=1`, { method: 'POST' })).body
  check(same?.changed === false, 'switching to the current track is a no-op', JSON.stringify(same))
  const bad = await api(`/api/stream/${sid}/audio?track=9`, { method: 'POST' })
  check(bad.status === 400, 'an out-of-range track is rejected', String(bad.status))
}

// Reopening resumes there.
await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
await sleep(2500)
const button = await page.locator('a', { hasText: /Continue episode/ }).first().innerText().catch(() => '')
check(/Continue episode 1 · \d+:\d\d/.test(button), 'series page button names the episode and time', button)

// Opening the series page asks for that episode's release to be found now,
// rather than when play is pressed — the "Searching for a release" wait.
check(
  prepared.some((p) => p.animeId === ANIME && p.episode === EP),
  'series page asks for the continue episode to be made ready',
  JSON.stringify(prepared),
)

// Select mode queues a hand-picked set. Only the library episode is picked: it
// needs no download, so nothing reaches the swarm and the queue drains at once.
await page.getByRole('button', { name: 'Select episodes…' }).click()
const firstRow = page.locator('li button[aria-pressed]').first()
await firstRow.click()
check((await firstRow.getAttribute('aria-pressed')) === 'true', 'episode row toggles selected')
check(
  await page.getByText('1 selected').isVisible().catch(() => false),
  'selection count follows the picks',
)
await page.screenshot({ path: `${shots}/select-episodes.png` })
await page.getByRole('button', { name: 'Download selected' }).click()
const queuedNote = await page
  .getByText(/Queued 1 of 1 episode/)
  .waitFor({ timeout: 10_000 })
  .then(() => true)
  .catch(() => false)
check(queuedNote, 'download selected queues the picked episode')
check(
  (await page.locator('li button[aria-pressed]').count()) === 0,
  'select mode closes after queuing',
)
await sleep(2000)
const queue = await api('/api/download/queue')
check(
  queue.ok && !queue.body.items.some((q) => q.animeId === ANIME && q.state === 'failed'),
  'the queued library episode did not fail',
  JSON.stringify(queue.body.items),
)

await page.goto(`${BASE}/watch/${ANIME}/${EP}`, { waitUntil: 'domcontentloaded' })
let resumed = null
for (let i = 0; i < 40; i++) {
  await sleep(1000)
  resumed = await state()
  if (resumed && resumed.t > 5) break
}
check(resumed && Math.abs(resumed.t - paused.t) < 8, 'reopening resumes at the paused position', JSON.stringify(resumed))

check(errors.length === 0, 'no console errors', [...new Set(errors)].slice(0, 4).join(' | '))

await browser.close()
console.log(failures === 0 ? '\nALL PASSED' : `\n${failures} FAILED`)
process.exit(failures === 0 ? 0 : 1)
