// Finished downloads of one show are listed as one group; a later season is its own entry and group.
// The list is fed to the page, so no torrent is involved.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const GB = 1 << 30
const download = (infoHash, animeId, title, episodes, more = {}) => ({
  infoHash, animeId, title, name: `[Group] ${title} - ${episodes.join('-')} [1080p].mkv`,
  episode: episodes.join(', '), episodes, totalBytes: GB, bytesOnDisk: GB, percent: 100,
  pinned: false, kept: false, state: 'paused', ...more,
})
let items = [
  download('a3', 1, 'Show One', ['3']),
  download('b1', 2, 'Show One Season 2', ['1'], { kept: true }),
  download('a1', 1, 'Show One', ['1'], { kept: true }),
  download('c1', 3, 'Lone Film', ['1']),
  download('a2', 1, 'Show One', ['2'], { pinned: true }),
  download('b2', 2, 'Show One Season 2', ['2', '3'], { kept: true }),
  download('d5', 4, 'Still Coming', ['5'], { percent: 40, bytesOnDisk: GB * 0.4, state: 'live' }),
  download('d6', 4, 'Still Coming', ['6'], { percent: 10, bytesOnDisk: GB * 0.1, state: 'live' }),
]

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

const calls = []
// One episode waiting its turn, and a queue that can be paused as a whole.
let paused = false
const queued = [{ animeId: 9, epKey: '2', episode: 2, state: 'pending', title: 'Queued Show' }]
const queueCalls = []
await page.route('**/api/download/queue', (route) => route.fulfill({ json: { items: queued, waiting: {}, paused } }))
await page.route(/\/api\/download\/queue\/(pause|resume|now)$/, (route) => {
  const verb = route.request().url().split('/').pop()
  queueCalls.push(`${verb}${verb === 'now' ? ` ${route.request().postData()}` : ''}`)
  if (verb !== 'now') paused = verb === 'pause'
  return route.fulfill({ json: { paused, moved: true } })
})
await page.route('**/api/downloads', (route) => route.fulfill({ json: { items, count: items.length } }))
await page.route(/\/api\/downloads\/[a-z0-9]+(\/(keep|unkeep))?$/, (route) => {
  const [, hash, verb] = route.request().url().match(/downloads\/([a-z0-9]+)(?:\/(keep|unkeep))?$/)
  const method = route.request().method()
  calls.push(`${method} ${hash}${verb ? ` ${verb}` : ''}`)
  if (method === 'DELETE') items = items.filter((d) => d.infoHash !== hash)
  else items = items.map((d) => (d.infoHash === hash ? { ...d, kept: verb === 'keep' } : d))
  return route.fulfill({ json: { infoHash: hash, kept: verb === 'keep' } })
})

await page.goto(`${BASE}/downloads`, { waitUntil: 'domcontentloaded' })
const toggle = (title) => page.getByRole('button', { name: new RegExp(`downloads of ${title}$`) })
const rowOf = (title) => toggle(title).locator('xpath=ancestor::li')
await toggle('Show One').waitFor({ timeout: 10_000 })

check((await toggle('Show One').count()) === 1, 'the three downloads of a show are one group')
check((await toggle('Show One Season 2').count()) === 1, 'its second season is a group of its own')
check((await toggle('Lone Film').count()) === 0, 'a show with one download stays a plain row')
check((await toggle('Still Coming').count()) === 0, 'unfinished downloads are not grouped')
check((await page.locator('li', { hasText: 'Still Coming' }).count()) === 2, 'each unfinished download keeps its own row')

check(/3 episodes · 3(\.0)? GB/i.test(await rowOf('Show One').innerText()), 'a group counts its episodes and size', (await rowOf('Show One').innerText()).replace(/\n/g, ' | '))
check(/3 episodes/.test(await rowOf('Show One Season 2').innerText()), 'a pack counts every episode it holds')
check(/1 of 3 kept/.test(await rowOf('Show One').innerText()), 'a partly kept group says how many are kept')
check(/Downloaded/.test(await rowOf('Show One Season 2').innerText()), 'a fully kept group reads as downloaded')

