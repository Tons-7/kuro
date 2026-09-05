// How long from opening an episode to the first frame actually playing.
import { chromium } from 'playwright'

const [anime, episode] = (process.argv[2] ?? '185874/44').split('/')

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 200)))

const marks = []
const t0 = Date.now()
const mark = (what) => marks.push([what, Date.now() - t0])

page.on('response', (res) => {
  const u = new URL(res.url()).pathname
  if (u === '/api/play') mark('play resolved a release')
  else if (u === '/api/stream/open') mark('transcoder session open')
  else if (u.endsWith('/playlist.m3u8')) mark('playlist')
  else if (u.endsWith('/init.mp4')) mark('init segment')
  else if (/\/api\/stream\/[^/]+\/\d+\.m4s$/.test(u)) {
    if (!marks.some(([m]) => m === 'first video segment')) mark('first video segment')
  }
})

await page.goto(`http://127.0.0.1:4321/watch/${anime}/${episode}`, {
  waitUntil: 'commit',
  timeout: 120000,
})

try {
  await page.waitForFunction(
    () => {
      const v = document.querySelector('video')
      return v && v.currentTime > 0.2 && !v.paused
    },
    null,
    { timeout: 300000, polling: 250 },
  )
  mark('FIRST FRAME PLAYING')
} catch {
  mark('gave up waiting')
  const v = await page.evaluate(() => {
    const el = document.querySelector('video')
    return el
      ? { readyState: el.readyState, currentTime: el.currentTime, paused: el.paused }
      : 'no video element'
  })
  console.log('video state:', JSON.stringify(v))
  console.log('page says:', (await page.locator('main').innerText()).slice(0, 300))
}

let last = 0
for (const [what, at] of marks) {
  console.log(`${String(at).padStart(7)}ms  (+${String(at - last).padStart(6)}ms)  ${what}`)
  last = at
}

await browser.close()
