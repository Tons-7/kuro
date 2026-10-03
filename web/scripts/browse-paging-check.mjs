// AniList's total is a placeholder until the last page, so the pager must not
// claim a page count it can't know. "gundam" is two pages of 42.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const pager = page.getByText(/^Page \d/)

await page.goto(`${BASE}/browse?q=gundam`, { waitUntil: 'domcontentloaded' })
await pager.waitFor({ timeout: 60000 })
const first = await pager.innerText()
check(/^Page 1$/.test(first.trim()), 'page 1 claims no page count', first)
check(/42\+ results/.test(await page.locator('body').innerText()), 'count reads as a lower bound')
await page.screenshot({ path: `${SHOTS}/paging-1.png` })

await page.getByRole('button', { name: 'Next' }).click()
await page.waitForURL(/page=2/)
await page.getByText(/^Page 2 of 2$/).waitFor({ timeout: 60000 }).catch(() => {})
const second = await pager.innerText()
check(/^Page 2 of 2$/.test(second.trim()), 'the last page knows the count', second)
check(await page.getByRole('button', { name: 'Next' }).isDisabled(), 'next is disabled on the last page')

await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
