// Screenshots of every page at desktop and phone width, for reviewing the UI,
// and a check that none of them throws or scrolls sideways.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'
const ANIME = process.env.KURO_ANIME ?? '127230'
const ONLY = process.env.TOUR ? process.env.TOUR.split(',') : null

const pages = [
  ['home', '/'],
  ['browse', '/browse'],
  ['browse-filtered', '/browse?genres=Action&formats=TV&sort=score'],
  ['anime', `/anime/${ANIME}`],
  ['watch', `/watch/${ANIME}/1`],
  ['library', '/library'],
  ['history', '/history'],
  ['schedule', '/schedule'],
  ['recent', '/recent'],
  ['downloads', '/downloads'],
  ['local', '/local'],
  ['settings', '/settings'],
  ['settings-quality', '/settings?tab=Quality'],
  ['settings-access', '/settings?tab=Access'],
  ['setup', '/setup'],
].filter(([name]) => !ONLY || ONLY.includes(name))

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const browser = await chromium.launch()
for (const [width, height, tag] of [
  [1440, 900, 'desktop'],
  [390, 844, 'phone'],
]) {
  const page = await browser.newPage({ viewport: { width, height } })
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))
  for (const [name, path] of pages) {
    errors.length = 0
    await page.goto(`${BASE}${path}`, { waitUntil: 'domcontentloaded' })
    // AniList-backed pages start cold in a fresh harness.
    await page.waitForTimeout(['watch', 'home', 'anime', 'browse', 'browse-filtered'].includes(name) ? 12000 : 5000)
    await page.screenshot({ path: `${SHOTS}/tour-${tag}-${name}.png`, fullPage: name !== 'watch' })
    const sideways = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    check(sideways <= 1, `${tag} ${name}: no sideways scroll`, sideways > 1 ? `${sideways}px too wide` : '')
    check(errors.length === 0, `${tag} ${name}: no page errors`, errors.join(' | '))
  }
  await page.close()
}
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
