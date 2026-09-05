// Screenshot one page, after giving it a moment to settle.
import { chromium } from 'playwright'

const base = 'http://127.0.0.1:4321'
const [, , path = '/', out = 'shot.png', waitMs = '4000'] = process.argv

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
page.on('console', (m) => m.type() === 'error' && console.log('console:', m.text().slice(0, 200)))
page.on('pageerror', (e) => console.log('uncaught:', e.message.slice(0, 200)))

await page.goto(base + path, { waitUntil: 'networkidle', timeout: 45000 }).catch(() => {})
await page.waitForTimeout(Number(waitMs))
await page.screenshot({ path: out, fullPage: false })
await browser.close()
console.log('wrote', out)
