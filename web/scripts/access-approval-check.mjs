// Restricted access and HTTPS, end to end: a "phone" reaches kuro by the
// certificate's name, waits, and gets in once the host accepts it on its own page.
// Needs KURO_EXTRA_CONFIG with tls_cert/tls_key for a certificate naming phone.test.
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const PORT = new URL(BASE).port
const PHONE = `https://phone.test:${PORT}`
const SHOTS = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const host = (path, method = 'GET', body) =>
  fetch(`${BASE}${path}`, {
    method,
    headers: body ? { 'content-type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  }).then((r) => r.json())

const access = await host('/api/access')
check(access.urls[0] === `${PHONE}/?token=${access.token}`, 'the pairing link uses https and the certificate name', access.urls[0])
check((await host('/api/access/approval', 'POST', { mode: 'once' })).mode === 'once', 'approval switched on')

// phone.test is this machine, but not by a loopback name: kuro treats it as another device.
const browser = await chromium.launch({ args: ['--host-resolver-rules=MAP phone.test 127.0.0.1'] })
const hostPage = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const phone = await browser.newContext({
  viewport: { width: 420, height: 800 }, isMobile: true, hasTouch: true, ignoreHTTPSErrors: true,
  userAgent: 'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36',
})
const phonePage = await phone.newPage()
const errors = []
hostPage.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))

await hostPage.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })

// A second tab on the phone makes the bare requests; only the browser resolves phone.test.
const probe = await phone.newPage()
const opened0 = await probe.goto(`${PHONE}/manifest.webmanifest`)
check(opened0.ok(), 'the manifest is public over https', String(opened0.status()))
const phoneFetch = (path, init) =>
  probe.evaluate(async ([p, i]) => {
    const r = await fetch(p, i)
    return { status: r.status, type: r.headers.get('content-type') ?? '' }
  }, [path, init])

// The installer fetches these with no cookie and no token.
for (const path of ['/icon-192.png', '/icon-512.png', '/icon-maskable-512.png']) {
  const res = await phoneFetch(path, { credentials: 'omit' })
  check(res.status === 200 && res.type.includes('image/png'), `${path} is public`, `${res.status} ${res.type}`)
}
const manifest = await probe.evaluate(() => fetch('/manifest.webmanifest', { credentials: 'omit' }).then((r) => r.json()))
check(manifest.display === 'standalone' && manifest.icons.some((i) => i.sizes === '512x512' && i.purpose === 'maskable'), 'the manifest is installable (standalone, 192/512 PNGs, maskable)')

const refused = await phoneFetch('/api/health')
check(refused.status === 401, 'without the link the phone is refused', String(refused.status))

await phonePage.goto(`${PHONE}/?token=${access.token}`, { waitUntil: 'domcontentloaded' })
check(await phonePage.getByText('Waiting for the host').isVisible(), 'with the link the phone sees the waiting page')
await phonePage.screenshot({ path: `${SHOTS}/approval-waiting.png` })
check((await phoneFetch('/api/health')).status === 403, 'the app stays closed to it while it waits')

const prompt = hostPage.getByText('A device wants to use kuro')
check(await prompt.waitFor({ timeout: 15000 }).then(() => true, () => false), 'the host is asked on whatever page it is on')
check(await hostPage.getByText('Chrome on Android').isVisible(), 'the request names the device')
await hostPage.screenshot({ path: `${SHOTS}/approval-prompt.png` })
await hostPage.getByRole('button', { name: 'Accept' }).click()

// The waiting page polls and reloads into the app on its own.
const opened = await phonePage.locator('#root').waitFor({ state: 'attached', timeout: 15000 }).then(() => true, () => false)
check(opened, 'the phone opens kuro by itself once accepted')
check((await phoneFetch('/api/health')).status === 200, 'and the app answers it')
check(await prompt.waitFor({ state: 'hidden', timeout: 10000 }).then(() => true, () => false), 'the prompt goes away')
check(await phonePage.evaluate(() => window.isSecureContext), 'the phone is on a secure context, which install needs')

const { devices } = await host('/api/access/devices')
check(devices.length === 1 && devices[0].status === 'approved', 'the device is listed as accepted', JSON.stringify(devices.map((d) => d.status)))
const selfApprove = await phoneFetch(`/api/access/devices/${devices[0].id}`, {
  method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ status: 'approved' }),
})
check(selfApprove.status === 403, 'a device cannot decide requests itself', String(selfApprove.status))

await host(`/api/access/devices/${devices[0].id}`, 'DELETE')
check((await phoneFetch('/api/health')).status === 403, 'removing the device shuts it out again')

await host('/api/access/approval', 'POST', { mode: 'off' })
check((await phoneFetch('/api/health')).status === 200, 'with approval off the link alone is enough')

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
console.log(failures ? `${failures} failed` : 'all passed')
process.exit(failures ? 1 : 0)
