// End-to-end sweep: every page, then real playback across a spread of anime.
// Reports what a person would notice — did it render, did it play, how long.
import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const base = process.env.KURO_URL ?? 'http://127.0.0.1:4321'
const shots = process.argv[2] ?? 'e2e'
mkdirSync(shots, { recursive: true })

const PAGES = [
  ['home', '/'],
  ['browse', '/browse'],
  ['schedule', '/schedule'],
  ['library', '/library'],
  ['settings', '/settings'],
  ['setup', '/setup'],
  ['downloads', '/downloads'],
  ['local', '/local'],
  ['notifications', '/notifications'],
]

// A deliberate spread: long-running, current, finished, film, and a
// single-cour show, so the release matching is not only tested on the easy case.
const TITLES = [
  ['Frieren', 154587, 1],
  ['Dandadan', 171018, 1],
  ['Attack on Titan', 16498, 1],
  ['One Piece', 21, 1050],
  ['Steins;Gate', 9253, 1],
  ['Mob Psycho 100', 21507, 1],
  ['A Silent Voice (film)', 20954, 1],
  ['BLEACH TYBW Calamity', 185874, 41],
]

const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

let failures = 0
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text().slice(0, 160)))
page.on('pageerror', (e) => errors.push(`uncaught: ${e.message.slice(0, 160)}`))

console.log('PAGES')
for (const [name, path] of PAGES) {
  errors.length = 0
  // Not networkidle: several screens poll, so the network never goes quiet.
  await page.goto(base + path, { waitUntil: 'domcontentloaded', timeout: 45000 }).catch((e) =>
    errors.push(`nav: ${e.message.slice(0, 100)}`),
  )
  await page.waitForTimeout(3500)

  const nodes = await page.evaluate(() => document.getElementById('root')?.childElementCount ?? 0)
  const text = await page.evaluate(() => document.body.innerText.trim().length)
  await page.screenshot({ path: `${shots}/page-${name}.png` })

  const bad = nodes === 0 || text < 40 || errors.length > 0
  if (bad) failures++
  console.log(`  ${bad ? 'FAIL' : 'ok  '} ${name.padEnd(14)} text=${String(text).padStart(5)}`)
  for (const e of [...new Set(errors)].slice(0, 3)) console.log(`         ${e}`)
}

console.log('\nPLAYBACK')
for (const [name, id, ep] of TITLES) {
  errors.length = 0
  const start = Date.now()
  let ready = null
  let playing = null
  let detail = ''

  await page.goto(`${base}/watch/${id}/${ep}`, { waitUntil: 'domcontentloaded' })

  for (let i = 0; i < 25; i++) {
    await page.waitForTimeout(2000)
    const s = await page.evaluate(() => {
      const v = document.querySelector('video')
      const body = document.body.innerText
      if (!v) {
        return { none: true, searching: body.includes('Searching for a release'), body: body.slice(0, 200) }
      }
      return {
        none: false,
        readyState: v.readyState,
        currentTime: v.currentTime,
        duration: Number.isFinite(v.duration) ? Math.round(v.duration) : null,
        error: v.error ? `media error ${v.error.code}` : null,
        subs: !!document.querySelector('canvas'),
      }
    })

    if (s.none) {
      if (!s.searching) {
        // Settled without a player: an error panel.
        detail = s.body.split('\n').map((x) => x.trim()).filter(Boolean)[0] ?? 'no player'
        break
      }
      continue
    }
    if (s.error) {
      detail = s.error
      break
    }
    if (ready === null && s.readyState >= 2) {
      ready = ((Date.now() - start) / 1000).toFixed(0)
      await page.evaluate(() => document.querySelector('video')?.play().catch(() => {}))
    }
    if (ready !== null && s.currentTime > 1.5) {
      playing = ((Date.now() - start) / 1000).toFixed(0)
      detail = `${s.duration}s${s.subs ? ' subs' : ' NO SUBS'}`
      break
    }
  }

  const ok = playing !== null
  if (!ok) failures++
  console.log(
    `  ${ok ? 'ok  ' : 'FAIL'} ${name.padEnd(22)} ep${String(ep).padEnd(5)} ${ok ? `ready ${ready}s, playing ${playing}s, ${detail}` : detail}`,
  )
  for (const e of [...new Set(errors)].slice(0, 2)) console.log(`         ${e}`)

  await page.screenshot({ path: `${shots}/play-${id}.png` })
  // Release the transcode session before moving on.
  await page.goto(`${base}/`, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(800)
}

await browser.close()
console.log(failures === 0 ? '\nall good' : `\n${failures} problem(s)`)
process.exit(failures === 0 ? 0 : 1)
