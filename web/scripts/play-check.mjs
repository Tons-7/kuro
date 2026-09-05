// Does an episode actually play? Opens a watch page, waits for the transcode
// to start, presses play and reports whether the clock moves.
import { chromium } from 'playwright'

const base = 'http://127.0.0.1:4321'
const target = process.argv[2] ?? '/watch/154587/1'
const shot = process.argv[3] ?? 'play.png'

const browser = await chromium.launch({
  // Autoplay is blocked without a gesture in a real browser; this makes the
  // check about the pipeline rather than about policy.
  args: ['--autoplay-policy=no-user-gesture-required'],
})
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })

page.on('console', (m) => m.type() === 'error' && console.log('  console:', m.text().slice(0, 220)))
page.on('pageerror', (e) => console.log('  uncaught:', e.message.slice(0, 220)))
page.on('response', (r) => {
  const u = new URL(r.url())
  if (u.host !== new URL(base).host) return
  if (u.pathname.startsWith('/api/stream/') && u.pathname.includes('/segment')) return
  if (r.status() >= 400 || u.pathname.startsWith('/api/')) {
    console.log(`  ${r.status()} ${u.pathname}`)
  }
})

const start = Date.now()
const at = () => `t+${((Date.now() - start) / 1000).toFixed(0)}s`

await page.goto(base + target, { waitUntil: 'domcontentloaded' })

let played = false
for (let i = 0; i < 40; i++) {
  await page.waitForTimeout(3000)

  const state = await page.evaluate(() => {
    const v = document.querySelector('video')
    if (!v) return { video: false, text: document.body.innerText.slice(0, 120) }
    return {
      video: true,
      readyState: v.readyState,
      networkState: v.networkState,
      currentTime: Number(v.currentTime.toFixed(2)),
      duration: Number.isFinite(v.duration) ? Number(v.duration.toFixed(1)) : null,
      paused: v.paused,
      buffered: v.buffered.length ? Number(v.buffered.end(0).toFixed(1)) : 0,
      error: v.error ? `${v.error.code}: ${v.error.message}` : null,
      subtitleCanvas: !!document.querySelector('canvas'),
    }
  })

  if (!state.video) {
    console.log(`${at()}  no video element — ${state.text.replace(/\n/g, ' | ').slice(0, 140)}`)
    continue
  }

  console.log(
    `${at()}  ready=${state.readyState} net=${state.networkState} t=${state.currentTime} dur=${state.duration} buf=${state.buffered} paused=${state.paused}${state.error ? ` ERR ${state.error}` : ''}${state.subtitleCanvas ? ' subs=canvas' : ''}`,
  )

  if (state.error) break

  // Once there are frames, try to play and confirm the clock advances.
  if (state.readyState >= 2 && !played) {
    await page.evaluate(() => document.querySelector('video')?.play().catch(() => {}))
    played = true
  }
  if (played && state.currentTime > 1.5) {
    console.log(`\nPLAYING — clock reached ${state.currentTime}s`)
    break
  }
}

await page.screenshot({ path: shot })
await browser.close()
