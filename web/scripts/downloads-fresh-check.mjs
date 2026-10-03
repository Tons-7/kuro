// The Downloads page never shows a stale list: a download queued elsewhere is
// there on arrival, and one queued while the page is open appears without a reload.
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
const queue = (episode) =>
  fetch(`${BASE}/api/download`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ animeId: ANIME, episode }),
  })
const queued = async () => (await fetch(`${BASE}/api/download/queue`).then((r) => r.json())).items ?? []

check(await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }), 'test episode ready')

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
// Client-side navigation, as the app's own links do: the query cache survives it.
const go = (path) =>
  page.evaluate((p) => {
    window.history.pushState({}, '', p)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, path)
const row = (episode) => page.getByText(new RegExp(`\\b(ep|episode) ${episode}\\b`, 'i')).first()

await page.goto(`${BASE}/downloads`, { waitUntil: 'domcontentloaded' })
await sleep(2000)

// Away, something else queues episode 5; back on Downloads it must already be listed.
await go('/browse')
await sleep(1000)
check((await queue(5)).ok, 'episode 5 queued from elsewhere')
const inQueue = await queued()
check(inQueue.some((q) => q.episode === 5), 'the server holds it', JSON.stringify(inQueue.map((q) => [q.episode, q.state])))
const t0 = Date.now()
await go('/downloads')
const onArrival = await row(5).waitFor({ timeout: 4000 }).then(() => true, () => false)
check(onArrival, 'it is listed on arrival, not after the next poll', `${Date.now() - t0} ms`)

// While the page is open, another is queued from elsewhere: it appears without a reload.
check((await queue(6)).ok, 'episode 6 queued while the page is open')
const t1 = Date.now()
const live = await row(6).waitFor({ timeout: 17_000 }).then(() => true, () => false)
check(live, 'it appears without a reload', `${Date.now() - t1} ms`)
await page.screenshot({ path: `${SHOTS}/downloads-fresh.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
