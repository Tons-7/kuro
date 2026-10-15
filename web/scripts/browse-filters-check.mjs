// The Browse filter pills: each choice lands in the URL and the request, genres stack, single choices
// (year, season, sort) replace each other, chips remove one, and the counts answer to the narrowing.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
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

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
// Every browse request the page makes, newest last, as its query string.
const asked = []
page.on('request', (r) => {
  const m = r.url().match(/\/api\/browse\?(.*)$/)
  if (m && !r.url().includes('/api/browse/')) asked.push(decodeURIComponent(m[1]))
})
const lastAsk = () => asked.at(-1) ?? ''
const pageParam = (key) => new URL(page.url()).searchParams.get(key)
const cards = page.locator('a[href^="/anime/"]')
// A card is two links (poster and title), so the shows listed are the distinct targets.
const shown = async () => new Set(await cards.evaluateAll((els) => els.map((e) => e.getAttribute('href')))).size
const results = () => page.locator('body').innerText().then((t) => t.match(/([\d,]+) results?\b/)?.[1]?.replace(',', '') ?? '')
// The server's count for a filter set, the page's yardstick; 503 while the request budget is spent,
// 502 when AniList sat on a probe. On a cold cache a broad count took five minutes (2026-10-15).
const count = async (qs) => {
  const until = Date.now() + 8 * 60_000
  while (Date.now() < until) {
    const r = await page.request.get(`${BASE}/api/browse/count${qs ? `?${qs}` : ''}`, { timeout: 120_000 })
    if (r.ok()) return (await r.json()).count
    await new Promise((res) => setTimeout(res, 1500))
  }
  return -1
}

