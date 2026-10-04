// The transcoder row says what it is for, and explains half an install. Run with BIN_ONLY=ffmpeg.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

const setup = await (await fetch(`${BASE}/api/setup`)).json()
const row = setup.components.find((c) => c.name === 'ffmpeg')
check(row && !row.present, 'ffmpeg without ffprobe does not count as installed', JSON.stringify({ present: row?.present }))
check(/ffmpeg is on this system but ffprobe is not/.test(row?.problem ?? ''), 'the server says which half is missing', row?.problem)

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
await page.goto(`${BASE}/setup`, { waitUntil: 'load', timeout: 60000 })

const item = page.locator('li').filter({ hasText: 'Transcoder' }).first()
await item.waitFor({ timeout: 30000 })
const text = await item.innerText()
check(text.includes('Needed for the built-in player'), 'the badge says what it is needed for')
check(!/\bOptional\b/.test(text), 'and no longer says Optional')
check(text.includes('ffmpeg and ffprobe') && text.includes('mpv or VLC'), 'the text names both programs and who can skip them')
check(text.includes('ffprobe is not; kuro needs both'), 'the page shows which half is missing')
check(await item.getByRole('button', { name: /install/i }).isVisible(), 'and still offers to install the pair')
await page.screenshot({ path: `${SHOTS}/setup-transcoder.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `${failures} failed` : 'all passed')
process.exit(failures ? 1 : 0)
