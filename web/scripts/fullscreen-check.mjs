// Exiting fullscreen showed a black screen while the episode kept playing.
// This reports what is actually on top of the video at each stage.
import { chromium } from 'playwright'

const [anime, episode] = (process.argv[2] ?? '185874/44').split('/')

const browser = await chromium.launch({
  args: ['--autoplay-policy=no-user-gesture-required'],
})
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 200)))

await page.goto(`http://127.0.0.1:4321/watch/${anime}/${episode}`, {
  waitUntil: 'load',
  timeout: 120000,
})

// Playback has to be running before any of this means anything.
await page
  .waitForFunction(
    () => {
      const v = document.querySelector('video')
      return v && v.readyState >= 2
    },
    null,
    { timeout: 300000, polling: 500 },
  )
  .catch(() => console.log('video never became ready'))

await page.evaluate(() => document.querySelector('video')?.play())
await page.waitForTimeout(4000)

const report = (label) =>
  page.evaluate((stage) => {
    const v = document.querySelector('video')
    if (!v) return { stage, error: 'no video' }
    const box = v.getBoundingClientRect()

    // Whatever sits over the middle of the picture is what a viewer sees.
    const onTop = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
    const overlays = [...document.querySelectorAll('canvas')].map((c) => ({
      w: c.width,
      h: c.height,
      css: `${c.style.width}x${c.style.height} @ ${c.style.top},${c.style.left}`,
      opacity: getComputedStyle(c).opacity,
    }))

    return {
      stage,
      fullscreen: !!document.fullscreenElement,
      videoBox: `${Math.round(box.width)}x${Math.round(box.height)} @ ${Math.round(box.x)},${Math.round(box.y)}`,
      videoOpacity: getComputedStyle(v).opacity,
      videoVisible: box.width > 0 && box.height > 0,
      currentTime: Number(v.currentTime.toFixed(1)),
      paused: v.paused,
      onTop: onTop ? `${onTop.tagName}.${String(onTop.className).slice(0, 40)}` : 'none',
      canvases: overlays,
    }
  }, label)

console.log(JSON.stringify(await report('before fullscreen'), null, 1))

await page.evaluate(() => document.querySelector('video')?.closest('div')?.requestFullscreen())
await page.waitForTimeout(3000)
console.log(JSON.stringify(await report('in fullscreen'), null, 1))

await page.evaluate(() => document.exitFullscreen())
await page.waitForTimeout(3000)
console.log(JSON.stringify(await report('after exiting'), null, 1))

await page.screenshot({ path: 'fullscreen-exit.png' })
console.log('screenshot: web/fullscreen-exit.png')

await browser.close()
