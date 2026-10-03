// Subtitle size and delay: the menu's size steps grow the dialogue, z/x shift
// cues in time (a cue shows in what was a gap), and the size is remembered.
//   node scripts/run-player-check.mjs subtitle-look-check.mjs
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = process.env.KURO_ANIME ?? '127230'
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

const video = () => page.locator('video')
async function waitPlaying() {
  for (let i = 0; i < 60; i++) {
    await sleep(1000)
    const s = await page.evaluate(() => {
      const v = document.querySelector('video')
      return v && { t: v.currentTime, paused: v.paused, ready: v.readyState }
    })
    if (s && s.t > 1 && !s.paused) return true
    if (s && s.paused && s.ready >= 3) await page.evaluate(() => document.querySelector('video')?.play().catch(() => {}))
  }
  return false
}

// Paused at t, so every shot is of the same frame.
async function holdAt(t) {
  await page.evaluate((at) => {
    const v = document.querySelector('video')
    v.pause()
    v.currentTime = at
  }, t)
  await sleep(2500)
}

// The lower third, where dialogue sits, with the controls in the same state
// for every shot: a key press shows them, which would read as a change.
async function shot(name) {
  const box = await video().boundingBox()
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await sleep(600)
  const clip = { x: box.x, y: box.y + box.height * 0.66, width: box.width, height: box.height * 0.34 }
  return (await page.screenshot({ clip, path: `${SHOTS}/${name}.png` })).toString('base64')
}

// Pixels that differ between two shots of the same frame.
function changed(a, b) {
  return page.evaluate(
    async ([a, b]) => {
      const read = async (data) => {
        const img = new Image()
        img.src = `data:image/png;base64,${data}`
        await img.decode()
        const c = document.createElement('canvas')
        c.width = img.width
        c.height = img.height
        const ctx = c.getContext('2d')
        ctx.drawImage(img, 0, 0)
        return ctx.getImageData(0, 0, c.width, c.height).data
      }
      const [pa, pb] = await Promise.all([read(a), read(b)])
      let n = 0
      for (let i = 0; i < pa.length; i += 4) {
        if (Math.abs(pa[i] - pb[i]) + Math.abs(pa[i + 1] - pb[i + 1]) + Math.abs(pa[i + 2] - pb[i + 2]) > 60) n++
      }
      return n
    },
    [a, b],
  )
}

const openMenu = async () => {
  await page.getByTitle('Subtitles').click()
  await page.getByRole('menu').waitFor({ timeout: 5000 })
}

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await waitPlaying(), 'playback started')
// Settles the subtitle renderer before measuring.
await sleep(3000)

// A cue covers 7–11 s.
await holdAt(8.5)
const normal = await shot('look-size-100')
await openMenu()
await page.getByRole('menuitem', { name: 'Size up' }).click()
await page.getByRole('menuitem', { name: 'Size up' }).click()
check(await page.getByRole('menu').getByText('130%').isVisible(), 'size reads 130%')
await page.keyboard.press('Escape')
await sleep(1500)
const bigger = await shot('look-size-130')
const grown = await changed(normal, bigger)
check(grown > 1000, 'larger size redraws the dialogue', `${grown} px changed`)

await page.reload({ waitUntil: 'domcontentloaded' })
check(await waitPlaying(), 'playback restarted after reload')
await openMenu()
check(await page.getByRole('menu').getByText('130%').isVisible(), 'size is remembered across loads')
await page.getByRole('menuitem', { name: 'Reset size' }).click()
check(await page.getByRole('menu').getByText('100%').isVisible(), 'reset returns to 100%')
await page.keyboard.press('Escape')
await sleep(1000)

// 6.5 s falls between the 2–6 s and 7–11 s cues.
await holdAt(6.5)
const gap = await shot('look-delay-0')
const steady = await changed(gap, await shot('look-delay-0-again'))
for (let i = 0; i < 10; i++) await page.keyboard.press('x')
check(await page.getByText('Subtitle delay +1.0 s').isVisible(), 'x shows the new delay')
await sleep(1500)
const delayed = await changed(gap, await shot('look-delay-plus1'))
check(delayed > steady + 1000, 'a +1 s delay shows the earlier cue in the gap', `${delayed} px changed (noise ${steady})`)
for (let i = 0; i < 10; i++) await page.keyboard.press('z')
await sleep(1500)
const back = await changed(gap, await shot('look-delay-back'))
check(back < 1000, 'z brings it back', `${back} px changed`)

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
