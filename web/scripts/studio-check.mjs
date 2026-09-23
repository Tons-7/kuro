// A show's studio is on its page and links to the studio's other work; the
// Browse filter finds a studio by name. Chainsaw Man is MAPPA.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)

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
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
const studio = page.locator('a[href^="/browse?studio="]').first()
check(await until(() => studio.isVisible()), 'the studio is shown on the series page', await studio.innerText().catch(() => ''))
await page.screenshot({ path: `${SHOTS}/studio-anime.png` })

await studio.click()
check(await until(async () => page.url().includes('/browse?studio=')), 'the studio opens Browse', page.url())
const chip = page.getByText(/^Studio: /)
check(await until(() => chip.isVisible()), 'Browse shows the studio filter', await chip.innerText().catch(() => ''))
const cards = page.locator('a[href^="/anime/"]')
check(await until(async () => (await cards.count()) > 3), "the studio's other work is listed", `${await cards.count()} cards`)
check(
  await until(async () => (await page.locator(`a[href="/anime/${ANIME}"]`).count()) > 0),
  'the show itself is among them',
)
await page.screenshot({ path: `${SHOTS}/studio-browse.png` })

// Clearing it, then finding a studio by name.
await page.getByRole('button', { name: /Remove Studio/ }).click()
check(await until(async () => !page.url().includes('studio=')), 'clearing the studio drops it from the URL')
await page.getByRole('button', { name: 'Studio', exact: true }).click()
await page.getByPlaceholder('Search studios…').fill('ufotable')
const option = page.getByRole('option', { name: /ufotable/i })
check(await until(() => option.first().isVisible()), 'searching finds the studio')
await option.first().click()
check(await until(async () => page.url().includes('studioName=ufotable')), 'picking it filters Browse', page.url())
check(await until(async () => (await cards.count()) > 3), 'its work is listed', `${await cards.count()} cards`)

// Narrowing a studio's list by format happens on the server.
const res = await (await fetch(`${BASE}/api/browse?studio=569&formats=MOVIE`)).json()
check(
  (res.items ?? []).length > 0 && res.items.every((i) => i.format === 'MOVIE'),
  'format narrows a studio list',
  `${(res.items ?? []).length} films`,
)

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
