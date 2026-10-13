// Change release on the downloads page: every row offers it, the picker opens for that episode, the
// pick is sent as the episode's release, and a queued row shows what was chosen.
import { spawnSync } from 'node:child_process'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 30_000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await Promise.resolve().then(fn).catch(() => false)) return true
    await new Promise((r) => setTimeout(r, 150))
  }
  return false
}

// Two finished downloads on disk (episodes 2 and 3), planted the way the features check does.
const seeded = spawnSync('go', ['run', './scripts/e2e-seed', '-db', process.env.KURO_DB, '-anime', String(ANIME)], {
  cwd: process.env.KURO_REPO, stdio: 'inherit',
}).status === 0
check(seeded, 'download rows seeded')

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

// The sites are not reachable here: the picker's list is answered for it.
const releases = [
  { Torrent: { title: '[Mock] Kuro Test Show - 03 [1080p]', infoHash: 'a'.repeat(40), size: 1 << 30, seeders: 40, seedersKnown: true, leechers: 1 }, Release: { group: 'Mock' } },
  { Torrent: { title: '[Other] Kuro Test Show - 03 [720p]', infoHash: 'b'.repeat(40), size: 1 << 29, seeders: 5, seedersKnown: true, leechers: 0 }, Release: { group: 'Other' } },
]
await page.route('**/api/episode/sources*', (route) => route.fulfill({ json: { results: releases, queries: ['Kuro Test Show'] } }))
const chosen = []
await page.route('**/api/download/release', (route) => {
  chosen.push(JSON.parse(route.request().postData() ?? '{}'))
  return route.fulfill({ status: 202, json: { queued: true } })
})
// Once chosen, the queue lists the episode with its release.
await page.route('**/api/download/queue', (route) =>
  route.fulfill({
    json: {
      items: chosen.length
        ? [{ animeId: ANIME, epKey: '3', episode: 3, state: 'pending', infoHash: chosen[0].infoHash, release: chosen[0].title }]
        : [],
      waiting: {}, paused: false,
    },
  }),
)

await page.goto(`${BASE}/downloads`, { waitUntil: 'domcontentloaded' })
// A show with several downloads is one closed card; its rows are inside.
const group = page.getByRole('button', { name: /^Show downloads of / }).first()
if (await until(() => group.isVisible(), 10_000)) await group.click()
const change = page.getByRole('button', { name: 'Change release for episode 3' })
check(await until(() => change.first().isVisible()), 'a download on disk offers Change release')
await change.first().click()
const dialog = page.getByRole('dialog', { name: 'Choose a release' })
check(await until(() => dialog.isVisible()), 'the picker opens')
check(await until(() => dialog.getByText('downloads it instead').isVisible()), 'and says the pick downloads instead of the current release')
await page.screenshot({ path: `${SHOTS}/change-release-picker.png` })
await dialog.getByText('[Other] Kuro Test Show - 03 [720p]').click()
check(await until(() => chosen.length === 1), 'picking one sends the choice')
check(chosen[0]?.animeId === ANIME && chosen[0]?.episode === 3 && chosen[0]?.infoHash === 'b'.repeat(40) && chosen[0]?.title === '[Other] Kuro Test Show - 03 [720p]', 'with the show, the episode, the release and its name', JSON.stringify(chosen[0]))
check(await until(async () => (await dialog.count()) === 0), 'and the picker closes')
// The copy on disk stands for the queued swap, so it is the row that says what is coming.
check(await until(() => page.getByText('Changing to: [Other] Kuro Test Show - 03 [720p]').isVisible()), 'the row on disk shows the release it is changing to')
check(await until(() => change.count().then((n) => n >= 1)), 'and still offers to change it')
await page.screenshot({ path: `${SHOTS}/change-release-queued.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
