// A release group preferred for one show only, and the release picking that
// follows from it.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)

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
const prefs = async (anime) =>
  (await fetch(`${BASE}/api/prefs${anime ? `?anime=${anime}` : ''}`)).json()

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)))

await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
const open = page.getByRole('button', { name: /Prefer a release group/ })
check(await until(() => open.isVisible()), 'the series page offers a per-show group')
await open.click()

const field = page.getByLabel('Prefer for this show')
check(await until(() => field.isVisible()), 'the field opens')
await field.fill('SubsPlease, Erai-raws')
await field.blur()

check(
  await until(async () => {
    const p = await prefs(ANIME)
    return p.overrides?.['release.prefer_groups'] === '["SubsPlease","Erai-raws"]'
  }),
  'the groups are stored for this show',
  JSON.stringify((await prefs(ANIME)).overrides?.['release.prefer_groups']),
)

const global = await prefs()
check(
  (global.effective?.['release.prefer_groups'] ?? '[]') === '[]',
  'the account default is untouched',
  String(global.effective?.['release.prefer_groups']),
)
check(
  (await prefs(ANIME)).effective?.['release.prefer_groups'] === '["SubsPlease","Erai-raws"]',
  'and the show resolves to its own',
)

await page.reload({ waitUntil: 'domcontentloaded' })
check(await until(() => page.getByLabel('Prefer for this show').inputValue().then((v) => v === 'SubsPlease, Erai-raws')), 'it survives a reload')

await page.getByRole('button', { name: 'Clear' }).click()
check(
  await until(async () => !(await prefs(ANIME)).overrides?.['release.prefer_groups']),
  'Clear removes the override',
)

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
