// Keys while watching: D shows the downloads over the player without stopping it, S skips the opening,
// ? lists every shortcut. The downloads list and skip range are fed to the page.
import { chromium } from 'playwright'
import { prepareLocalEpisode } from './harness-lib.mjs'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const SHOTS = process.env.SHOTS ?? '.'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}

check(await prepareLocalEpisode({ base: BASE, lib: process.env.KURO_LIB, anime: ANIME }), 'test episode ready')

const GB = 1 << 30
const download = (infoHash, name, more = {}) => ({
  infoHash, name, episodes: [], totalBytes: GB, bytesOnDisk: GB * 0.4, percent: 40,
  pinned: false, kept: false, state: 'live', mbps: 1.2, peers: 2, ...more,
})
const items = [
  download('a3', '[Group] Show One - 03 [1080p].mkv', { animeId: 1, title: 'Show One', episode: '3', episodes: ['3'], pinned: true }),
  download('x1', '[Stray] Other Show - 01 [1080p].mkv', { percent: 5, bytesOnDisk: GB * 0.05, mbps: 0 }),
  download('b1', '[Group] Show Two - 01 [1080p].mkv', { animeId: 2, title: 'Show Two', episode: '1', episodes: ['1'], percent: 100, bytesOnDisk: GB }),
]
const queued = [{ animeId: 3, epKey: '7', episode: 7, state: 'failed', error: 'no release found', title: 'Show Three' }]

const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'] })
const page = await context.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
await page.route('**/api/download/queue', (route) => route.fulfill({ json: { items: queued, waiting: {} } }))
await page.route('**/api/downloads', (route) => route.fulfill({ json: { items, count: items.length } }))
// An opening over the first minute and a half, so the skip button shows from the start.
await page.route('**/api/episode/skips**', (route) => route.fulfill({ json: { ranges: [{ kind: 'op', start: 0, end: 90 }] } }))

await page.goto(`${BASE}/watch/${ANIME}/1`, { waitUntil: 'domcontentloaded' })
const video = page.locator('video').first()
await video.waitFor({ timeout: 30_000 })
await page.waitForFunction(() => (document.querySelector('video')?.readyState ?? 0) >= 2, null, { timeout: 30_000 }).catch(() => {})
const state = () => video.evaluate((v) => ({ paused: v.paused, time: v.currentTime, duration: v.duration }))
// Keys go to the page, not to a control that happens to hold focus.
await page.locator('body').click({ position: { x: 5, y: 400 } })

const panel = page.getByRole('dialog', { name: 'Downloads' })
check((await panel.count()) === 0, 'the downloads panel starts closed')
const before = await state()
await page.keyboard.press('d')
check(await panel.waitFor({ timeout: 3000 }).then(() => true, () => false), 'D opens the downloads over the player')
const text = (await panel.innerText().catch(() => '')).replace(/\n/g, ' | ')
check(/Show One episode 3/.test(text) && /40%/.test(text) && /1\.2 Mbps/.test(text) && /2 peers/.test(text), 'a download shows its show, progress, speed and peers', text)
check(/\[Stray\] Other Show - 01/.test(text), 'a download under no show is listed by its release name')
check(/Show Three episode 7/.test(text) && /no release found/.test(text), 'a failed one says why')
check(!/Show Two/.test(text) && /1 on disk/.test(text), 'finished ones are counted, not listed')
check(page.url().includes('/watch/'), 'the episode page is still open')
check((await state()).paused === before.paused, 'opening it does not pause or start the video')
await sleep(600)
await page.screenshot({ path: `${SHOTS}/shortcuts-downloads.png` })

// The player keeps its keys while the panel is up.
await page.keyboard.press('k')
await sleep(400)
check((await state()).paused !== before.paused, 'K still plays or pauses with the panel open')
await page.keyboard.press('k')

await panel.getByRole('button', { name: 'Copy details' }).click()
await sleep(300)
const copied = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => `unreadable: ${e}`)
check(/Downloading \(2\)/.test(copied) && /Show One episode 3 — 40%/.test(copied) && /\[Stray\] Other Show/.test(copied) && /Failed \(1\)/.test(copied) && /On disk \(1\)/.test(copied),
  'copy details puts the whole list on the clipboard as text', copied.replace(/\n/g, ' | ').slice(0, 300))

