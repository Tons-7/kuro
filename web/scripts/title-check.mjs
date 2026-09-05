// EN/JP must actually change the names, not just the pill.
import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1600, height: 1000 } })
p.on('pageerror', e => console.log('PAGE ERROR:', e.message.slice(0,200)))
await p.goto('http://127.0.0.1:4321/browse', { waitUntil:'load', timeout:60000 })
await p.waitForTimeout(9000)

const names = async () =>
  (await p.locator('a[href^="/anime/"] p').allInnerTexts()).slice(0, 4)

console.log('EN:', JSON.stringify(await names()))
await p.getByRole('button', { name: 'JP', exact: true }).click()
await p.waitForTimeout(6000)
console.log('JP:', JSON.stringify(await names()))
await p.getByRole('button', { name: 'EN', exact: true }).click()
await p.waitForTimeout(6000)
console.log('back to EN:', JSON.stringify(await names()))
await b.close()
