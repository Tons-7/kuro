// Renders public/icon.svg to the PNGs Android wants to install kuro as an app.
// Run from web/ after changing the icon: node scripts/make-icons.mjs
import { readFileSync } from 'node:fs'
import { chromium } from 'playwright'

const svg = readFileSync('public/icon.svg', 'utf8')
// Maskable: the launcher cuts its own shape, so the background runs to the edge.
const fullBleed = svg.replace(' rx="112"', '')

const browser = await chromium.launch()
for (const [name, size, source] of [
  ['icon-192.png', 192, svg],
  ['icon-512.png', 512, svg],
  ['icon-maskable-512.png', 512, fullBleed],
]) {
  const page = await browser.newPage({ viewport: { width: size, height: size } })
  await page.setContent(`<style>body{margin:0}svg{display:block;width:${size}px;height:${size}px}</style>${source}`)
  await page.screenshot({ path: `public/${name}`, omitBackground: true })
  await page.close()
  console.log(`public/${name}`)
}
await browser.close()