await page.keyboard.press('d')
await sleep(300)
check((await panel.count()) === 0, 'D closes it again')
await page.keyboard.press('d')
await panel.waitFor({ timeout: 3000 }).catch(() => {})
await page.keyboard.press('Escape')
await sleep(300)
check((await panel.count()) === 0, 'Escape closes it too')

// Typing a "d" is not a shortcut.
await page.locator('#kuro-search').fill('d')
await page.locator('#kuro-search').press('d')
await sleep(300)
check((await panel.count()) === 0, 'typing in search does not open it')
await page.locator('#kuro-search').fill('')
await page.locator('body').click({ position: { x: 5, y: 400 } })

// S is the skip button, pressed.
const skip = page.getByRole('button', { name: /^Skip opening/ })
check(await skip.waitFor({ timeout: 8000 }).then(() => true, () => false), 'the skip button shows during the opening')
await video.evaluate((v) => (v.currentTime = 1))
await page.keyboard.press('s')
await sleep(800)
const after = await state()
check(after.time >= Math.min(90, after.duration - 1) - 1, 'S jumps to the end of the opening', JSON.stringify(after))

// ? lists the keys, the new ones included.
await page.keyboard.press('?')
const help = page.getByRole('dialog', { name: 'Keyboard shortcuts' })
check(await help.waitFor({ timeout: 3000 }).then(() => true, () => false), '? opens the list of shortcuts')
const listed = await help.innerText().catch(() => '')
check(/Play or pause/.test(listed) && /Skip the opening/.test(listed) && /Show or hide downloads/.test(listed) && /Subtitles earlier or later/.test(listed), 'it lists the player keys and the new ones')
await sleep(600)
await page.screenshot({ path: `${SHOTS}/shortcuts-list.png` })
await page.keyboard.press('Escape')
await sleep(300)
check((await help.count()) === 0, 'Escape closes the list')

// The player teaches its own keys: each button's tooltip names one, and one button opens the list.
const tip = (name) => page.getByRole('button', { name, exact: true }).first().getAttribute('title').catch(() => null)
const tips = [await tip(/^(Play|Pause)$/), await tip('Back 10 seconds'), await tip('Forward 10 seconds'), await tip(/^(Mute|Unmute)$/), await tip('Fullscreen')]
check(tips.join(' | ') && /\(K\)/.test(tips[0]) && /\(J\)/.test(tips[1]) && /\(L\)/.test(tips[2]) && /\(M\)/.test(tips[3]) && /\(F\)/.test(tips[4]),
  'player buttons name their key in the tooltip', tips.join(' | '))
await page.locator('video').first().hover()
await page.getByRole('button', { name: 'Keyboard shortcuts', exact: true }).click()
check(await help.waitFor({ timeout: 3000 }).then(() => true, () => false), 'the keyboard button in the player bar opens the list')
await page.keyboard.press('Escape')
await sleep(300)

// From the profile menu, for whoever does not know the key.
await page.getByRole('button', { name: 'Profile and settings' }).click()
await page.getByRole('menuitem', { name: /Keyboard shortcuts/ }).click()
check(await help.waitFor({ timeout: 3000 }).then(() => true, () => false), 'the profile menu opens the list as well')
await page.keyboard.press('Escape')

// Fullscreen shows only what is inside the fullscreen element, so the panel has to be drawn there.
await page.locator('body').click({ position: { x: 5, y: 400 } })
await page.keyboard.press('f')
const full = await page.waitForFunction(() => !!document.fullscreenElement, null, { timeout: 4000 }).then(() => true, () => false)
if (!full) {
  console.log('skip the fullscreen steps: this browser refused fullscreen')
} else {
  await page.keyboard.press('d')
  await panel.waitFor({ timeout: 3000 }).catch(() => {})
  const inside = await page.evaluate(() => !!document.fullscreenElement?.querySelector('[role=dialog][aria-label=Downloads]'))
  check(inside, 'in fullscreen the panel is drawn inside the player')
  await sleep(600)
  await page.screenshot({ path: `${SHOTS}/shortcuts-fullscreen.png` })
  await page.keyboard.press('d')
}

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
