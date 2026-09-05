// Drive the first-run screen the way someone receiving the app would: open it
// with something missing, press the button, watch it land.
import { chromium } from 'playwright'

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 200)))

await page.goto('http://127.0.0.1:4321/setup', { waitUntil: 'load', timeout: 60000 })
await page.waitForTimeout(4000)

const rowFor = (label) => page.locator('li').filter({ hasText: label }).first()
const shaders = rowFor('Anime4K shaders')
console.log('row found            :', await shaders.count())
console.log('shows it is missing  :', (await shaders.innerText()).includes('Install'))

const install = shaders.getByRole('button', { name: /install|try again/i })
await install.click()
console.log('clicked install')

// The page polls while it runs, so progress should appear without a reload.
let sawProgress = false
for (let i = 0; i < 40; i++) {
  await page.waitForTimeout(1500)
  const text = await shaders.innerText()
  if (/Downloading|Extracting|Looking up/.test(text)) {
    if (!sawProgress) console.log('progress shown       :', text.split('\n').pop())
    sawProgress = true
  }
  if (!/Install|Downloading|Extracting|Looking up/.test(text)) break
}

await page.waitForTimeout(2000)
console.log('progress was shown   :', sawProgress)
console.log('row now reads        :', (await shaders.innerText()).replace(/\n/g, ' | ').slice(0, 90))

const done = await page.locator('text=Everything is installed.').count()
console.log('all installed panel  :', done > 0)

await page.screenshot({ path: 'setup-after.png' })
await browser.close()
