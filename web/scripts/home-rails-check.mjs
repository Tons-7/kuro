// The home rails at the bottom: what is airing now and what is coming next,
// and their "See all" links landing on the matching browse filters.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.MANUAL_SHOTS ?? process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 30000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    try {
      if (await fn()) return true
    } catch {}
    await sleep(300)
  }
  return false
}
const get = async (p) => (await fetch(BASE + p)).json()

const SEASONS = ['WINTER', 'SPRING', 'SUMMER', 'FALL']
const now = new Date()
const q = Math.floor(now.getMonth() / 3)
const here = { season: SEASONS[q], year: now.getFullYear() }
const next = { season: SEASONS[(q + 1) % 4], year: q === 3 ? now.getFullYear() + 1 : now.getFullYear() }

for (let i = 0; i < 60; i++) {
  try {
    if ((await fetch(`${BASE}/api/setup`)).ok) break
  } catch {}
  await sleep(1000)
}

const upcoming = await get('/api/discover?sort=upcoming&perPage=20')
check((upcoming.items ?? []).length > 0, 'the upcoming sort returns shows', String(upcoming.items?.length))
const aired = (upcoming.items ?? []).filter((i) => i.status && i.status !== 'NOT_YET_RELEASED')
check(aired.length === 0, 'none of them has started airing',
  aired.map((i) => `${i.title}:${i.status}`).slice(0, 3).join(', '))
const wrongSeason = (upcoming.items ?? []).filter(
  (i) => i.season && (i.season !== next.season || i.seasonYear !== next.year),
)
check(wrongSeason.length === 0, `they are all ${next.season} ${next.year}`,
  wrongSeason.map((i) => `${i.title}:${i.season} ${i.seasonYear}`).slice(0, 3).join(', '))

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
const rail = (title) => page.locator('section', { has: page.getByText(title, { exact: true }) }).first()

check(await until(() => rail('Upcoming').isVisible()), 'the Upcoming row is on the home page')
const trendingBox = await rail('Trending now').boundingBox()
const upcomingBox = await rail('Upcoming').boundingBox()
check((upcomingBox?.y ?? 0) > (trendingBox?.y ?? 0), 'and sits below Trending now',
  `trending ${Math.round(trendingBox?.y ?? 0)} vs upcoming ${Math.round(upcomingBox?.y ?? 0)}`)
check(await rail('Upcoming').locator('a[href^="/anime/"]').first().isVisible(), 'with cards in it')
await page.screenshot({ path: `${SHOTS}/home-upcoming.png`, fullPage: true })

// See all used to drop the season and land on "most popular of all time".
const seeAll = (title) => rail(title).getByRole('link', { name: /see all/i }).first()
const seasonHref = await seeAll('This season').getAttribute('href')
check(seasonHref === `/browse?season=${here.season}&year=${here.year}&sort=popular`,
  'This season → browse filtered to this season', String(seasonHref))
const upcomingHref = await seeAll('Upcoming').getAttribute('href')
check(upcomingHref === `/browse?season=${next.season}&year=${next.year}&sort=popular`,
  'Upcoming → browse filtered to next season', String(upcomingHref))

await seeAll('This season').click()
check(await until(() => page.url().includes('season='), 10_000), 'it navigates to browse', page.url())
check(await until(() => page.getByRole('button', { name: /^Genre/ }).isVisible()), 'the filter bar is up')
const chips = await page.locator('button').allInnerTexts()
check(chips.some((t) => t.trim().toLowerCase() === here.season.toLowerCase()),
  'the season filter shows as chosen', chips.filter(Boolean).slice(0, 8).join(' | '))
check(chips.some((t) => t.trim() === String(here.year)), 'and so does the year')
check(await until(() => page.locator('a[href^="/anime/"]').first().isVisible(), 30_000),
  'and the results are not empty')
const shown = await get(`/api/browse?season=${here.season}&year=${here.year}&sort=popular&perPage=10`)
const strays = (shown.items ?? []).filter((i) => i.seasonYear && i.seasonYear !== here.year)
check(strays.length === 0, 'every result is from this year',
  strays.map((i) => `${i.title}:${i.seasonYear}`).slice(0, 3).join(', '))
await page.screenshot({ path: `${SHOTS}/browse-season.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
