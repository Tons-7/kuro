// While an episode starts, the player says what it is waiting on; once playing, the seek bar shows what is loaded.
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.SHOTS ?? '.'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

check(await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }), 'test episode ready')

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

// The play request is held open, as a slow release holds it, while the stage moves on.
let stage = { step: 'search' }
let release
const held = new Promise((r) => (release = r))
await page.route('**/api/play', async (route) => {
  await held
  await route.continue()
})
await page.route('**/api/play/stage**', (route) => route.fulfill({ json: stage }))

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
const says = (text) => page.getByText(text).first().waitFor({ timeout: 6000 }).then(() => true, () => false)

check(await says('Searching for a release…'), 'it starts by saying it is searching')
stage = { step: 'files', release: '[Group] Show - 01 [1080p].mkv' }
check(await says('Getting the file list…'), 'then that it is getting the file list')
check(await says('[Group] Show - 01 [1080p].mkv'), 'and names the release')
stage = { step: 'data', release: '[Group] Show - 01 [1080p].mkv', peers: 2, bytes: 3 * 1024 * 1024 }
check(await says('Waiting for the first data…'), 'then that it is waiting for data')
check(await says(/2 peers, 3\.0 MB so far/), 'with the peers and what has arrived')
stage = { step: 'data', release: '[Group] Show - 01 [1080p].mkv', peers: 0, bytes: 0 }
check(await says(/no peers connected yet/), 'and says when nobody is connected')
stage = { step: 'start' }
check(await says('Reading the start of the file…'), 'then that it is reading the start of the file')
await page.screenshot({ path: `${SHOTS}/starting-note.png` })

// Let it start: the notes go, and the bar shows what is loaded ahead of the playhead.
stage = {}
release()
const video = page.locator('video').first()
await video.waitFor({ timeout: 30_000 })
await page.waitForFunction(() => (document.querySelector('video')?.currentTime ?? 0) > 1, null, { timeout: 30_000 }).catch(() => {})
check((await page.getByText('Reading the start of the file…').count()) === 0, 'the note is gone once it plays')

await sleep(1500)
const bar = await page.evaluate(() => {
  const v = document.querySelector('video')
  const parts = [...document.querySelectorAll('[data-loaded]')].map((el) => {
    const track = el.parentElement.getBoundingClientRect()
    const box = el.getBoundingClientRect()
    return { from: (box.left - track.left) / track.width, to: (box.right - track.left) / track.width }
  })
  return { parts, played: v.currentTime / v.duration, buffered: v.buffered.length ? v.buffered.end(v.buffered.length - 1) / v.duration : 0 }
})
const ahead = bar.parts.find((p) => p.to > bar.played)
check(bar.parts.length > 0, 'the seek bar draws the loaded part', JSON.stringify(bar))
// A library file is whole on disk, so the loaded part is the whole bar; the browser's buffer lies inside it.
check(!!ahead && ahead.to > 0.99 && bar.buffered <= ahead.to, 'it spans the whole file, with the browser buffer inside', JSON.stringify(bar))
await page.locator('video').first().hover()
await page.screenshot({ path: `${SHOTS}/loaded-bar.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
