// End-to-end check of the hand controls: mark watched/unwatched ticks, the
// score menu, favourites and notes, the release picker, the random button, the
// orphan clean-up, and the folded season-pack row on Downloads. Run through
// run-player-check.mjs with CHECK=features-check.mjs.
import { spawnSync } from 'node:child_process'
import { copyFileSync } from 'node:fs'
import { join } from 'node:path'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const LIB = process.env.KURO_LIB
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const shots = process.env.SHOTS ?? '.'

const api = async (path, init) => {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) },
  })
  let body
  try {
    body = await res.json()
  } catch {
    body = undefined
  }
  return { ok: res.ok, status: res.status, body }
}
const post = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) })

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 8000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(150)
  }
  return false
}

// ---------------------------------------------------------------- setup
for (let i = 0; i < 60; i++) {
  try {
    if ((await api('/api/setup')).ok) break
  } catch {}
  await sleep(1000)
}
await post('/api/local/paths', { paths: [LIB] })
await post('/api/local/scan', {})
let file
for (let i = 0; i < 30 && !file; i++) {
  await sleep(1000)
  const files = await api('/api/local/files')
  file = (files.body?.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show'))
}
check(!!file, 'test file listed')
if (!file) process.exit(1)
await post('/api/local/assign', { id: file.id, animeId: ANIME, episode: 1 })
// Episode 3 is where auto-next lands; a local copy keeps the run off the swarm.
copyFileSync(join(LIB, 'Kuro Test Show - 01.mkv'), join(LIB, 'third.mkv'))
await post('/api/local/scan', {})
let third
for (let i = 0; i < 30 && !third; i++) {
  await sleep(1000)
  const files = await api('/api/local/files')
  third = (files.body?.items ?? []).find((f) => String(f.path ?? '').includes('third.mkv'))
}
check(!!third, 'second local file scanned')
await post('/api/local/assign', { id: third.id, animeId: ANIME, episode: 3 })

// Several sections plant rows straight into the database; without it the
// run cannot mean anything, so it stops rather than failing them one by one.
if (!process.env.KURO_DB || !process.env.KURO_REPO) {
  console.error('KURO_DB and KURO_REPO are required (run through run-player-check.mjs)')
  process.exit(2)
}
const seed = (...flags) =>
  spawnSync('go', ['run', './scripts/e2e-seed', '-db', process.env.KURO_DB, '-anime', String(ANIME), ...flags], {
    cwd: process.env.KURO_REPO,
    stdio: 'inherit',
  }).status === 0
check(seed('-pack'), 'download rows seeded')

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(`uncaught: ${e.message.slice(0, 200)}`))

// ---------------------------------------------------------------- downloads: pack row
await page.goto(`${BASE}/downloads`, { waitUntil: 'domcontentloaded' })
const packRow = page.locator('li', { hasText: 'Batch' })
check(await until(() => packRow.count().then((n) => n === 1)), 'a season pack is one row')
check(
  await until(() => packRow.innerText().then((t) => /episodes 4, 5/.test(t) && /1\.4 GB of 1\.4 GB/.test(t))),
  'the pack row names every episode and sums their size',
  (await packRow.innerText().catch(() => '')).replace(/\s+/g, ' ').slice(0, 120),
)
await page.screenshot({ path: `${shots}/features-downloads.png` })

// ---------------------------------------------------------------- series page
await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
const episodeRows = page.locator('ul li', { has: page.locator('a[href*="/watch/"]') })
const listed = await until(() => episodeRows.count().then((n) => n > 3), 45_000)
check(listed, 'episode list rendered (needs AniList reachable)')

const tickFor = (n) =>
  page
    .locator('li', { has: page.locator(`a[href$="/watch/${ANIME}/${n}"]`) })
    .getByRole('button', { name: /Mark (not )?watched/ })
    .first()

