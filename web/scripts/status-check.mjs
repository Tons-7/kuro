// Setting a list status has to survive a reload, not just close the menu.
import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1500, height: 1000 } })
p.on('pageerror', e => console.log('PAGE ERROR:', e.message.slice(0,200)))
p.on('response', r => { if (r.url().includes('/api/status')) console.log('POST /api/status ->', r.status()) })

await p.goto('http://127.0.0.1:4321/anime/1535', { waitUntil:'load', timeout:60000 })
await p.waitForTimeout(9000)

const trigger = p.getByRole('button', { name: /Add to list|Listed as/ })
console.log('button reads      :', (await trigger.first().innerText()).trim())
await trigger.first().click()
await p.waitForTimeout(700)

const items = p.locator('[role=menu] [role=menuitem]')
console.log('statuses offered  :', await items.count())
await p.getByRole('menuitem', { name: 'Completed' }).click()
await p.waitForTimeout(2500)
console.log('button now reads  :', (await trigger.first().innerText()).trim())

await p.reload({ waitUntil: 'load' })
await p.waitForTimeout(9000)
console.log('after reload      :', (await p.getByRole('button', { name: /Add to list|Listed as/ }).first().innerText()).trim())
await b.close()
