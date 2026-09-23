// Entering and leaving fullscreen keeps the picture: exiting once showed a
// black screen while the episode kept playing.
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = process.env.KURO_ANIME ?? '127230'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

check(
  await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }),
  'test episode ready',
)

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'load', timeout: 120000 })
const ready = await page
  .waitForFunction(() => (document.querySelector('video')?.readyState ?? 0) >= 2, null, {
    timeout: 120000,
    polling: 500,
  })
  .then(() => true)
  .catch(() => false)
check(ready, 'the episode loads')
await page.evaluate(() => document.querySelector('video')?.play())
await page.waitForTimeout(3000)

const report = () =>
  page.evaluate(() => {
    const v = document.querySelector('video')
    if (!v) return { error: 'no video' }
    const box = v.getBoundingClientRect()
    const onTop = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
    return {
      fullscreen: !!document.fullscreenElement,
      width: Math.round(box.width),
      t: v.currentTime,
      paused: v.paused,
      // The subtitle layer is a transparent canvas; anything else is a cover.
      onTop: onTop?.tagName ?? 'none',
    }
  })

const before = await report()
check(!before.paused && before.width > 0, 'playing in the page', JSON.stringify(before))

// The player's own button, as a viewer would: the mouse wakes the hidden controls first.
await page.mouse.move(700, 400)
await page.mouse.move(720, 420)
await page.getByRole('button', { name: 'Fullscreen' }).click()
await page.waitForTimeout(2000)
const inside = await report()
check(inside.fullscreen, 'fullscreen entered', JSON.stringify(inside))
check(!inside.paused && inside.t > before.t, 'still playing in fullscreen')
await page.screenshot({ path: `${SHOTS}/fullscreen-in.png` })

await page.evaluate(() => document.exitFullscreen())
await page.waitForTimeout(2500)
const after = await report()
check(!after.fullscreen, 'fullscreen left')
check(!after.paused && after.t > inside.t, 'still playing after', JSON.stringify(after))
check(['VIDEO', 'CANVAS'].includes(after.onTop), 'the picture is what sits on top', after.onTop)
await page.screenshot({ path: `${SHOTS}/fullscreen-out.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