const pill = (label) => page.getByRole('button', { name: new RegExp(`^${label}`) })
const choose = async (label, option) => {
  const b = pill(label)
  if ((await b.getAttribute('aria-expanded')) !== 'true') await b.click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

await page.goto(`${BASE}/browse`, { waitUntil: 'domcontentloaded' })
check(await until(() => cards.count().then((n) => n > 5), 60_000), 'Browse lists anime before any filter')
check(await page.getByText('No filters', { exact: false }).isVisible(), 'and says nothing narrows it')

// Two genres at once: both sent, and the count shrinks.
await choose('Genre', 'romance')
check(await until(() => pageParam('genres') === 'Romance'), 'a genre lands in the URL', page.url())
await choose('Genre', 'comedy')
check(await until(() => pageParam('genres') === 'Romance,Comedy'), 'a second genre joins it', pageParam('genres'))
check(await until(() => /genres=Romance,Comedy/.test(lastAsk())), 'both genres are sent to the server', lastAsk())
await page.keyboard.press('Escape')
const one = await count('genres=Romance')
const two = await count('genres=Romance,Comedy')
check(one > 0 && two > 0 && two < one, 'the count narrows with the second genre', `${one} → ${two}`)
check(await until(async () => (await results()) === String(two), 60_000), 'the page shows that count', await results())
const chip = (label) => page.getByRole('button', { name: `Remove ${label}` })
check((await chip('Romance').count()) === 1 && (await chip('Comedy').count()) === 1, 'both genres show as chips')

// Format and status stack on top.
await choose('Format', 'movie')
check(await until(() => pageParam('formats') === 'MOVIE'), 'a format lands in the URL', pageParam('formats'))
await page.keyboard.press('Escape')
await choose('Status', 'finished')
check(await until(() => pageParam('statuses') === 'FINISHED'), 'a status lands in the URL', pageParam('statuses'))
await page.keyboard.press('Escape')
check(await until(() => /formats=MOVIE/.test(lastAsk()) && /statuses=FINISHED/.test(lastAsk()) && /genres=Romance,Comedy/.test(lastAsk())), 'every filter travels together', lastAsk())
const four = await count('genres=Romance,Comedy&formats=MOVIE&statuses=FINISHED')
check(four > 0 && four < two, 'format and status narrow it further', `${two} → ${four}`)

// Year and season are single choices: a second pick replaces the first.
await choose('Year', '2023')
check(await until(() => pageParam('year') === '2023'), 'a year lands in the URL')
await page.keyboard.press('Escape')
await choose('2023', '2022')
check(await until(() => pageParam('year') === '2022'), 'picking another year replaces it', pageParam('year'))
await page.keyboard.press('Escape')
await choose('Season', 'spring')
check(await until(() => pageParam('season') === 'SPRING'), 'a season lands in the URL', pageParam('season'))
await page.keyboard.press('Escape')
check(await until(() => /year=2022/.test(lastAsk()) && /season=SPRING/.test(lastAsk())), 'year and season are sent', lastAsk())
await page.screenshot({ path: `${SHOTS}/browse-filters.png` })

// A chip removes one value and leaves the rest.
await chip('Romance').click()
check(await until(() => pageParam('genres') === 'Comedy'), 'removing a chip drops that genre alone', pageParam('genres'))

// Sort changes the order asked for.
await choose('Most popular', 'Highest rated')
check(await until(() => /sort=/.test(lastAsk()) && pageParam('sort') !== null), 'a sort is sent', `${pageParam('sort')} · ${lastAsk()}`)
await page.keyboard.press('Escape')

// Text search joins the filters. Exact: the header's "Search anime" box is another thing.
await page.getByLabel('Search', { exact: true }).fill('love')
check(await until(() => pageParam('q') === 'love' && /q=love/.test(lastAsk())), 'a search joins the filters', lastAsk())
check(await until(() => /genres=Comedy/.test(lastAsk()) && /sort=score/.test(lastAsk())), 'without dropping the filters', lastAsk())

// Studio: the whole list is held, so every filter narrows it, the count is exact, and its chip goes alone.
const api = async (qs) => (await fetch(`${BASE}/api/browse?${qs}`)).json()
await page.goto(`${BASE}/browse`, { waitUntil: 'domcontentloaded' })
await page.getByRole('button', { name: 'Studio', exact: true }).click()
await page.getByPlaceholder('Search studios…').fill('MAPPA')
const mappa = page.getByRole('option', { name: /^MAPPA$/i }).first()
check(await until(() => mappa.isVisible(), 60_000), 'the studio picker finds MAPPA')
await mappa.click()
check(await until(() => pageParam('studio') === '569' && pageParam('studioName') === 'MAPPA'), 'the studio lands in the URL', page.url())
check(await until(() => cards.count().then((n) => n > 3), 60_000), "and the studio's work is listed")
const studioChip = page.getByRole('button', { name: 'Remove Studio: MAPPA' })
check(await until(() => studioChip.isVisible()), 'as a chip')
await choose('Genre', 'action')
await page.keyboard.press('Escape')
await choose('Format', 'tv')
await page.keyboard.press('Escape')
await choose('Status', 'finished')
await page.keyboard.press('Escape')
check(await until(() => ['studio=569', 'genres=Action', 'formats=TV', 'statuses=FINISHED'].every((p) => lastAsk().includes(p))), 'genre, format and status travel with the studio', lastAsk())
const narrowed = await api('studio=569&genres=Action&formats=TV&statuses=FINISHED&perPage=42')
const genresOf = (i) => i.Genres ?? i.genres ?? []
check((narrowed.items ?? []).length > 0 && narrowed.items.every((i) => i.format === 'TV' && i.status === 'FINISHED' && genresOf(i).includes('Action')), 'the server narrows the studio list by all three', `${(narrowed.items ?? []).length} shows`)
check(await until(async () => (await shown()) === narrowed.items.length, 30_000), 'and the page lists exactly those', String(await shown()))
await choose('Year', '2022')
await page.keyboard.press('Escape')
const withYear = await api('studio=569&genres=Action&formats=TV&statuses=FINISHED&year=2022&perPage=42')
check((withYear.items ?? []).length > 0 && withYear.items.length < narrowed.items.length && withYear.items.every((i) => i.seasonYear === 2022), 'a year narrows the studio list too', `${(withYear.items ?? []).length} of ${narrowed.items.length}`)
check(await until(async () => (await results()) === String(withYear.total), 30_000), 'and the page shows the exact count', `${await results()} vs ${withYear.total}`)
check(await until(async () => (await shown()) === withYear.items.length), 'and lists those', String(await shown()))
await page.getByLabel('Search', { exact: true }).fill('chainsaw')
check(await until(() => /studio=569/.test(lastAsk()) && /q=chainsaw/.test(lastAsk())), 'a search is sent with the studio', lastAsk())
const found = await api('studio=569&q=chainsaw&perPage=42')
check((found.items ?? []).length > 0 && found.items.every((i) => /chainsaw/i.test(i.title)), 'and narrows the studio list by title', (found.items ?? []).map((i) => i.title).join(' | '))
await page.getByLabel('Search', { exact: true }).fill('')
await choose('Most popular', 'Highest rated')
await page.keyboard.press('Escape')
check(await until(() => /studio=569/.test(lastAsk()) && /sort=score/.test(lastAsk())), 'a sort is sent with the studio', lastAsk())
const byScore = await api('studio=569&sort=score&perPage=42')
const scores = (byScore.items ?? []).map((i) => i.score ?? 0)
check(scores.length > 3 && scores.every((s, i) => i === 0 || s <= scores[i - 1]), 'and the studio list comes sorted by it', scores.slice(0, 8).join(' '))
await studioChip.click()
check(await until(() => pageParam('studio') === null && pageParam('studioName') === null && pageParam('genres') === 'Action' && pageParam('formats') === 'TV' && pageParam('statuses') === 'FINISHED'), 'removing the studio chip keeps the other filters', page.url())
check(await until(() => !/studio=/.test(lastAsk()) && /genres=Action/.test(lastAsk())), 'and the request drops it alone', lastAsk())
await page.screenshot({ path: `${SHOTS}/browse-filters-studio.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
