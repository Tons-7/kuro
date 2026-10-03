// Each Anime4K tier draws a picture on this device, Off draws none, and a phone's Auto stays off.
// Headed by default: WebGPU needs the real GPU, which headless Chromium does not always get.
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.SHOTS ?? '.'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const post = (path, body) =>
  fetch(BASE + path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) })

check(await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }), 'test episode ready')
await post('/api/prefs', { key: 'playback.anime4k', value: 'true' })

const browser = await chromium.launch({
  headless: process.env.HEADLESS === '1',
  args: ['--autoplay-policy=no-user-gesture-required', '--enable-unsafe-webgpu', '--enable-features=Vulkan'],
})

// One profile per case, with the device choice stored before the page loads.
async function watch(choice, contextOptions = {}) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, ...contextOptions })
  if (choice) await context.addInitScript((c) => localStorage.setItem('kuro.anime4k', c), choice)
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
  page.on('console', (m) => m.type() === 'warning' && m.text().includes('anime4k') && errors.push(m.text().slice(0, 200)))
  await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
  const video = page.locator('video')
  await video.waitFor({ timeout: 60000 })
  for (let i = 0; i < 40; i++) {
    if (await video.evaluate((v) => !v.paused && v.currentTime > 1).catch(() => false)) break
    await video.evaluate((v) => v.play().catch(() => {})).catch(() => {})
    await sleep(1000)
  }
  // The upscaled canvas is the one before the video; shown means opacity 1.
  const shown = async () =>
    page.evaluate(() => {
      const canvas = document.querySelector('video')?.previousElementSibling
      return canvas instanceof HTMLCanvasElement && getComputedStyle(canvas).opacity === '1'
        ? `${canvas.width}x${canvas.height}`
        : ''
    })
  let size = ''
  for (let i = 0; i < 20 && !size; i++) {
    size = await shown()
    if (!size) await sleep(500)
  }
  return { context, page, size, errors }
}

const gpu = await (async () => {
  const { context, page } = await watch('off')
  const has = await page.evaluate(async () => !!(navigator.gpu && (await navigator.gpu.requestAdapter())))
  await context.close()
  return has
})()
check(gpu, 'this browser has WebGPU (needed for every tier below)')

if (gpu) {
  for (const tier of ['light', 'balanced', 'full']) {
    const run = await watch(tier)
    check(!!run.size, `${tier}: the upscaled picture is shown`, run.size)
    await sleep(2500)
    await run.page.screenshot({ path: `${SHOTS}/anime4k-${tier}.png` })
    const moving = await run.page.locator('video').evaluate(async (v) => {
      const t = v.currentTime
      await new Promise((r) => setTimeout(r, 1500))
      return v.currentTime - t > 0.5
    })
    check(moving, `${tier}: playback keeps going`)
    // The canvas covers the video, so a frozen renderer would show one frame over a running clock.
    const frame = () => run.page.locator('video').screenshot()
    const before = await frame()
    await sleep(1200)
    check(!before.equals(await frame()), `${tier}: the picture keeps updating`)
    check(run.errors.length === 0, `${tier}: no errors`, run.errors.join(' | '))
    await run.context.close()
  }

  const auto = await watch(null)
  check(!!auto.size, 'auto on a computer follows Settings (on)', auto.size)
  await auto.context.close()
}

const off = await watch('off')
check(!off.size, 'off: no upscaled picture')
await off.context.close()

const phone = await watch(null, { viewport: { width: 412, height: 915 }, isMobile: true, hasTouch: true })
check(!phone.size, 'auto on a phone stays off even with Settings on')
await phone.context.close()

await post('/api/prefs', { key: 'playback.anime4k', value: 'false' })
await browser.close()
console.log(failures ? `${failures} failed` : 'all passed')
process.exit(failures ? 1 : 0)
