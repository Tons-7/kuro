// Report how much artwork a page pulls, and how much of it arrives.
import { chromium } from 'playwright'

const base = 'http://127.0.0.1:4321'
const [, , path = '/', waitMs = '12000'] = process.argv

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })

let bytes = 0
let requests = 0
page.on('response', async (res) => {
  if (!/anilistcdn|\.(png|jpg|jpeg|webp)/i.test(res.url())) return
  requests++
  const len = Number(res.headers()['content-length'] ?? 0)
  bytes += len
})

await page.goto(base + path, { waitUntil: 'networkidle', timeout: 60000 }).catch(() => {})
await page.waitForTimeout(3000)
await page.evaluate(() => window.scrollTo({ top: 620 }))
await page.waitForTimeout(Number(waitMs))

const imgs = await page.evaluate(() =>
  Array.from(document.images)
    .filter((i) => i.currentSrc)
    .map((i) => i.complete && i.naturalWidth > 0),
)
console.log(`image requests: ${requests}, bytes: ${(bytes / 1024 / 1024).toFixed(2)} MB`)
console.log(`with a src: ${imgs.length}, rendered: ${imgs.filter(Boolean).length}`)

await browser.close()
