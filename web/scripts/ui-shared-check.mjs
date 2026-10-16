// The shared pieces: inline confirms, the pager's clamp, Settings tabs, folder refusals, a job that didn't start.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const until = async (fn, ms = 8000) => {
  for (const end = Date.now() + ms; Date.now() < end; await new Promise((r) => setTimeout(r, 100))) {
    if (await fn().catch(() => false)) return true
  }
  return false
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
const tab = (name) => page.getByRole('tab', { name, exact: true })

// The tab lives in the URL: a reload keeps it, and a link to another one switches even from here.
await page.goto(`${BASE}/settings`, { waitUntil: 'domcontentloaded' })
await tab('Quality').click()
check(await until(async () => new URL(page.url()).searchParams.get('tab') === 'Quality'), 'a picked tab is in the URL', page.url())
await page.reload({ waitUntil: 'domcontentloaded' })
check(await until(() => tab('Quality').getAttribute('aria-selected').then((v) => v === 'true')), 'a reload keeps the tab')
await page.goto(`${BASE}/settings?tab=About`, { waitUntil: 'domcontentloaded' })
await tab('Playback').click()
await page.evaluate(() => {
  history.pushState(null, '', '/settings?tab=About')
  dispatchEvent(new PopStateEvent('popstate'))
})
check(await until(() => tab('About').getAttribute('aria-selected').then((v) => v === 'true')), 'the same ?tab= link again switches back')

// The inline confirm on a faked network-open Access tab (this instance listens on 127.0.0.1 alone).
let rotated = 0
await page.route('**/api/access', (route) =>
  route.fulfill({
    json: { reachable: true, host: true, canSwitch: false, listening: '0.0.0.0:4410', urls: ['http://192.168.1.20:4410/?t=x'] },
  }),
)
await page.route('**/api/access/rotate', (route) => {
  rotated++
  return route.fulfill({ json: {} })
})
// A fresh load: the access state fetched at startup is kept a while, so the fake would go unasked.
await page.goto(`${BASE}/settings?tab=Access`, { waitUntil: 'domcontentloaded' })
const signOut = page.getByRole('button', { name: 'Sign out all devices' })
if (await signOut.waitFor({ timeout: 8000 }).then(() => true, () => false)) {
  await signOut.click()
  const cancel = page.getByRole('button', { name: 'Cancel', exact: true })
  check(await until(() => page.getByRole('button', { name: 'Sign them out' }).isVisible()), 'the confirm names the act')
  check(await until(() => cancel.evaluate((el) => el === document.activeElement)), 'Cancel takes the focus, so a second Enter does no harm')
  await page.keyboard.press('Escape')
  check(await until(() => signOut.isVisible()), 'Escape backs out of the confirm')
  check(rotated === 0, 'backing out signs nobody out')
} else {
  check(false, 'the Access tab offers to sign every device out')
}
await page.unroute('**/api/access')
await page.unroute('**/api/access/rotate')

// A job the server refused says so under its row.
await page.route('**/api/jobs/*', (route) =>
  route.request().method() === 'POST' ? route.fulfill({ status: 500, json: { error: 'job exploded' } }) : route.fallback(),
)
await tab('Jobs').click()
const run = page.getByRole('button', { name: 'Run', exact: true }).first()
await run.waitFor({ timeout: 8000 })
await run.click()
check(await until(() => page.getByText("Didn't start: job exploded").isVisible()), 'a refused job run is said, not swallowed')
await page.unroute('**/api/jobs/*')

// Library folders: the refused path and why, beside the list, and the text stays to correct.
await tab('Library').click()
const folder = page.getByRole('textbox', { name: 'Folder to add' })
await folder.waitFor({ timeout: 8000 })
const bad = 'Z:\\no\\such\\folder\\kuro-check'
await folder.fill(bad)
await page.getByRole('button', { name: 'Add', exact: true }).click()
check(await until(() => page.getByText(`${bad}: not readable`).isVisible()), 'the refused folder is named with its reason')
check((await folder.inputValue()) === bad, 'the refused path stays in the box to fix')

// A later page left empty steps back instead of saying there is nothing.
for (const path of ['/library?page=5', '/history?page=4']) {
  await page.goto(`${BASE}${path}`, { waitUntil: 'domcontentloaded' })
  check(await until(async () => new URL(page.url()).searchParams.get('page') === '1'), `${path.split('?')[0]} steps back to a page with something`, page.url())
}

// Notifications, faked: the page and the bell's panel confirm the same way.
let deleted = 0
const now = Math.floor(Date.now() / 1000)
await page.route('**/api/notifications*', (route) => {
  if (route.request().method() === 'DELETE') {
    deleted++
    return route.fulfill({ json: { deleted: 2 } })
  }
  return route.fulfill({
    json: {
      unread: 1,
      items: [
        { id: 1, kind: 'episode', animeId: 1, episode: 3, title: 'Show One', body: 'Episode 3 is out', createdAt: now, read: false },
        { id: 2, kind: 'episode', animeId: 2, episode: 1, title: 'Show Two', body: 'Episode 1 is out', createdAt: now, read: true },
      ],
    },
  })
})
await page.goto(`${BASE}/notifications`, { waitUntil: 'domcontentloaded' })
await page.getByRole('button', { name: 'Clear all', exact: true }).click()
check(await page.getByRole('button', { name: 'Mark all read' }).isVisible(), 'Mark all read stays while the clear is asked')
check(await page.getByRole('heading', { name: 'Notifications', level: 1 }).isVisible(), 'the page has the shared header')
await page.getByRole('button', { name: 'Delete all' }).click()
check(await until(async () => deleted === 1), 'Delete all deletes, once')

await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
await page.getByRole('button', { name: /notifications/i }).first().click()
await page.getByRole('button', { name: 'Clear', exact: true }).click()
check(await page.getByRole('button', { name: 'Clear all', exact: true }).isVisible(), 'the panel asks before clearing')
await page.keyboard.press('Escape')
check(await until(() => page.getByRole('button', { name: 'Clear', exact: true }).isVisible()), 'Escape backs out, the panel stays open')
check(deleted === 1, 'backing out deletes nothing')
await page.unroute('**/api/notifications*')

// On a phone the eight tabs are a menu; the strip hid the last ones off-screen.
const phone = await browser.newPage({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })
phone.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
await phone.goto(`${BASE}/settings`, { waitUntil: 'domcontentloaded' })
const menu = phone.getByRole('combobox', { name: 'Settings section' })
check(await menu.waitFor({ timeout: 8000 }).then(() => true, () => false), 'a phone gets a section menu')
check((await menu.locator('option').count()) === 8, 'every section is in it')
await menu.selectOption('About')
check(await until(async () => new URL(phone.url()).searchParams.get('tab') === 'About'), 'choosing a section switches to it', phone.url())
const width = await phone.evaluate(() => document.documentElement.scrollWidth)
check(width <= 390, 'nothing runs off the side', `${width}px`)

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)
