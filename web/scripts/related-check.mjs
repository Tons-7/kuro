// The series page: seasons in their own rail, films and spin-offs grouped into
// theirs, and a cast rail of single portraits. Detective Conan is the hard case.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'
const ANIME = Number(process.env.KURO_RELATED ?? 235)
const RAIL = /^(Related|Movies?|Specials?|OVAs?|ONAs?|Spin-offs?|Alternatives?|Recaps?)(\s·\s\d+)?$/

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 30000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return true
    await new Promise((r) => setTimeout(r, 500))
  }
  return false
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
check(await until(() => page.getByRole('heading', { level: 1 }).isVisible()), 'the series page loads')

const api = await (await fetch(`${BASE}/api/franchise?id=${ANIME}`)).json()
const total = (api.related ?? []).length
check(total > 10, 'the API returns the franchise films', `${total} related`)

const rails = page.locator('section').filter({ has: page.getByRole('heading', { name: RAIL }) })
check(await until(async () => (await rails.count()) > 0), 'the related material has a rail')
const titles = await rails.locator('h2').allInnerTexts()
check(titles.length > 1, 'a big franchise is grouped by kind, not one strip of sixty', titles.join(' | '))

const chips = rails.locator('a[href^="/anime/"]')
check(await until(async () => (await chips.count()) === total), 'every entry is in a rail, none hidden behind a button', `${await chips.count()} of ${total}`)
check(
  (await rails.getByRole('button', { name: /Show all/ }).count()) === 0,
  'no Show all: the rails scroll instead',
)

// Nothing renders as a bare title: a dash means hydration did not fill it.
const texts = await chips.allInnerTexts()
const undated = texts.filter((t) => !/\b(19|20)\d{2}\b/.test(t))
check(undated.length === 0, 'every entry shows its year', undated.slice(0, 3).map((t) => t.replace(/\s+/g, ' ')).join(' | '))
const noEpisodes = texts.filter((t) => !/\d+ ep\b/.test(t))
check(noEpisodes.length === 0, 'every entry shows its episode count', noEpisodes.slice(0, 3).map((t) => t.replace(/\s+/g, ' ')).join(' | '))

// Arrows and the progress line belong to rails that actually overflow.
const overflowing = await rails.evaluateAll((sections) =>
  sections.map((s) => {
    const track = s.querySelector('div.overflow-x-auto')
    const heading = s.querySelector('h2')?.textContent ?? ''
    return {
      heading,
      scrollable: !!track && track.scrollWidth - track.clientWidth > 1,
      arrows: s.querySelectorAll('button[aria-label^="Scroll"]').length,
      bar: !!s.querySelector('[aria-hidden][class*="rounded-full"]'),
    }
  }),
)
for (const r of overflowing) {
  check(
    r.scrollable ? r.arrows === 2 : r.arrows === 0,
    `${r.heading.trim()}: arrows only when it scrolls`,
    `${r.arrows} arrows, scrollable=${r.scrollable}`,
  )
}
check(overflowing.some((r) => !r.scrollable), 'a short rail is among them', overflowing.map((r) => r.heading.trim()).join(' | '))

const seasons = page.locator('section', { has: page.getByRole('heading', { name: /^Seasons/ }) })
if (await seasons.isVisible().catch(() => false)) {
  check(!/movie|side story/i.test(await seasons.innerText()), 'the seasons rail holds no films')
}

const cast = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Cast' }) })
if (await until(() => cast.isVisible(), 25000)) {
  check(!/SUPPORTING/.test(await cast.innerText()), 'no shouted SUPPORTING label')
  const toggle = cast.getByRole('button', { name: /Show (all|more)/ })
  if (await toggle.isVisible().catch(() => false)) {
    await toggle.click()
    const less = cast.getByRole('button', { name: 'Show less' })
    check(await until(() => less.isVisible()), 'the cast toggle becomes Show less')
    check(
      (await cast.getByRole('button', { name: /Show (all|more)/ }).count()) === 0,
      'and does not sit beside its own opposite',
    )
  }
  // The cast rail is below the episode list, where browsing belongs.
  const episodes = page.locator('section', { has: page.getByRole('heading', { name: 'Episodes' }) })
  const [castBox, epBox] = [await cast.boundingBox(), await episodes.boundingBox()]
  if (castBox && epBox) check(castBox.y > epBox.y, 'the cast sits below the episodes')
}

check(errors.length === 0, 'no page errors', errors.join(' | '))

const first = await rails.first().boundingBox()
if (first) {
  await page.screenshot({
    path: `${SHOTS}/series-rails.png`,
    clip: { x: 0, y: Math.max(0, first.y - 150), width: 1440, height: 800 },
  })
}

await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
