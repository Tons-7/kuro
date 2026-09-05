// The new controls, exercised the way a person would.
import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1600, height: 1000 } })
p.on('pageerror', e => console.log('PAGE ERROR:', e.message.slice(0,200)))

await p.goto('http://127.0.0.1:4321/', { waitUntil:'load', timeout:60000 })
await p.waitForTimeout(9000)

// Notification panel: opens in place rather than navigating.
const before = new URL(p.url()).pathname
await p.getByRole('button', { name: /notification/i }).click()
await p.waitForTimeout(900)
const panel = p.locator('[data-portal-menu]').first()
console.log('panel opens       :', await panel.isVisible().catch(() => false))
console.log('stayed on page    :', new URL(p.url()).pathname === before)
console.log('panel offers      :', (await panel.innerText()).split('\n').filter(Boolean).slice(0,4).join(' | '))
await p.keyboard.press('Escape')

// Profile menu.
await p.getByRole('button', { name: /profile/i }).click()
await p.waitForTimeout(700)
const menu = p.locator('[role=menu]').first()
console.log('profile items     :', (await menu.getByRole('menuitem').count()))

await p.keyboard.press('Escape')

// Genre filter: two columns, searchable.
await p.goto('http://127.0.0.1:4321/browse', { waitUntil:'load', timeout:60000 })
await p.waitForTimeout(7000)
await p.getByRole('button', { name: /^Genre/ }).click()
await p.waitForTimeout(700)
const box = await p.locator('[role=listbox]').first().boundingBox()
console.log('genre panel width :', Math.round(box?.width ?? 0))
await p.locator('[role=listbox] input').fill('rom')
await p.waitForTimeout(500)
console.log('after typing "rom":', await p.locator('[role=listbox] [role=option]').allInnerTexts())
await b.close()
