// Paging through filtered results. Next used to be swallowed by the rule that
// sends a changed filter back to page one.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 25000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    try {
      if (await fn()) return true
    } catch {}
    await new Promise((r) => setTimeout(r, 400))
  }
  return false
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

// A filter narrow enough to be stable, wide enough to have several pages.
await page.goto(`${BASE}/browse?year=2023&sort=popular`, { waitUntil: 'domcontentloaded' })
const cards = page.locator('a[href^="/anime/"]')
check(await until(async () => (await cards.count()) > 0), 'filtered results load')

const firstOnPageOne = await cards.first().getAttribute('href')
const next = page.getByRole('button', { name: 'Next' })
check(await next.isEnabled(), 'Next is offered')
await next.click()

check(await until(() => page.url().includes('page=2')), 'Next moves to page two', page.url())
check(
  await until(async () => (await cards.first().getAttribute('href')) !== firstOnPageOne),
  'the results actually change',
  `${firstOnPageOne} → ${await cards.first().getAttribute('href')}`,
)
check(await page.getByText('Page 2').isVisible().catch(() => false), 'the pager says page 2')
check((await page.locator('a[href^="/anime/"]').count()) > 0, 'page two has results')

// The filter survives the page change, and going back returns to page one.
check(page.url().includes('year=2023'), 'the filter is still applied', page.url())
await page.getByRole('button', { name: 'Previous' }).click()
check(await until(() => /page=1|(?!.*page=)/.test(page.url())), 'Previous goes back', page.url())
check(
  await until(async () => (await cards.first().getAttribute('href')) === firstOnPageOne),
  'and shows the first page again',
)

// A new search starts at its own first page rather than page three of the last.
await page.goto(`${BASE}/browse?year=2023&sort=popular&page=3`, { waitUntil: 'domcontentloaded' })
await until(async () => (await cards.count()) > 0)
await page.getByPlaceholder(/Search .* anime/i).fill('frieren')
check(await until(() => !page.url().includes('page=3')), 'a new search returns to page one', page.url())
check(await until(() => page.url().includes('q=frieren')), 'and keeps the search', page.url())

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
