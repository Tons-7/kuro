// Capture the main screens so they can be judged side by side.
import { chromium } from 'playwright'
import { mkdirSync } from 'fs'

const OUT = process.env.SHOT_DIR ?? 'shots'
mkdirSync(OUT, { recursive: true })

const PAGES = [
  ['home', '/'],
  ['browse', '/browse'],
  ['schedule', '/schedule'],
  ['library', '/library'],
  ['anime', '/anime/154587'],
  ['settings', '/settings'],
  ['manage', '/manage'],
]

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 160)))

for (const [name, path] of PAGES) {
  try {
    await page.goto('http://127.0.0.1:4321' + path, { waitUntil: 'load', timeout: 60000 })
    await page.waitForTimeout(Number(process.env.SHOT_WAIT ?? 7000))
    await page.screenshot({ path: `${OUT}/${name}.png` })
    console.log('captured', name)
  } catch (e) {
    console.log('failed', name, String(e.message ?? e).slice(0, 80))
  }
}

await browser.close()