if (listed) {
  // Mark episode 3 by hand: 1-3 tick, 4 does not.
  await tickFor(3).click()
  check(await until(async () => (await tickFor(3).getAttribute('aria-label')) === 'Mark not watched'), 'episode 3 ticks when marked')
  check(await until(async () => (await tickFor(2).getAttribute('aria-label')) === 'Mark not watched'), 'episodes before it tick too (progress is a count)')
  check((await tickFor(4).getAttribute('aria-label')) === 'Mark watched', 'episode 4 stays unticked')
  let entry = await api(`/api/anime/${ANIME}`)
  check(entry.body?.progress === 3, 'tracker progress is 3', String(entry.body?.progress))

  // Unmark 2: 2 and 3 untick, 1 stays.
  await tickFor(2).click()
  check(await until(async () => (await tickFor(3).getAttribute('aria-label')) === 'Mark watched'), 'unmarking 2 also unticks 3')
  check(await until(async () => (await tickFor(1).getAttribute('aria-label')) === 'Mark not watched'), 'episode 1 stays ticked')
  entry = await api(`/api/anime/${ANIME}`)
  check(entry.body?.progress === 1, 'tracker progress rewound to 1', String(entry.body?.progress))

  // Marking again after an unmark works (the round-trip that used to hang).
  await tickFor(3).click()
  check(await until(async () => (await tickFor(3).getAttribute('aria-label')) === 'Mark not watched'), 'episode 3 ticks again after an unmark')

  // Score menu: readable, picks, persists.
  const scoreButton = page.getByRole('button', { name: 'Your score' })
  await scoreButton.click()
  const eight = page.getByRole('menuitemradio', { name: '8', exact: true })
  check(await eight.isVisible(), 'score menu opens with numbers')
  const colour = await eight.evaluate((el) => getComputedStyle(el).color)
  const bg = await eight.evaluate((el) => getComputedStyle(el.closest('[role=menu]')).backgroundColor)
  check(colour !== bg && !/255, 255, 255/.test(bg), 'score menu is not white on white', `${colour} on ${bg}`)
  await eight.click()
  check(await until(() => scoreButton.innerText().then((t) => t.includes('8/10'))), 'button shows 8/10')
  entry = await api(`/api/anime/${ANIME}`)
  check(entry.body?.score === 80, 'score stored as 80/100', String(entry.body?.score))
  await page.reload({ waitUntil: 'domcontentloaded' })
  check(await until(() => scoreButton.innerText().then((t) => t.includes('8/10')), 45_000), 'score survives a reload')
  await scoreButton.click()
  await page.getByRole('menuitemradio', { name: 'Unrated' }).click()
  check(await until(() => scoreButton.innerText().then((t) => t.includes('Rate'))), 'score can be cleared')

  // Favourite and note.
  const fav = page.getByRole('button', { name: /favourite/i })
  await fav.click()
  check(await until(() => fav.getAttribute('aria-pressed').then((v) => v === 'true')), 'favourite toggles on')
  await page.getByRole('button', { name: 'Add a note' }).click()
  const note = page.getByPlaceholder(/Your note/)
  await note.fill('rewatch in winter')
  await note.blur()
  check(await until(async () => (await api(`/api/anime/${ANIME}`)).body?.bookmark?.note === 'rewatch in winter'), 'note saved on blur')
  await page.screenshot({ path: `${shots}/features-series.png` })

  await page.goto(`${BASE}/library?status=favourites`, { waitUntil: 'domcontentloaded' })
  check(await until(() => page.locator(`a[href="/anime/${ANIME}"]`).count().then((n) => n > 0)), 'favourites tab lists the show')
  const favs = await api('/api/bookmarks')
  check(favs.body?.items?.length === 1, 'bookmarks endpoint has one favourite')
}

// ---------------------------------------------------------------- watch page: picker
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
const choose = page.getByRole('button', { name: 'Choose release' })
check(await until(() => choose.isVisible(), 30_000), 'watch page offers Choose release')
await choose.click()
const dialog = page.getByRole('dialog', { name: 'Choose a release' })
check(await until(() => dialog.isVisible()), 'release picker opens')
check(
  await until(async () => {
    const text = await dialog.innerText()
    return /seeders/.test(text) || /No release found/.test(text)
  }, 60_000),
  'picker lists releases or says there are none',
)
await page.screenshot({ path: `${shots}/features-picker.png` })
await page.keyboard.press('Escape')
check(await until(() => dialog.isVisible().then((v) => !v)), 'picker closes on Escape')

