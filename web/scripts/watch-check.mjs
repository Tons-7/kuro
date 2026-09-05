// Opens a watch page and reports what the user actually sees, and when.
import { chromium } from 'playwright'

const base = 'http://127.0.0.1:4321'
const target = process.argv[2] ?? '/watch/201514/1'

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })

page.on('console', (m) => m.type() === 'error' && console.log('  console:', m.text().slice(0, 200)))
page.on('pageerror', (e) => console.log('  uncaught:', e.message.slice(0, 200)))
page.on('response', (r) => {
  const u = new URL(r.url())
  if (u.host === new URL(base).host && u.pathname.startsWith('/api/')) {
    console.log(`  ${r.status()} ${u.pathname}`)
  }
})

const start = Date.now()
await page.goto(base + target, { waitUntil: 'domcontentloaded' })

// Sample what the player area says over time.
let last = ''
for (let i = 0; i < 24; i++) {
  await page.waitForTimeout(2500)
  const text = (await page.evaluate(() => document.body.innerText))
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
    .slice(0, 6)
    .join(' | ')
  if (text !== last) {
    console.log(`t+${((Date.now() - start) / 1000).toFixed(0)}s  ${text.slice(0, 180)}`)
    last = text
  }
  const settled = await page.evaluate(
    () => !document.body.innerText.includes('Searching for a release'),
  )
  if (settled) break
}

await page.screenshot({ path: process.argv[3] ?? 'watch.png' })
await browser.close()
