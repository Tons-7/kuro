// Audits every picture the main pages show: broken, stretched, or drawn larger than the file it came from.
// Reads live catalogue data, so it is run by hand: PAGES=/a,/b overrides the pages, DPR sets the screen density.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'
const DPR = Number(process.env.DPR ?? 1)
const PAGES = (process.env.PAGES ?? '/,/browse,/schedule,/anime/16498,/anime/21519,/anime/5204,/anime/14807,/anime/154587')
  .split(',')
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: DPR })
const failed = []
page.on('response', (r) => {
  if (r.request().resourceType() === 'image' && r.status() >= 400) failed.push(`${r.status()} ${r.url().slice(0, 110)}`)
})
page.on('requestfailed', (r) => {
  if (r.resourceType() === 'image') failed.push(`failed ${r.url().slice(0, 110)}`)
})

let problems = 0
for (const path of PAGES) {
  failed.length = 0
  await page.goto(`${BASE}${path}`, { waitUntil: 'domcontentloaded' })
  await sleep(5000)
  // Lazy pictures load as they near the screen.
  const height = await page.evaluate(() => document.documentElement.scrollHeight)
  for (let y = 0; y < height; y += 700) {
    await page.evaluate((to) => window.scrollTo(0, to), y)
    await sleep(350)
  }
  await sleep(2500)

  const found = await page.evaluate(async (dpr) => {
    // A picture chosen from a 2x set reports half its width; the file loaded on its own reports all of it.
    const fileWidth = (src) =>
      new Promise((resolve) => {
        const probe = new Image()
        probe.onload = () => resolve(probe.naturalWidth)
        probe.onerror = () => resolve(0)
        probe.src = src
      })
    const widths = new Map()
    for (const img of document.querySelectorAll('img[srcset]')) {
      if (img.currentSrc) widths.set(img, await fileWidth(img.currentSrc))
    }
    const host = (src) => {
      try {
        return new URL(src, location.href).host
      } catch {
        return '?'
      }
    }
    const imgs = [...document.querySelectorAll('img')].map((img) => {
      const box = img.getBoundingClientRect()
      const fit = getComputedStyle(img).objectFit
      const natural = img.naturalWidth / (img.naturalHeight || 1)
      const shown = box.width / (box.height || 1)
      return {
        src: img.currentSrc || img.src,
        host: host(img.currentSrc || img.src),
        natural: [img.naturalWidth, img.naturalHeight],
        shown: [Math.round(box.width), Math.round(box.height)],
        visible: box.width > 0 && box.height > 0,
        broken: img.complete && img.naturalWidth === 0,
        pending: !img.complete,
        // Drawn with more screen pixels than the file has.
        stretch: img.naturalWidth ? (box.width * dpr) / (widths.get(img) || img.naturalWidth) : 0,
        // "fill" squashes a picture whose shape differs from its box; cover and contain do not.
        distorted: fit === 'fill' && img.naturalWidth > 0 && Math.abs(natural - shown) / natural > 0.06,
        // How much of the picture a cover crop throws away.
        cropped: fit === 'cover' && img.naturalWidth > 0 ? 1 - Math.min(natural / shown, shown / natural) : 0,
        fit,
        // A backdrop blurred on purpose is not expected to be sharp.
        blurred: getComputedStyle(img).filter.includes('blur'),
        where: (img.closest('[aria-label]')?.getAttribute('aria-label') ?? img.alt ?? '').slice(0, 40),
      }
    })
    const backgrounds = [...document.querySelectorAll('*')]
      .map((el) => getComputedStyle(el).backgroundImage)
      .filter((b) => b.includes('url('))
      .map((b) => b.match(/url\("?([^")]+)"?\)/)?.[1])
      .filter(Boolean)
    return { imgs, backgrounds: [...new Set(backgrounds)] }
  }, DPR)

  const shown = found.imgs.filter((i) => i.visible)
  const broken = shown.filter((i) => i.broken)
  const pending = shown.filter((i) => i.pending)
  // Banners are exempt: 1900 wide is the largest the catalogue has.
  const soft = shown.filter((i) => i.stretch > 1.3 && !i.blurred && !i.src.includes('/banner/'))
  const distorted = shown.filter((i) => i.distorted)
  const cropped = shown.filter((i) => i.cropped > 0.35)
  problems += broken.length + distorted.length + failed.length + soft.length

  const hosts = {}
  for (const i of shown) hosts[i.host] = (hosts[i.host] ?? 0) + 1
  console.log(`\n== ${path}: ${shown.length} pictures (${found.imgs.length - shown.length} not on screen), ${found.backgrounds.length} backgrounds`)
  console.log(`   from: ${Object.entries(hosts).map(([h, n]) => `${h} ×${n}`).join(', ')}`)
  console.log(`   broken ${broken.length} · never loaded ${pending.length} · failed requests ${failed.length} · stretched ${soft.length} · squashed ${distorted.length} · heavily cropped ${cropped.length}`)
  const line = (i, extra) => `     ${extra} | file ${i.natural.join('×')} shown ${i.shown.join('×')} | ${i.where} | ${i.src.slice(0, 90)}`
  for (const i of broken.slice(0, 6)) console.log(line(i, 'BROKEN'))
  for (const i of pending.slice(0, 4)) console.log(line(i, 'pending'))
  for (const i of distorted.slice(0, 6)) console.log(line(i, 'SQUASHED'))
  for (const i of soft.toSorted((a, b) => b.stretch - a.stretch).slice(0, 6)) console.log(line(i, `stretched ×${i.stretch.toFixed(2)}`))
  for (const i of cropped.toSorted((a, b) => b.cropped - a.cropped).slice(0, 4)) console.log(line(i, `cropped ${Math.round(i.cropped * 100)}%`))
  for (const f of [...new Set(failed)].slice(0, 6)) console.log(`     request ${f}`)
  for (const b of found.backgrounds.slice(0, 4)) console.log(`     background ${b.slice(0, 100)}`)

  await page.evaluate(() => window.scrollTo(0, 0))
  await sleep(300)
  await page.screenshot({ path: `${SHOTS}/images${path.replace(/\W+/g, '-') || '-home'}.png` })
}

await browser.close()
console.log(`\n${problems} broken, squashed, stretched or failed`)
process.exit(problems ? 1 : 0)