// ---------------------------------------------------------------- browse: random
await page.goto(`${BASE}/browse`, { waitUntil: 'domcontentloaded' })
const random = page.getByRole('button', { name: /Random/ })
await random.click()
check(await until(() => page.url().includes('/anime/'), 30_000), 'random lands on a series page', page.url())
await page.goto(`${BASE}/browse?genres=Romance,Comedy&formats=TV`, { waitUntil: 'domcontentloaded' })
let randomRequest
page.on('request', (r) => {
  if (r.url().includes('/api/random')) randomRequest = r.url()
})
await page.getByRole('button', { name: /Random/ }).click()
check(await until(() => !!randomRequest), 'filtered random asks the server')
check(/genres=Romance%2CComedy/.test(randomRequest ?? '') && /format=TV/.test(randomRequest ?? ''), 'random carries the genre and format filters', randomRequest)

// ---------------------------------------------------------------- settings: orphans
await page.goto(`${BASE}/settings`, { waitUntil: 'domcontentloaded' })
await page.getByRole('tab', { name: 'Quality' }).click()
const clean = page.getByRole('button', { name: 'Clean up' })
check(await clean.isVisible(), 'orphan clean-up is offered')
await clean.click()
check(await until(() => page.getByText(/Removed \d+/).isVisible()), 'clean-up reports what it removed')

// ---------------------------------------------------------------- settings: preferred groups
const groups = page.getByLabel('Preferred release groups')
await groups.fill('SubsPlease, Erai-raws')
await groups.blur()
check(
  await until(async () => (await api('/api/prefs')).body?.effective?.['release.prefer_groups'] === '["SubsPlease","Erai-raws"]'),
  'preferred groups saved as a list',
)
await page.reload({ waitUntil: 'domcontentloaded' })
await page.getByRole('tab', { name: 'Quality' }).click()
check(await until(() => page.getByLabel('Preferred release groups').inputValue().then((v) => v === 'SubsPlease, Erai-raws')), 'preferred groups survive a reload')

// ---------------------------------------------------------------- local files: assign by hand
copyFileSync(join(LIB, 'Kuro Test Show - 01.mkv'), join(LIB, 'mystery clip.mkv'))
await post('/api/local/scan', {})
let mystery
for (let i = 0; i < 30 && !mystery; i++) {
  await sleep(1000)
  const files = await api('/api/local/files?unmatched=true')
  mystery = (files.body?.items ?? []).find((f) => String(f.path ?? '').includes('mystery clip'))
}
check(!!mystery && !mystery.animeId, 'the copy scanned in as unmatched')
await page.goto(`${BASE}/local`, { waitUntil: 'domcontentloaded' })
const row = page.locator('li', { hasText: 'mystery clip' })
await row.getByRole('button', { name: 'Assign' }).click()
await page.getByLabel('Search for the show').fill('Chainsaw Man')
const hit = page.locator('li button', { hasText: /Chainsaw Man/ }).first()
// Guarded: without a hit the clicks below throw and take the rest of the run
// with them, hiding every later check behind one unreachable search.
const offered = await until(() => hit.isVisible(), 30_000)
check(offered, 'search offers a show to pick (AniList reachable)')
if (offered) {
  await hit.click()
  await page.getByLabel('Episode number').fill('9')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  check(
    await until(async () => {
      const files = await api('/api/local/files')
      const f = (files.body?.items ?? []).find((x) => String(x.path ?? '').includes('mystery clip'))
      return f?.animeId === ANIME && f?.episode === 9
    }),
    'file assigned to episode 9 by hand',
  )
  check(await until(() => row.getByRole('link', { name: 'Play ep 9' }).isVisible()), 'row now offers to play it')
}
// The rescan above must not have undone the episode-1 assignment from setup.
const ep1 = (await api('/api/local/files')).body?.items?.find((f) => String(f.path ?? '').includes('Kuro Test Show - 01'))
check(ep1?.animeId === ANIME && ep1?.episode === 1, 'hand assignment survives a rescan', JSON.stringify(ep1 ?? {}).slice(0, 120))
const localPlay = await post('/api/play', { animeId: ANIME, episode: 1 })
check(localPlay.body?.source === 'local', 'episode 1 still plays from the local file', localPlay.body?.source)
await page.screenshot({ path: `${shots}/features-local.png` })

