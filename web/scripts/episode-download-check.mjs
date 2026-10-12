// Every episode not yet on disk has a download button on the series page; one click queues it.
import { spawnSync } from 'node:child_process'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = process.env.KURO_ANIME ?? '127230'
const SHOTS = process.env.SHOTS ?? '.'


let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 30_000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await new Promise((r) => setTimeout(r, 150))
  }
  return false
}

// A kept season pack holding episodes 4 and 5, planted the way the features check does.
const seeded = spawnSync('go', ['run', './scripts/e2e-seed', '-db', process.env.KURO_DB, '-anime', ANIME, '-pack'], {
  cwd: process.env.KURO_REPO, stdio: 'inherit',
}).status === 0
check(seeded, 'download rows seeded')

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

// Episode 7 is already waiting in the queue, so its row must not offer a second download.
await page.route('**/api/download/queue', (route) =>
  route.fulfill({ json: { items: [{ animeId: Number(ANIME), epKey: '7', episode: 7, state: 'pending' }], waiting: {}, paused: false } }),
)
const queued = []
await page.route('**/api/download', (route) => {
  queued.push(JSON.parse(route.request().postData() ?? '{}'))
  return route.fulfill({ json: { queued: 1 } })
})

await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
const buttons = page.getByRole('button', { name: /^Download episode \d+$/ })
check(await until(() => buttons.count().then((n) => n > 3)), 'episodes not on disk offer a download button')
check((await page.getByRole('button', { name: 'Download episode 4' }).count()) === 0, 'an episode on disk has no download button')
const waiting = page.getByRole('button', { name: 'Episode 7 queued' })
check(await until(() => waiting.isVisible(), 10_000) && (await waiting.isDisabled()), 'an episode already in the queue shows as queued and cannot be queued again')

// Whichever episode is first offered; the seed owns a few.
const first = buttons.first()
const n = Number((await first.getAttribute('aria-label')).match(/\d+/)[0])
await first.click()
check(await until(() => queued.length === 1), 'one click queues the episode', JSON.stringify(queued))
check(queued[0]?.animeId === Number(ANIME) && queued[0]?.episode === n, 'with the right show and episode', JSON.stringify(queued[0]))
check(await until(() => page.getByRole('button', { name: `Episode ${n} queued` }).isVisible()), 'the button says it is queued')
await page.screenshot({ path: `${SHOTS}/episode-download.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
