// Scrub-preview thumbnails against a complete local episode: the sheet is
// built, its tiles carry a real picture rather than black, and hovering the
// progress bar shows the tile for that time.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const EP = 1
const shots = process.env.SHOTS ?? '.'

const api = async (path, init) => {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) },
  })
  const text = await res.text()
  let body
  try {
    body = JSON.parse(text)
  } catch {
    body = text
  }
  return { ok: res.ok, status: res.status, body }
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) })

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// ---------------------------------------------------------------- setup
for (let i = 0; i < 60; i++) {
  try {
    if ((await api('/api/setup')).ok) break
  } catch {}
  await sleep(1000)
}
check((await post('/api/local/paths', { paths: [LIB] })).ok, 'library path set')
check((await post('/api/local/scan', {})).ok, 'library scan started')
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  const files = await api('/api/local/files')
  file = (files.body.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show'))
}
check(!!file, 'test file listed')
if (!file) process.exit(1)
check((await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: EP })).ok, 'file assigned')
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })

// ---------------------------------------------------------------- open the player
const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
let streamOpen = null
page.on('response', (r) => {
  if (r.url().includes('/api/stream/open')) r.json().then((b) => { streamOpen = b }).catch(() => {})
})

await page.goto(`${BASE}/watch/${ANIME}/${EP}`, { waitUntil: 'domcontentloaded' })
for (let i = 0; i < 60 && !streamOpen; i++) await sleep(500)
const sid = streamOpen?.id ?? streamOpen?.session?.id ?? streamOpen?.streamId
check(!!sid, 'stream opened', JSON.stringify(streamOpen ?? {}).slice(0, 160))
if (!sid) process.exit(1)

// ---------------------------------------------------------------- the sheet
let sheet
for (let i = 0; i < 90; i++) {
  const r = await api(`/api/stream/${sid}/thumbnails`)
  if (r.ok && r.body?.ready) {
    sheet = r.body
    break
  }
  await sleep(2000)
}
check(!!sheet, 'sheet built for a complete local file', JSON.stringify(sheet ?? {}))
if (!sheet) {
  await browser.close()
  process.exit(1)
}
check(sheet.count > 1 && sheet.interval > 0 && sheet.columns > 0, 'sheet has several tiles', `${sheet.count} tiles every ${sheet.interval}s`)

// Every tile's mean brightness, read from the sheet the player will use.
const tiles = await page.evaluate(async ({ url, sheet }) => {
  const img = new Image()
  img.src = url
  await img.decode()
  const canvas = document.createElement('canvas')
  canvas.width = img.naturalWidth
  canvas.height = img.naturalHeight
  const ctx = canvas.getContext('2d')
  ctx.drawImage(img, 0, 0)
  const out = []
  for (let i = 0; i < sheet.count; i++) {
    const x = (i % sheet.columns) * sheet.width
    const y = Math.floor(i / sheet.columns) * sheet.height
    const px = ctx.getImageData(x, y, sheet.width, sheet.height).data
    let sum = 0
    for (let p = 0; p < px.length; p += 4) sum += (px[p] + px[p + 1] + px[p + 2]) / 3
    out.push(Math.round(sum / (px.length / 4)))
  }
  return { natural: [img.naturalWidth, img.naturalHeight], means: out }
}, { url: `${BASE}/api/stream/${sid}/thumbs.jpg`, sheet })
const dark = tiles.means.filter((m) => m < 16).length
check(dark === 0, 'no tile is black', `means ${tiles.means.join(',')}`)
check(
  tiles.natural[0] >= sheet.columns * sheet.width && tiles.natural[1] >= sheet.rows * sheet.height,
  'sheet image covers the declared grid',
  `${tiles.natural.join('x')} for ${sheet.columns}x${sheet.rows} of ${sheet.width}x${sheet.height}`,
)

// ---------------------------------------------------------------- hovering the bar
// A fresh mount asks for the sheet at once, which is what a viewer who opens
// an already-cached episode gets.
await page.reload({ waitUntil: 'domcontentloaded' })
await page.waitForFunction(() => (document.querySelector('video')?.currentTime ?? 0) > 1, null, { timeout: 30_000 }).catch(() => {})
const bar = page.locator('div.group\\/scrub').first()
await page.mouse.move(640, 400)
await bar.waitFor({ timeout: 10_000 })
const box = await bar.boundingBox()
check(!!box, 'scrub bar is on screen')

const previewAt = async (fraction) => {
  // One pixel in from the right edge, or the pointer has already left the bar.
  await page.mouse.move(box.x + Math.min(box.width - 1, box.width * fraction), box.y + box.height / 2, { steps: 4 })
  await sleep(300)
  return page.evaluate(() => {
    const tile = [...document.querySelectorAll('div')].find((d) => d.style.backgroundImage.includes('thumbs.jpg'))
    if (!tile) return null
    const label = tile.parentElement?.querySelector('p')?.textContent ?? ''
    return { position: tile.style.backgroundPosition, label, width: tile.getBoundingClientRect().width }
  })
}
const positions = []
for (const f of [0.2, 0.5, 0.8]) {
  const p = await previewAt(f)
  check(!!p, `preview shows a tile at ${Math.round(f * 100)}%`, JSON.stringify(p))
  if (p) positions.push(p)
  if (f === 0.5) await page.screenshot({ path: `${shots}/scrub-preview.png` })
}
check(positions.length === 3 && new Set(positions.map((p) => p.position)).size === 3, 'each hover point shows a different tile', positions.map((p) => p.position).join(' | '))
check(positions.length === 3 && positions.every((p) => /\d+:\d\d/.test(p.label)), 'preview carries the hovered time', positions.map((p) => p.label).join(' | '))

// The preview crop is what the sheet promised, not a squashed one.
check(positions.length === 3 && positions.every((p) => Math.abs(p.width - sheet.width) < 1), 'tile drawn at its native width')

// The very end of the bar must still land on a real frame.
const last = await previewAt(1)
check(!!last && last.position === `-${((sheet.count - 1) % sheet.columns) * sheet.width}px -${Math.floor((sheet.count - 1) / sheet.columns) * sheet.height}px`, 'end of the bar shows the last tile', JSON.stringify(last))

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nALL PASSED' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
