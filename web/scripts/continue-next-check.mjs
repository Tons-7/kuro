// Continue watching has to offer the next episode of a show you are partway
// through even when you never paused mid-episode — including a rewatch, which
// used to be missing from the row entirely.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 20000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return true
    await sleep(300)
  }
  return false
}
const post = (p, b) =>
  fetch(BASE + p, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(b),
  })
const cont = async () => (await (await fetch(`${BASE}/api/continue?perPage=50`)).json()).items ?? []
const pick = async () => (await cont()).find((i) => i.id === ANIME)

for (let i = 0; i < 60; i++) {
  try {
    if ((await fetch(`${BASE}/api/setup`)).ok) break
  } catch {}
  await sleep(1000)
}
// The show has to exist locally before a list status can hang off it.
await fetch(`${BASE}/api/anime/${ANIME}`)

// Watching episode 1 through to the end and stopping there: no resume point.
await post('/api/status', { animeId: ANIME, status: 'CURRENT' })
await post('/api/watched', { animeId: ANIME, episode: 1 })

let item = await pick()
check(!!item, 'a show watched up to episode 1 is in continue watching')
check(item?.progress === 1, 'the API reports the progress it has', String(item?.progress))
check(
  (item?.resume?.episode ?? item?.progress + 1) === 2,
  'and points at episode 2',
  String(item?.resume?.episode ?? item?.progress + 1),
)

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
const rail = page.locator('section', { has: page.getByText('Continue watching', { exact: true }) })
check(await until(() => rail.first().isVisible()), 'the Continue watching row is on the home page')
const card = rail.locator(`a[href="/watch/${ANIME}/2"]`).first()
check(await until(() => card.isVisible()), 'its card links straight to episode 2')
check(
  (await card.innerText().catch(() => '')).includes('Episode 2'),
  'and says which episode',
  (await card.innerText().catch(() => '')).split('\n').join(' | '),
)

// Finishing the show takes it off the row; there is no next episode.
await post('/api/watched', { animeId: ANIME, episode: 12 })
check(!(await pick()), 'a finished show drops off the row')

// Back to partway through, this time as a rewatch. REPEATING resets progress,
// so the watch has to be redone.
await post('/api/watched', { animeId: ANIME, episode: 2, watched: false })
await post('/api/status', { animeId: ANIME, status: 'REPEATING' })
await post('/api/watched', { animeId: ANIME, episode: 1 })
item = await pick()
check(!!item, 'a rewatch in progress is in continue watching')
check(
  (item?.resume?.episode ?? item?.progress + 1) === 2,
  'and offers the next episode of the rewatch',
  String(item?.resume?.episode ?? item?.progress + 1),
)

await page.reload({ waitUntil: 'domcontentloaded' })
check(await until(() => rail.locator(`a[href="/watch/${ANIME}/2"]`).first().isVisible()),
  'the rewatch card is on the home page')

// Removing it is per-episode and has to stick.
await post('/api/continue/dismiss', { animeId: ANIME, episode: 2 })
check(!(await pick()), 'dismissing the card removes it')

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
