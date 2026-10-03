// AniList's total is a placeholder for paged answers, so kuro measures it: the
// pager says "Page 1 of N" from the first page and agrees with the server's count.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'
const PER_PAGE = 42

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const pager = page.getByText(/^Page \d/)

for (const [label, filters] of [
  ['a search', 'q=gundam'],
  ['filters only', 'genres=Romance&formats=TV&year=2024'],
]) {
  // A busy AniList budget answers 503; retry for a minute like the page does.
  let count, answer
  for (let i = 0; i < 13 && count === undefined; i++) {
    const r = await fetch(`${BASE}/api/browse/count?${filters}&perPage=${PER_PAGE}`)
    answer = `${r.status} ${await r.text()}`
    if (r.status === 503) await new Promise((done) => setTimeout(done, 5000))
    else count = JSON.parse(answer.slice(4)).count
  }
  if (count === undefined) console.log(`  count answered ${answer}`)
  const pages = Math.ceil(count / PER_PAGE)
  check(count > PER_PAGE && count < 5000, `${label}: the server counts the real results`, String(count))

  await page.goto(`${BASE}/browse?${filters}`, { waitUntil: 'domcontentloaded' })
  await pager.waitFor({ timeout: 60000 })
  const ofN = new RegExp(`^Page 1 of ${pages}$`)
  const first = await page.getByText(ofN).waitFor({ timeout: 60000 }).then(() => true, () => false)
  check(first, `${label}: page 1 already says "of ${pages}"`, (await pager.innerText()).trim())
  const body = await page.locator('body').innerText()
  check(body.includes(`${count.toLocaleString()} results`), `${label}: the result count is exact`, String(count))
  await page.screenshot({ path: `${SHOTS}/paging-${label.replace(/\W+/g, '-')}.png` })

  await page.getByRole('button', { name: 'Next' }).click()
  await page.waitForURL(/page=2/)
  const second = await page.getByText(new RegExp(`^Page 2 of ${pages}$`)).waitFor({ timeout: 60000 }).then(() => true, () => false)
  check(second, `${label}: page 2 keeps the count`, (await pager.innerText()).trim())
  if (pages === 2) {
    check(await page.getByRole('button', { name: 'Next' }).isDisabled(), `${label}: next is disabled on the last page`)
  }
}

await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
