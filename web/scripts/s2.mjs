import { chromium } from 'playwright'
import { mkdirSync } from 'fs'
const OUT = process.env.SHOT_DIR
mkdirSync(OUT, { recursive: true })
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1600, height: 1000 } })
p.on('pageerror', e => console.log('PAGE ERROR:', e.message.slice(0,160)))
for (const [name, path] of [['recent','/recent'],['browse','/browse'],['settings','/settings'],['home','/']]) {
  await p.goto('http://127.0.0.1:4321'+path, { waitUntil:'load', timeout:60000 })
  await p.waitForTimeout(14000)
  await p.screenshot({ path: `${OUT}/${name}.png` })
  console.log('captured', name)
}
await b.close()