// Collapsed until asked; opened, the episodes are in order whatever order they finished in.
check((await page.getByText(/^Episode \d/).count()) === 0, 'groups start collapsed')
await toggle('Show One').click()
const listed = await page.getByText(/^Episode \d/).allInnerTexts()
check(listed.join(',') === 'Episode 1,Episode 2,Episode 3', 'an opened group lists its episodes in order', listed.join(','))
await page.screenshot({ path: `${SHOTS}/downloads-groups.png` })
await toggle('Show One').click()
check((await page.getByText(/^Episode \d/).count()) === 0, 'and closes again')

// Keep all touches only the ones not kept yet.
await rowOf('Show One').getByRole('button', { name: 'Keep all' }).click()
await rowOf('Show One').getByText('Downloaded').waitFor({ timeout: 8000 }).catch(() => {})
check(calls.toSorted().join(' | ') === 'POST a2 keep | POST a3 keep', 'keep all keeps the rest', calls.join(' | '))
check(/Downloaded/.test(await rowOf('Show One').innerText()), 'the group then reads as downloaded')

// Delete all asks first, and leaves the episode that is playing.
calls.length = 0
await rowOf('Show One').getByRole('button', { name: /^Delete every download/ }).click()
check(calls.length === 0, 'deleting a group asks first')
const confirm = rowOf('Show One').getByRole('button', { name: /^Delete 2(\.0)? GB/i })
check((await confirm.count()) === 1, 'the confirmation states what it frees, without the one playing')
await confirm.click()
await page.waitForFunction(() => !document.querySelector('[aria-label^="Show downloads of Show One"]:not([aria-label$="Season 2"])'), null, { timeout: 8000 }).catch(() => {})
check(calls.toSorted().join(' | ') === 'DELETE a1 | DELETE a3', 'only the ones not playing are deleted', calls.join(' | '))
check((await toggle('Show One').count()) === 0, 'one download left: the show is a plain row again')
check((await toggle('Show One Season 2').count()) === 1, 'the other season is untouched')

// Pausing one download lets the next in line start, so stopping everything is its own control.
check((await page.getByRole('status').count()) === 0, 'no paused notice while the queue runs')
await page.getByRole('button', { name: 'Pause all' }).click()
const notice = page.getByRole('status')
check(await notice.waitFor({ timeout: 8000 }).then(() => true, () => false), 'pause all says downloads are paused')
check(queueCalls.join(' | ') === 'pause', 'pause all pauses the queue, not one download', queueCalls.join(' | '))
check((await page.getByRole('button', { name: 'Resume downloads' }).count()) === 1, 'the button turns into resume')
await page.screenshot({ path: `${SHOTS}/downloads-paused.png` })
await notice.getByRole('button', { name: 'Resume' }).click()
await notice.waitFor({ state: 'detached', timeout: 8000 }).catch(() => {})
check(queueCalls.join(' | ') === 'pause | resume' && (await notice.count()) === 0, 'resume starts it again', queueCalls.join(' | '))

// A waiting episode can be started at once.
queueCalls.length = 0
await page.locator('li', { hasText: 'Queued Show' }).getByRole('button', { name: 'Download now' }).click()
await page.waitForTimeout(500)
check(queueCalls.length === 1 && /^now .*"animeId":9.*"epKey":"2"/.test(queueCalls[0]), 'download now asks for that episode', queueCalls.join(' | '))

// Each download is its own outlined card, with a gap before the next.
const cards = await page.locator('ul.space-y-2 > li').evaluateAll((rows) =>
  rows
    .filter((li) => parseFloat(getComputedStyle(li).borderTopWidth) > 0)
    .map((li) => ({ top: li.getBoundingClientRect().top, bottom: li.getBoundingClientRect().bottom })),
)
const gaps = cards.slice(1).map((c, i) => Math.round(c.top - cards[i].bottom))
check(cards.length >= 4 && gaps.every((g) => g >= 6), 'downloads are separate cards with space between', `${cards.length} cards, gaps ${gaps.join(',')}`)

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