// ---------------------------------------------------------------- player: speed and volume carry over
await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
const video = page.locator('video')
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode loads')
await page.mouse.move(400, 300)
await page.getByRole('button', { name: 'Playback speed' }).click()
await page.getByRole('menuitemradio', { name: '1.5×' }).click()
check(await until(() => video.evaluate((v) => v.playbackRate === 1.5)), 'speed applies to the video')
await video.evaluate((v) => {
  v.volume = 0.3
})
check(await until(() => video.evaluate((v) => Math.abs(v.volume - 0.3) < 0.01)), 'volume set to 30%')
// The change is persisted from the volumechange event, which lands a tick later.
check(await until(() => page.evaluate(() => /"volume":0\.3/.test(localStorage.getItem('kuro.player') ?? ''))), 'volume remembered')
await page.goto(`${BASE}/watch/${ANIME}/9`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'next episode loads')
check(await until(() => video.evaluate((v) => Math.abs(v.volume - 0.3) < 0.01 && v.playbackRate === 1.5)), 'volume and speed carried to the next episode', await video.evaluate((v) => `vol=${v.volume} rate=${v.playbackRate}`))

// ---------------------------------------------------------------- player: controls hide after a keyboard resume
// The pointer never moves again after the first nudge, so only the resume
// itself can re-arm the hide timer.
const cursorHidden = () =>
  page.evaluate(() => !!document.querySelector('.group\\/player')?.classList.contains('cursor-none'))
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode 1 loads for the controls check')
await page.mouse.move(400, 300)
await video.evaluate((v) => v.play().catch(() => {}))
check(await until(cursorHidden, 8000), 'controls fade while playing')
await page.keyboard.press('Space')
check(await until(() => video.evaluate((v) => v.paused)), 'space pauses')
await sleep(4000)
check(!(await cursorHidden()), 'controls stay up while paused')
await page.keyboard.press('Space')
check(await until(() => video.evaluate((v) => !v.paused)), 'space resumes')
check(await until(cursorHidden, 8000), 'controls fade again after a keyboard resume')

// ---------------------------------------------------------------- history stats
// The speed check above played for a few seconds; that is watch time.
await sleep(11_000)
const stats = await api('/api/history/stats')
check((stats.body?.totalSeconds ?? 0) > 0, 'played time is counted', String(stats.body?.totalSeconds))
await page.goto(`${BASE}/history`, { waitUntil: 'domcontentloaded' })
check(await until(() => page.getByLabel('Watch stats').isVisible()), 'history page shows watch stats')

// ---------------------------------------------------------------- search dropdown
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
const search = page.getByRole('combobox', { name: 'Search anime' })
await search.fill('Chainsaw')
const option = page.locator('#kuro-search-results [role=option]').first()
check(await until(() => option.isVisible()), 'typing opens instant results')
check(await until(() => option.innerText().then((t) => /Chainsaw Man/.test(t))), 'the library show is listed', await option.innerText().catch(() => ''))
await search.press('ArrowDown')
await search.press('Enter')
check(await until(() => page.url().endsWith(`/anime/${ANIME}`)), 'keyboard pick opens the series page', page.url())
await search.fill('zzzz nothing')
check(await until(() => page.getByText('Nothing in your library.').isVisible()), 'no local match says so')
check(await page.getByRole('option', { name: /Search AniList for/ }).isVisible(), 'AniList search offered as the last row')
await search.press('Escape')

