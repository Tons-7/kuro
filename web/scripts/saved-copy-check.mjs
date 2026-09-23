// When AniList can't be reached pages come from kuro's saved copy, and a banner
// says so; the next live answer clears it. The outage is simulated by marking
// responses the way the server does when it answers from the saved copy.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 30000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return true
    await new Promise((r) => setTimeout(r, 300))
  }
  return false
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

// Browsing saves what it shows: that is what the fallback later stands on.
const live = await fetch(`${BASE}/api/browse?sort=popular&perPage=42`)
check(live.headers.get('x-kuro-live') === '1', 'a live answer is marked live')
const first = (await live.json()).items?.[0]
check(!!first, 'browse returned shows', first?.title)
const saved = await until(async () => {
  const hits = await (await fetch(`${BASE}/api/search/local?q=${encodeURIComponent(first.title.slice(0, 12))}`)).json()
  return (hits.results ?? []).some((r) => r.id === first.id)
}, 15000)
check(saved, 'a browsed show is saved and found by the local search')

const dayAgo = Math.floor(Date.now() / 1000) - 26 * 3600
await page.route('**/api/discover**', async (route) => {
  const res = await route.fetch()
  const headers = { ...res.headers(), 'x-kuro-saved': String(dayAgo) }
  delete headers['x-kuro-live']
  await route.fulfill({ response: res, headers })
})
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
const banner = page.getByRole('status').filter({ hasText: 'saved copy' })
check(await until(() => banner.isVisible()), 'the banner shows while answers are saved copies')
check(/1d ago/.test(await banner.innerText().catch(() => '')), 'and says how old', await banner.innerText().catch(() => ''))
await page.screenshot({ path: `${SHOTS}/saved-copy-banner.png` })

// AniList back: the next live answer clears it.
// A request still in the handler would fulfil a route already gone.
await page.unrouteAll({ behavior: 'ignoreErrors' })
await banner.getByRole('button', { name: 'Try now' }).click()
check(await until(async () => !(await banner.isVisible())), 'a live answer clears the banner')

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
