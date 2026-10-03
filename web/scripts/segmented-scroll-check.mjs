// Toggling Anime4K in its dialog must not scroll the dialog, and a strip's selected tab stays in view.
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

check(await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }), 'test episode ready')

const browser = await chromium.launch()
// A phone: short enough that the dialog has to scroll.
const page = await browser.newPage({ viewport: { width: 412, height: 700 }, isMobile: true, hasTouch: true })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
await page.getByRole('button', { name: /^Anime4K/ }).click()
const dialog = page.getByRole('dialog', { name: 'Anime4K' })
await dialog.waitFor()

const scrolls = () => dialog.evaluate((d) => ({ dialog: d.scrollTop, page: window.scrollY, can: d.scrollHeight > d.clientHeight }))
const start = await scrolls()
check(start.can, 'the dialog is taller than the screen, so it can scroll')
check(start.dialog === 0, 'the dialog opens at its top', JSON.stringify(start))

const power = dialog.getByRole('switch', { name: 'Anime4K upscaling' })
for (const state of ['on', 'off', 'on']) {
  await power.click()
  await sleep(300)
  const now = await scrolls()
  check(now.dialog === 0 && now.page === start.page, `switching it ${state} leaves the dialog where it was`, JSON.stringify(now))
}

// The strip still brings its own selection into view, sideways.
await dialog.getByRole('tab', { name: 'Full' }).scrollIntoViewIfNeeded()
await dialog.getByRole('tab', { name: 'Full' }).click()
await sleep(300)
const visible = await dialog.getByRole('tab', { name: 'Full' }).evaluate((el) => {
  const tab = el.getBoundingClientRect()
  const strip = el.parentElement.getBoundingClientRect()
  return tab.left >= strip.left - 1 && tab.right <= strip.right + 1
})
check(visible, 'the selected choice is inside its strip')

await browser.close()
console.log(failures ? `${failures} failed` : 'all passed')
process.exit(failures ? 1 : 0)
