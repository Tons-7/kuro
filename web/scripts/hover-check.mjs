// Resting on a card should show its details and offer a way to the series.
import { chromium } from 'playwright'

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1500, height: 950 } })
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 250)))

await page.goto('http://127.0.0.1:4321/', { waitUntil: 'load', timeout: 60000 })
await page.waitForTimeout(9000)

// Not the first /watch/ link on the page: that one is the hero's play button.
const card = page.locator('section:has-text("Recently released") a[href^="/watch/"]').first()
console.log('card plays                :', await card.getAttribute('href'))

await card.hover()
await page.waitForTimeout(1400)

const panel = page.locator('.fixed.z-50').first()
console.log('panel shown               :', await panel.isVisible().catch(() => false))
console.log('panel says                :', (await panel.innerText()).replace(/\n/g, ' | '))

const series = panel.locator('a[href^="/anime/"]').first()
console.log('panel links to series     :', await series.getAttribute('href'))
await series.click()
await page.waitForTimeout(2500)
console.log('and lands on              :', new URL(page.url()).pathname)

await page.goBack()
await page.waitForTimeout(6000)
const caption = page
  .locator('section:has-text("Recently released") a[href^="/anime/"]')
  .first()
console.log('caption under card links  :', await caption.getAttribute('href'))

await browser.close()