// ---------------------------------------------------------------- external links + trailer
const aniLink = page.getByRole('link', { name: /AniList/ })
check(await until(() => aniLink.isVisible()), 'AniList link on the series page')
check((await aniLink.getAttribute('href')) === `https://anilist.co/anime/${ANIME}`, 'AniList link targets the show')
const malLink = page.getByRole('link', { name: /MyAnimeList/ })
check(await malLink.isVisible(), 'MyAnimeList link on the series page')
const trailerBtn = page.getByRole('button', { name: /Trailer/ }).first()
if (await trailerBtn.isVisible()) {
  await trailerBtn.click()
  const trailer = page.getByRole('dialog', { name: 'Trailer' })
  check(await until(() => trailer.isVisible()), 'trailer opens over the page')
  check(await trailer.locator('iframe').getAttribute('src').then((s) => /youtube-nocookie\.com\/embed\//.test(s ?? '')), 'trailer is an embedded video')
  check(page.url().endsWith(`/anime/${ANIME}`), 'still on the series page underneath')
  await page.keyboard.press('Escape')
  check(await until(() => trailer.isVisible().then((v) => !v)), 'Escape closes the trailer')
} else {
  console.log('skip trailer: none listed for this show')
}

// ---------------------------------------------------------------- library sort, filter, own score
await post('/api/score', { animeId: ANIME, score: 80 })
await page.goto(`${BASE}/library?sort=score`, { waitUntil: 'domcontentloaded' })
check(await until(() => page.locator(`a[href="/anime/${ANIME}"]`).first().isVisible()), 'library lists the show')
check(await until(() => page.getByTitle('Your score').first().innerText().then((t) => /8\/10/.test(t))), 'own score shown on the card')
await page.getByLabel('Filter library').fill('zzzz')
check(await until(() => page.getByText('No match in your list').isVisible()), 'filter narrows the list to nothing')
await page.getByLabel('Filter library').fill('Chainsaw')
check(await until(() => page.locator(`a[href="/anime/${ANIME}"]`).first().isVisible()), 'filter finds the show')
check(await until(() => page.url().includes('q=Chainsaw')), 'filter lives in the URL')
const sortSelect = page.getByLabel('Sort library')
await sortSelect.selectOption('title')
check(await until(() => page.url().includes('sort=title')), 'sort lives in the URL')

// ---------------------------------------------------------------- filler skip + up next
check(seed('-extras'), 'filler and notification rows seeded')
await post('/api/prefs', { key: 'playback.skip_filler', value: 'true', animeId: ANIME })
const nextApi = await api(`/api/episodes/next?id=${ANIME}&after=1`)
check(nextApi.body?.next === 3, 'next after 1 skips the filler episode 2', String(nextApi.body?.next))
await post('/api/prefs', { key: 'playback.autonext', value: 'false' })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode 1 loads for the up-next check')
await video.evaluate((v) => {
  v.currentTime = v.duration - 0.5
})
const upNext = page.getByRole('dialog', { name: 'Up next' })
check(await until(() => upNext.isVisible(), 20_000), 'up-next card appears at the end')
// Fullscreen paints only the player's subtree, so a card outside it counts
// down where nobody can see it.
check(
  await page.evaluate(() => {
    const card = document.querySelector('[role="dialog"][aria-label="Up next"]')
    return !!card && !!document.querySelector('.group\\/player')?.contains(card)
  }),
  'up-next card sits inside the player',
)
check(await until(() => upNext.innerText().then((t) => /Episode 3/.test(t))), 'up next is episode 3, not the filler', await upNext.innerText().catch(() => ''))
check(await upNext.getByRole('button', { name: 'Play next' }).isVisible(), 'without auto-next it only offers a button')
await upNext.getByRole('button', { name: 'Dismiss' }).click()
check(await until(() => upNext.isVisible().then((v) => !v)), 'dismiss hides the card')

await post('/api/prefs', { key: 'playback.autonext', value: 'true' })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode 1 reloads')
await video.evaluate((v) => {
  v.currentTime = v.duration - 0.5
})
check(await until(() => upNext.isVisible(), 20_000), 'countdown card appears with auto-next on')
check(await until(() => upNext.innerText().then((t) => /Play now · [1-5]/.test(t))), 'card counts down from 5')
await upNext.getByRole('button', { name: 'Cancel' }).click()
await sleep(6500)
check(page.url().endsWith(`/watch/${ANIME}/1`), 'cancel stops the countdown', page.url())
// The ended video sits paused; watching the last half-second again ends it again.
await video.evaluate((v) => {
  v.currentTime = v.duration - 0.5
  void v.play()
})
check(await until(() => page.url().endsWith(`/watch/${ANIME}/3`), 20_000), 'auto-next lands on episode 3 after five seconds', page.url())
// ---------------------------------------------------------------- fullscreen survives the next episode
// Fullscreen belongs to the player element; remounting it between episodes
// drops out of fullscreen.
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode 1 loads for the fullscreen check')
await page.evaluate(() => {
  document.querySelector('.group\\/player').dataset.mark = 'kept'
})
const wentFullscreen = await page
  .evaluate(() => document.querySelector('.group\\/player').requestFullscreen().then(() => true))
  .catch(() => false)
await video.evaluate((v) => {
  v.currentTime = v.duration - 0.5
  void v.play()
})
check(await until(() => page.url().endsWith(`/watch/${ANIME}/3`), 25_000), 'auto-next moved to episode 3', page.url())
check(
  await page.evaluate(() => document.querySelector('.group\\/player')?.dataset.mark === 'kept'),
  'the player element survived the episode change',
)
if (wentFullscreen) {
  check(await page.evaluate(() => !!document.fullscreenElement), 'still fullscreen on the next episode')
} else {
  console.log('ok   (fullscreen not available in this browser; element continuity checked instead)')
}
check(await until(() => video.evaluate((v) => !v.paused), 20_000), 'the next episode plays rather than sitting paused')

// ---------------------------------------------------------------- opening and ending markers
// The release's own chapters name the opening and ending, so they need no
// crowd-sourced timestamp and no guess at the episode's length.
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode 1 loads for the skip check')
const dur = Math.round(await video.evaluate((v) => v.duration))
const marks = await api(`/api/episode/skips?anime=${ANIME}&episode=1&duration=${dur}`)
check(marks.body?.source === 'chapters', 'skip ranges come from the file chapters', marks.body?.source)
const kinds = (marks.body?.ranges ?? []).map((r) => r.kind).join(',')
check(kinds === 'op,ed', 'both an opening and an ending were found', kinds)
const op = (marks.body?.ranges ?? []).find((r) => r.kind === 'op')
check(op && Math.round(op.start) === 10 && Math.round(op.end) === 40, 'the opening carries the chapter times', JSON.stringify(op))
// Chapters are read off the file, so they need no episode length at all.
const guessed = await api(`/api/episode/skips?anime=${ANIME}&episode=1`)
check(guessed.body?.source === 'chapters', 'chapters resolve without a duration', guessed.body?.source)
// The marker is titled "Opening · m:ss–m:ss"; that tooltip is what a viewer sees.
await page.mouse.move(400, 300)
const marker = page.locator('div.group\\/scrub div[title^="Opening"]')
check(await until(() => marker.first().isVisible(), 20_000), 'the opening is marked on the scrub bar',
  await marker.first().getAttribute('title').catch(() => 'absent'))
check(await until(() => page.locator('div.group\\/scrub div[title^="Ending"]').first().isVisible()), 'the ending is marked too')

// ---------------------------------------------------------------- resume survives an in-app switch
// The player is reused between episodes, so the new source has to attach at the
// saved position rather than the one the last episode left behind.
await post('/api/progress', { animeId: ANIME, episode: 3, position: 40, duration: 180, played: 40 })
await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
check(await until(() => video.evaluate((v) => v.readyState >= 1), 60_000), 'episode 1 loads before the switch')
await page.locator(`a[href="/watch/${ANIME}/3"]`).first().click()
check(await until(() => page.url().endsWith(`/watch/${ANIME}/3`)), 'switched episode without a page load', page.url())
check(
  await until(() => video.evaluate((v) => v.currentTime > 25), 45_000),
  'resumes at the saved position after an in-app switch',
  await video.evaluate((v) => `t=${v.currentTime.toFixed(1)}`).catch(() => '?'),
)

const thirdPlay = await post('/api/play', { animeId: ANIME, episode: 3 })
check(thirdPlay.body?.source === 'local', 'episode 3 plays from the local copy, not a torrent', thirdPlay.body?.source)

// ---------------------------------------------------------------- desktop notifications
const notifyContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
await notifyContext.grantPermissions(['notifications'], { origin: BASE })
const notifyPage = await notifyContext.newPage()
await notifyPage.addInitScript(() => {
  window.__notes = []
  class FakeNotification {
    constructor(title, opts) {
      window.__notes.push({ title, body: opts?.body })
    }
    static permission = 'granted'
    static requestPermission() {
      return Promise.resolve('granted')
    }
  }
  window.Notification = FakeNotification
})
await notifyPage.goto(`${BASE}/settings?tab=Notifications`, { waitUntil: 'domcontentloaded' })
const desktopSwitch = notifyPage.getByRole('switch', { name: 'Desktop alerts' })
check(await until(() => desktopSwitch.isVisible()), 'desktop alerts switch offered')
await desktopSwitch.click()
check(await until(() => notifyPage.evaluate(() => localStorage.getItem('kuro.notify.desktop') === '1')), 'desktop alerts switch turns on')
// First sight only records the backlog; a newer item after that is announced.
await notifyPage.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
await sleep(1500)
check(await notifyPage.evaluate(() => window.__notes.length === 0), 'existing notifications are not replayed')
await post('/api/prefs', { key: 'notify.enabled', value: 'true' })
check(seed('-extras'), 'a fresh notification seeded')
await notifyPage.reload({ waitUntil: 'domcontentloaded' })
check(
  await until(() => notifyPage.evaluate(() => window.__notes.length === 1 && /Episode 7/.test(window.__notes[0].title)), 10_000),
  'a new notification pops up on the desktop',
  await notifyPage.evaluate(() => JSON.stringify(window.__notes)),
)
await notifyContext.close()

// ---------------------------------------------------------------- mobile double-tap seek
const phone = await browser.newContext({ viewport: { width: 420, height: 800 }, hasTouch: true, isMobile: true })
const phonePage = await phone.newPage()
await phonePage.goto(`${BASE}/watch/${ANIME}/9`, { waitUntil: 'domcontentloaded' })
const pv = phonePage.locator('video')
check(await until(() => pv.evaluate((v) => v.readyState >= 1), 60_000), 'phone player loads')
await pv.evaluate((v) => {
  v.currentTime = 30
  v.pause()
})
const pbox = await pv.boundingBox()
const tapAt = async (fx) => {
  await phonePage.touchscreen.tap(pbox.x + pbox.width * fx, pbox.y + pbox.height / 2)
}
await tapAt(0.85)
await sleep(80)
await tapAt(0.85)
check(await until(() => pv.evaluate((v) => v.currentTime >= 39 && v.currentTime <= 42)), 'double tap on the right seeks forward ten seconds', String(await pv.evaluate((v) => v.currentTime)))
await pv.evaluate((v) => {
  v.pause()
  v.currentTime = 40
})
await sleep(400)
await tapAt(0.15)
await sleep(80)
await tapAt(0.15)
check(await until(() => pv.evaluate((v) => v.currentTime >= 29 && v.currentTime <= 31.5)), 'double tap on the left seeks back ten seconds', String(await pv.evaluate((v) => v.currentTime)))
// A lone tap in the middle still toggles play.
await pv.evaluate((v) => v.pause())
await sleep(400)
await tapAt(0.5)
check(await until(() => pv.evaluate((v) => !v.paused)), 'single tap plays')
await phone.close()

// ---------------------------------------------------------------- hentai filter
const hentai = await api('/api/filters')
check((hentai.body?.genres ?? []).includes('Hentai'), 'Hentai is in the genre vocabulary (AniList reachable)')

await browser.close()
check(errors.length === 0, 'no page errors', errors.join(' | '))
console.log(`\n${failures === 0 ? 'all checks passed' : `${failures} check(s) failed`}`)
process.exit(failures === 0 ? 0 : 1)
