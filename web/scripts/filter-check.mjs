// Multi-select was the point: picking two genres has to send both.
import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1500, height: 950 } })
p.on('pageerror', e => console.log('PAGE ERROR:', e.message.slice(0,200)))
await p.goto('http://127.0.0.1:4321/browse', { waitUntil:'load', timeout:60000 })
await p.waitForTimeout(6000)

await p.getByRole('button', { name: /^Genre/ }).click()
await p.waitForTimeout(600)
const panel = p.locator('[role=listbox]')
console.log('panel opened      :', await panel.isVisible())

const options = panel.getByRole('option')
console.log('options           :', await options.count())
await options.nth(0).click()
await p.waitForTimeout(400)
await options.nth(1).click()
await p.waitForTimeout(1500)

console.log('url after two     :', new URL(p.url()).search)
console.log('trigger reads     :', await p.getByRole('button', { name: /^(Genre|action|adventure)/i }).first().innerText())

await p.keyboard.press('Escape')
await p.waitForTimeout(2500)
const cards = await p.locator('a[href^="/anime/"]').count()
console.log('results rendered  :', cards > 0)
await b.close()
