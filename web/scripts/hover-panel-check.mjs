// The hover panel: an airing show reads "10/13 eps" with its next episode, a
// finished one "13 eps"; both offer details, the trackers and play.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1500, height: 950 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

async function hoverFirst(statuses, shot) {
  // An airing show with a total announced, so "10/13" is what should show.
  const api = await (await fetch(`${BASE}/api/browse?statuses=${statuses}&formats=TV&sort=popular&perPage=42`)).json()
  const pick = (api.items ?? []).find((i) => (statuses === 'RELEASING' ? i.episodes && i.nextEpisode : true))
  await page.goto(`${BASE}/browse?statuses=${statuses}&formats=TV&sort=popular`, { waitUntil: 'domcontentloaded' })
  const card = pick
    ? page.locator(`a[href="/anime/${pick.id}"]`).filter({ has: page.locator('img') }).first()
    : page.locator('a[href^="/anime/"]').filter({ has: page.locator('img') }).first()
  await card.waitFor({ timeout: 30000 })
  await page.mouse.move(0, 0)
  // Off-centre: the middle is the card's play button.
  await card.hover({ position: { x: 12, y: 60 } })
  const panel = page.locator('div.fixed.z-50.rounded-2xl')
  await panel.waitFor({ timeout: 5000 }).catch(() => {})
  // The art arrives a moment after the panel.
  await panel
    .locator('img')
    .evaluate((img) => img.complete || new Promise((r) => img.addEventListener('load', r, { once: true })))
    .catch(() => {})
  const text = (await panel.innerText().catch(() => '')).replace(/\n/g, ' | ')
  await page.screenshot({ path: `${SHOTS}/${shot}.png` })
  const box = await panel.boundingBox().catch(() => null)
  if (box) await page.screenshot({ path: `${SHOTS}/${shot}-panel.png`, clip: box })
  return { text, panel }
}

const airing = await hoverFirst('RELEASING', 'hover-airing')
check(airing.text.length > 0, 'the panel opens on an airing show', airing.text.slice(0, 160))
check(/AIRING/i.test(airing.text), 'it says the show is airing')
check(/\d+\/\d+ EPS/i.test(airing.text), 'episodes shown as out/total', airing.text.match(/\d+(\/\d+)? EPS/i)?.[0] ?? '')
check(/EP \d+ in /i.test(airing.text), 'the next episode countdown is there')
check((await airing.panel.locator('a[href*="anilist.co/anime/"]').count()) === 1, 'an AniList link')
check((await airing.panel.locator('a[href^="/anime/"]').count()) === 1, 'a Details link')

const finished = await hoverFirst('FINISHED', 'hover-finished')
check(/FINISHED/i.test(finished.text), 'a finished show says so')
check(/\b\d+ EPS\b/i.test(finished.text) && !/\d+\/\d+ EPS/i.test(finished.text), 'a finished show reads "13 eps", not a fraction', finished.text.match(/\d+(\/\d+)? EPS/i)?.[0] ?? '')
check(/STUDIO/i.test(finished.text), 'the studio is listed')
check(/AIRED/i.test(finished.text), 'the air date is listed')
check(/\d{4} – \w{3} \d/.test(finished.text), 'a finished show gives its whole run, start to end', finished.text.match(/AIRED \| ([^|]+)/i)?.[1] ?? '')
check(/ – now/.test(airing.text), 'an airing show runs to now')
check((await finished.panel.locator('a[href*="myanimelist.net/anime/"]').count()) === 1, 'a MyAnimeList link')

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
