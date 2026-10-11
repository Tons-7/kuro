// A release bigger than the cache budget: the watch page says it will be removed, and Keep makes it stay.
// The play answer and the downloads list are fed to the page, so no torrent is involved.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = process.env.KURO_ANIME ?? '127230'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 10_000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await new Promise((r) => setTimeout(r, 150))
  }
  return false
}

const GB = 1 << 30
const HASH = 'f'.repeat(40)
let film = {
  infoHash: HASH, animeId: Number(ANIME), title: 'Big Film', name: '[Group] Big Film [1080p].mkv',
  episode: '1', episodes: ['1'], totalBytes: 8.5 * GB, bytesOnDisk: 8.5 * GB, percent: 100,
  pinned: true, kept: false, state: 'live',
}
const session = {
  animeId: Number(ANIME), episode: 1, title: film.name, source: 'torrent', infoHash: HASH,
  torrentId: 7, fileIndex: 0, streamUrl: 'http://127.0.0.1:1/nowhere', startAt: 0, player: 'browser',
}
const keeps = []

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

await page.route('**/api/play', (route) => route.fulfill({ json: session }))
await page.route('**/api/cache', (route) => route.fulfill({ json: { budget: 5 * GB, bytes: 8.5 * GB, files: 1 } }))
await page.route('**/api/downloads', (route) => route.fulfill({ json: { items: [film], count: 1 } }))
await page.route(`**/api/downloads/${HASH}/keep`, (route) => {
  keeps.push(route.request().method())
  film = { ...film, kept: true }
  return route.fulfill({ json: { infoHash: HASH, kept: true } })
})

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
const note = page.getByRole('status').filter({ hasText: 'bigger than' })
check(await until(() => note.isVisible()), 'the page says the release is bigger than the cache')
const text = (await note.innerText().catch(() => '')).replace(/\s+/g, ' ')
check(/8\.5 GB/.test(text) && /5(\.0)? GB cache/.test(text), 'it names both sizes', text)
await page.screenshot({ path: `${SHOTS}/over-budget-note.png` })

await note.getByRole('button', { name: 'Keep this episode' }).click()
check(await until(() => keeps.length === 1), 'Keep asks the server to keep the download', keeps.join(','))
check(await until(() => note.isVisible().then((v) => !v)), 'a kept download has no warning')

// Within the budget there is nothing to say.
film = { ...film, kept: false, totalBytes: 2 * GB }
await page.reload({ waitUntil: 'domcontentloaded' })
await page.getByText('Big Film').first().waitFor({ timeout: 10_000 }).catch(() => {})
await new Promise((r) => setTimeout(r, 1500))
check(!(await note.isVisible()), 'a release that fits the cache gets no warning')

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
