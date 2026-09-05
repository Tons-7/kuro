// Seek to a known dialogue timestamp and screenshot the player.
import { chromium } from 'playwright'

const [, , path, seconds, out] = process.argv

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1500, height: 950 } })
page.on('console', (m) => m.type() === 'error' && console.log('[error]', m.text().slice(0, 200)))
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 250)))

await page.goto('http://127.0.0.1:4321' + path, { waitUntil: 'load', timeout: 60000 })
await page.waitForSelector('video', { timeout: 180000 })
await page.waitForTimeout(6000)

await page.evaluate(async (t) => {
  const v = document.querySelector('video')
  v.muted = true
  await v.play().catch(() => {})
  v.currentTime = t
}, Number(seconds))

// Let the swarm deliver that region, then hold the frame so the shot lands
// inside the dialogue window rather than drifting past it.
const target = Number(seconds)
for (let i = 0; i < 40; i++) {
  await page.waitForTimeout(1500)
  const s = await page.evaluate(() => {
    const v = document.querySelector('video')
    return { t: v.currentTime, ready: v.readyState }
  })
  if (s.ready >= 3 && s.t >= target - 0.5) break
}
// Pause first: seeking while playing drifts past a short dialogue window.
await page.evaluate((t) => {
  const v = document.querySelector('video')
  v.pause()
  v.currentTime = t
}, target)
await page.waitForTimeout(4000)

const t = await page.evaluate(() => document.querySelector('video').currentTime.toFixed(1))
console.log('frame held at', t, 's')
await page.locator('.group\\/player').first().screenshot({ path: out })
await browser.close()
