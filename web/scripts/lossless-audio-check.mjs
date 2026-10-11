// A file with lossless surround sound plays in the browser as FLAC: converted, but not to a lossy format,
// and actually heard. Built here, since the harness's own episode carries ordinary audio.
import { spawnSync } from 'node:child_process'
import { join } from 'node:path'
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const LIB = process.env.KURO_LIB
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const post = (path, body) =>
  fetch(BASE + path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) })

check(await prepareLocalEpisode({ base: BASE, lib: LIB, anime: ANIME }), 'test episode ready')

// Episode 2: 24-bit PCM in 5.1, the shape of a Blu-ray's lossless track.
const ffmpeg = join(process.env.KURO_REPO, 'bin', process.platform === 'win32' ? 'ffmpeg.exe' : 'ffmpeg')
const made = spawnSync(ffmpeg, [
  '-hide_banner', '-loglevel', 'error', '-y',
  '-f', 'lavfi', '-i', 'testsrc2=size=1280x720:rate=24:duration=60',
  '-f', 'lavfi', '-i', 'sine=frequency=330:duration=60',
  '-filter_complex', '[1:a]pan=5.1|c0=c0|c1=c0|c2=c0|c3=c0|c4=c0|c5=c0[a]',
  '-map', '0:v', '-map', '[a]',
  '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-g', '48',
  '-c:a', 'pcm_s24le', join(LIB, 'Kuro Test Show - 02.mkv'),
])
check(made.status === 0, 'a file with lossless 5.1 sound was built', String(made.stderr ?? '').slice(0, 200))

await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  const files = await (await fetch(`${BASE}/api/local/files`)).json()
  file = (files.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show - 02'))
}
check(!!file, 'the file was scanned')
if (file) await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: 2 })

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
let plan
page.on('response', async (res) => {
  if (res.url().includes('/api/stream/open')) plan = (await res.json().catch(() => ({}))).plan
})

check(await page.evaluate(() => MediaSource.isTypeSupported('audio/mp4; codecs="flac"')), 'the browser plays FLAC')
await page.goto(`${BASE}/watch/${ANIME}/2`, { waitUntil: 'domcontentloaded' })
const video = page.locator('video').first()
await video.waitFor({ timeout: 30_000 })
await page.waitForFunction(() => (document.querySelector('video')?.currentTime ?? 0) > 4, null, { timeout: 40_000 }).catch(() => {})

check(!!plan && plan.audioCopy === false && plan.audioCodec === 'flac', 'the sound is converted to FLAC, not to a lossy format', JSON.stringify(plan))
const state = await video.evaluate((v) => ({
  t: +v.currentTime.toFixed(1), paused: v.paused, ready: v.readyState,
  error: v.error ? `${v.error.code} ${v.error.message}` : null,
  audioBytes: v.webkitAudioDecodedByteCount ?? -1, videoBytes: v.webkitVideoDecodedByteCount ?? -1,
}))
check(state.t >= 4 && !state.error, 'the episode plays', JSON.stringify(state))
check(state.audioBytes > 0, 'its sound is decoded, not only its picture', JSON.stringify(state))

// A seek restarts the conversion mid-file; the sound must come back with the picture.
await video.evaluate((v) => (v.currentTime = 40))
await page.waitForFunction(() => (document.querySelector('video')?.currentTime ?? 0) > 43, null, { timeout: 40_000 }).catch(() => {})
const after = await video.evaluate((v) => ({ t: +v.currentTime.toFixed(1), error: v.error ? v.error.message : null, audioBytes: v.webkitAudioDecodedByteCount ?? -1 }))
check(after.t >= 43 && !after.error && after.audioBytes > state.audioBytes, 'it plays on with sound after a seek', JSON.stringify(after))

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
