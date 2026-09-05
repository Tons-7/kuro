// Does the page update itself, or does it sit frozen?
import { chromium } from 'playwright'

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1500, height: 950 } })
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 250)))

await page.goto('http://127.0.0.1:4321' + (process.argv[2] ?? '/'), {
  waitUntil: 'load',
  timeout: 60000,
})
await page.waitForTimeout(12000)

const read = () =>
  page.evaluate(() => {
    const el = document.body.innerText
    // The relative timestamps are what should advance on their own.
    return (el.match(/\d+h ?\d*m? (?:ago|from now)|in \d+h ?\d*m/g) ?? []).slice(0, 4)
  })

const before = await read()
console.log('t=0   ', JSON.stringify(before))

// The ticker fires every 30s.
await page.waitForTimeout(70000)
const after = await read()
console.log('t=70s ', JSON.stringify(after))

const moved = JSON.stringify(before) !== JSON.stringify(after)
console.log(moved ? 'LIVE: timestamps advanced on their own' : 'FROZEN: nothing changed')

await browser.close()
process.exit(moved ? 0 : 1)
