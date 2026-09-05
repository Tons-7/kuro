// Range dropdown on a long show, and the expandable synopsis.
import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1500, height: 1000 } })
p.on('pageerror', e => console.log('PAGE ERROR:', e.message.slice(0,200)))

// One Piece: over a thousand episodes.
await p.goto('http://127.0.0.1:4321/anime/21', { waitUntil:'load', timeout:60000 })
await p.waitForTimeout(12000)

const range = p.getByRole('button', { name: /of \d+/ })
console.log('range button      :', (await range.count()) ? (await range.first().innerText()).replace(/\n/g,' ') : 'ABSENT')
if (await range.count()) {
  await range.first().click()
  await p.waitForTimeout(800)
  const opts = p.locator('[role=listbox] [role=option]')
  console.log('ranges offered    :', await opts.count())
  const last = (await opts.count()) - 1
  console.log('last range        :', await opts.nth(last).innerText())
  await opts.nth(last).click()
  await p.waitForTimeout(1200)
  console.log('after picking last:', (await range.first().innerText()).replace(/\n/g,' '))
}

const more = p.getByRole('button', { name: 'Show more' })
console.log('show more present :', await more.count() > 0)
if (await more.count()) {
  const before = (await p.locator('main p').first().innerText()).length
  await more.click()
  await p.waitForTimeout(500)
  const after = (await p.locator('main p').first().innerText()).length
  console.log('toggles to        :', await p.getByRole('button', { name: 'Show less' }).count() > 0 ? 'Show less' : 'nothing')
  console.log('text length       :', before, '->', after)
}
await b.close()
