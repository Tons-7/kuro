// A card has to show the list tag it already carries: a completed show reads
// "Completed", not "Add to list". And a long-runner with no announced total
// still shows how far along it is.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 20000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return true
    await new Promise((r) => setTimeout(r, 400))
  }
  return false
}
const post = (p, b) =>
  fetch(BASE + p, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b) })

await post('/api/status', { animeId: ANIME, status: 'COMPLETED' })

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

// The API must carry the list tag, not just "on the list".
const browse = await (await fetch(`${BASE}/api/browse?q=Chainsaw%20Man&perPage=10`)).json()
const item = (browse.items ?? []).find((i) => i.id === ANIME)
check(!!item, 'the show is in the browse results')
check(item?.onList === true, 'the API says it is on the list')
check(item?.listStatus === 'COMPLETED', 'the API says which list', String(item?.listStatus))

// Home's rails come from a different endpoint, which used to build its cards
// with its own copy of the code and forget the list tag. Tag one of its own
// results so the assertion cannot pass by finding nothing.
const trending = await (await fetch(`${BASE}/api/discover?sort=trending&perPage=20`)).json()
const pick = (trending.items ?? [])[0]
check(!!pick, 'the home rail returned something to tag')
if (pick) {
  await post('/api/status', { animeId: pick.id, status: 'PAUSED' })
  const again = await (await fetch(`${BASE}/api/discover?sort=trending&perPage=20`)).json()
  const tagged = (again.items ?? []).find((i) => i.id === pick.id)
  check(tagged?.onList === true, 'the home rail sees it on the list', pick.title)
  check(tagged?.listStatus === 'PAUSED', 'and carries which list', String(tagged?.listStatus))
}

await page.goto(`${BASE}/browse?q=${encodeURIComponent('Chainsaw Man')}`, { waitUntil: 'domcontentloaded' })
const card = page.locator(`a[href="/anime/${ANIME}"]`).first()
check(await until(() => card.isVisible()), 'the card is on the browse page')
await card.hover()

const tag = page.locator(`:below(a[href="/anime/${ANIME}"])`)
const bookmark = page
  .locator('div')
  .filter({ has: page.locator(`a[href="/anime/${ANIME}"]`) })
  .getByRole('button', { name: /Listed as|Add to list/ })
  .first()
check(await until(() => bookmark.isVisible()), 'the bookmark shows on hover')
const label = await bookmark.getAttribute('aria-label')
check(label === 'Listed as Completed', 'the bookmark says it is already completed', String(label))
void tag

// A show with no announced total: the card measures against what has aired.
const lib = await (await fetch(`${BASE}/api/library?status=&perPage=50`)).json()
const ongoing = (lib.items ?? []).find((i) => !i.episodes && i.nextEpisode)
if (ongoing) {
  await page.goto(`${BASE}/library`, { waitUntil: 'domcontentloaded' })
  const row = page.locator('div').filter({ has: page.locator(`a[href="/anime/${ongoing.id}"]`) }).first()
  check(await until(() => row.isVisible()), 'the ongoing show is in the library')
  const text = (await row.innerText()).replace(/\s+/g, ' ')
  check(/\d+ aired/.test(text), 'its card counts what has aired', text.slice(0, 80))
} else {
  console.log('note  no show without an episode total in this library; skipped that half')
}

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
