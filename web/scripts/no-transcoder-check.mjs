// Only the torrent engine installed: the watch page says the transcoder is
// missing and points to Setup, instead of a raw ffprobe error.
//   BIN_ONLY=none node scripts/run-player-check.mjs no-transcoder-check.mjs
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = process.env.KURO_ANIME ?? '127230'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

check(await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }), 'test episode ready')

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
const install = page.getByRole('link', { name: 'Install the transcoder' })
check(await install.waitFor({ timeout: 90000 }).then(() => true, () => false), 'offers to install the transcoder')
const text = await page.locator('body').innerText()
check(/needs the transcoder/.test(text), 'says what is missing')
check(!/fork\/exec|ffprobe/.test(text), 'no raw ffprobe error')
await page.screenshot({ path: `${SHOTS}/no-transcoder.png` })

await install.click()
check(await page.waitForURL(/\/setup/, { timeout: 10000 }).then(() => true, () => false), 'leads to Setup')

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
