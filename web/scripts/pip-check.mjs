// Picture in picture: the compact player moves into the floating window with
// its controls working and the subtitles still drawn, and comes back.
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

check(await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }), 'test episode ready')

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const context = await browser.newContext({ viewport: { width: 1400, height: 900 } })
const page = await context.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'load' })
await page.waitForFunction(() => (document.querySelector('video')?.readyState ?? 0) >= 2, null, { timeout: 120000 })

const supported = await page.evaluate(() => 'documentPictureInPicture' in window)
if (!supported) {
  console.log('SKIP document picture-in-picture is not available in this browser build')
  await browser.close()
  process.exit(0)
}

const opened = context.waitForEvent('page', { timeout: 15000 }).catch(() => null)
await page.hover('video')
await page.getByRole('button', { name: 'Picture in picture' }).click()
const pip = await opened
check(!!pip, 'a floating window opens')
if (pip) {
  await pip.waitForTimeout(2500)
  const state = await pip.evaluate(() => {
    const v = document.querySelector('video')
    return {
      video: !!v,
      playing: !!v && !v.paused,
      subtitleCanvas: document.querySelectorAll('canvas').length,
      back: !!Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('Back to tab')),
      fullscreenButton: !!document.querySelector('button[aria-label="Fullscreen"]'),
      fontsLoaded: document.fonts.check('12px "Inter Variable"'),
    }
  })
  check(state.video && state.playing, 'the episode plays in the window', JSON.stringify(state))
  check(state.subtitleCanvas > 0, 'subtitles are drawn there')
  check(state.back, 'a way back to the tab is offered')
  check(!state.fullscreenButton, 'controls that do not fit a small window are left out')
  check(state.fontsLoaded, 'the app fonts load in the window')

  // A delegated React handler has to work in the other document.
  const before = await pip.evaluate(() => document.querySelector('video').currentTime)
  await pip.hover('video')
  await pip.getByRole('button', { name: 'Forward 10 seconds' }).click()
  await pip.waitForTimeout(800)
  const after = await pip.evaluate(() => document.querySelector('video').currentTime)
  check(after - before > 5, 'its buttons work', `${before.toFixed(1)} → ${after.toFixed(1)}`)
  await pip.screenshot({ path: `${SHOTS}/pip.png` })

  await pip.getByRole('button', { name: /Back to tab/ }).click()
  await page.waitForTimeout(1500)
  check(
    await page.evaluate(() => !!document.querySelector('main video')),
    'the player is back in the page',
  )
}

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
