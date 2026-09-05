import { chromium } from 'playwright'

const browser = await chromium.launch()
const page = await browser.newPage()
page.on('console', (m) => m.type() === 'error' && console.log('console:', m.text().slice(0, 300)))
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 400)))
page.on('requestfailed', (r) => console.log('FAILED REQ:', r.url().slice(0, 120), r.failure()?.errorText))

const res = await page.goto('http://127.0.0.1:4321' + (process.argv[2] ?? '/'), {
  waitUntil: 'load',
  timeout: 45000,
})
console.log('status:', res?.status())
await page.waitForTimeout(4000)
console.log('body length:', (await page.evaluate(() => document.body.innerText)).length)
await browser.close()
