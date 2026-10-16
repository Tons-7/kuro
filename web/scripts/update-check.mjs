// The self-updater on a real build: a fake GitHub's newer version is fetched, verified, swapped in and run.
//   KURO_REPO=D:\kuro node scripts/update-check.mjs
import { spawn, spawnSync } from 'node:child_process'
import { createHash, generateKeyPairSync, sign } from 'node:crypto'
import { createServer } from 'node:http'
import { mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { chromium } from 'playwright'

const REPO = process.env.KURO_REPO ?? join(process.cwd(), '..')
const PORT = 4398
const BASE = `http://127.0.0.1:${PORT}`
const OLD = '1.0.0'
const MID = '1.0.0.1'
const NEW = '1.0.1'
const NOTES = {
  [NEW]: '## The big one\n- **Keep all** keeps every episode\n- a second line',
  [MID]: '- a fix in between',
  [OLD]: '- what the old one brought',
}

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const api = async (path, init) => {
  const res = await fetch(BASE + path, { ...init, headers: { 'content-type': 'application/json' } })
  const text = await res.text()
  let body
  try {
    body = JSON.parse(text)
  } catch {
    body = text
  }
  return { ok: res.ok, status: res.status, body }
}
const post = (path) => api(path, { method: 'POST' })

// ---------------------------------------------------------------- build
const scratch = join(tmpdir(), `kuro-update-${process.pid}`)
rmSync(scratch, { recursive: true, force: true })
const root = join(scratch, 'app')
const data = join(scratch, 'data')
const release = join(scratch, 'release')
for (const d of [root, data, release]) mkdirSync(d, { recursive: true })

// The updater refuses an unsigned release, so this one is signed by a key only these test builds trust.
const { publicKey, privateKey } = generateKeyPairSync('ed25519')
const trusted = Buffer.from(publicKey.export({ format: 'jwk' }).x, 'base64url').toString('hex')
const flags = (version) => `-s -w -X kuro/internal/update.Version=${version} -X kuro/internal/update.signingKeyHex=${trusted}`
const build = (version, out) =>
  spawnSync('go', ['build', '-trimpath', '-ldflags', flags(version), '-o', out, './cmd/kuro'], {
    cwd: REPO,
    stdio: 'inherit',
    env: { ...process.env, CGO_ENABLED: '0' },
  })
check(build(OLD, join(root, 'kuro.exe')).status === 0, `built ${OLD}`)
check(build(NEW, join(release, 'kuro.exe')).status === 0, `built ${NEW}`)

const asset = `kuro-${NEW}-update.zip`
// Windows' bsdtar zips with -a, run in the directory so the archive holds a bare kuro.exe.
const zipped = spawnSync(join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'tar.exe'), ['-a', '-c', '-f', asset, 'kuro.exe'], {
  cwd: release,
  stdio: 'inherit',
})
check(zipped.status === 0 && existsSync(join(release, asset)), 'update zip packed')
const zip = readFileSync(join(release, asset))
const sum = createHash('sha256').update(zip).digest('hex')
const sums = `${sum}  ${asset}\n`
const signature = sign(null, Buffer.from(sums), privateKey)

// ---------------------------------------------------------------- fake github
const gh = createServer((req, res) => {
  const url = req.url ?? ''
  if (url.startsWith('/repos/Tons-7/kuro/releases/latest')) {
    res.setHeader('content-type', 'application/json')
    res.end(
      JSON.stringify({
        tag_name: `v${NEW}`,
        body: NOTES[NEW],
        assets: [
          { name: asset, browser_download_url: `${ghBase()}/dl/${asset}` },
          { name: 'SHA256SUMS.txt', browser_download_url: `${ghBase()}/dl/SHA256SUMS.txt` },
          { name: 'SHA256SUMS.txt.sig', browser_download_url: `${ghBase()}/dl/SHA256SUMS.txt.sig` },
        ],
      }),
    )
  } else if (url.startsWith('/repos/Tons-7/kuro/releases?')) {
    // Every release, so one several versions behind reads all of them; drafts and betas are nobody's.
    res.setHeader('content-type', 'application/json')
    res.end(
      JSON.stringify([
        { tag_name: 'v9.0.0', body: 'a draft', draft: true },
        { tag_name: 'v1.0.2', body: 'a beta', prerelease: true },
        ...[NEW, MID, OLD].map((v) => ({ tag_name: `v${v}`, body: NOTES[v], published_at: '2026-10-16T10:00:00Z' })),
      ]),
    )
  } else if (url === `/dl/${asset}`) {
    res.setHeader('content-length', zip.length)
    res.end(zip)
  } else if (url === '/dl/SHA256SUMS.txt') {
    res.end(sums)
  } else if (url === '/dl/SHA256SUMS.txt.sig') {
    res.end(signature)
  } else {
    res.statusCode = 404
    res.end()
  }
})
await new Promise((r) => gh.listen(0, '127.0.0.1', r))
const ghBase = () => `http://127.0.0.1:${gh.address().port}`

// ---------------------------------------------------------------- run old
// Its own peer port: the default is the real app's.
writeFileSync(join(root, 'config.toml'), `addr = "127.0.0.1:${PORT}"\n\n[torrent]\nlisten_port = 4343\nupnp = false\n`)
const env = {
  ...process.env,
  KURO_ROOT: root,
  // APPDATA too: the engine imports rqbit's per-user session when it has none.
  LOCALAPPDATA: data,
  APPDATA: data,
  KURO_UPDATE_API: ghBase(),
  KURO_NO_WINDOW: '1',
}
const old = spawn(join(root, 'kuro.exe'), [], { env, cwd: root })
let oldLog = ''
old.stdout.on('data', (d) => (oldLog += d))
old.stderr.on('data', (d) => (oldLog += d))
let oldExit = null
old.on('exit', (code) => (oldExit = code))

const waitVersion = async (want, seconds) => {
  for (let i = 0; i < seconds * 4; i++) {
    try {
      const h = await api('/api/health')
      if (h.ok && h.body.version === want) return true
    } catch {}
    await sleep(250)
  }
  return false
}

check(await waitVersion(OLD, 30), `old version ${OLD} answering`)

// The startup check runs as a job; give it a moment, then force one anyway.
await sleep(500)
const checked = await post('/api/update/check')
check(checked.ok && checked.body.available && checked.body.latest?.version === NEW, 'update seen', JSON.stringify(checked.body))
const versions = (list) => (list ?? []).map((n) => n.version).join(',')
check(versions(checked.body.pending) === `${NEW},${MID}`, 'every release since this one is offered, newest first', versions(checked.body.pending))
check(versions(checked.body.whatsNew) === OLD, "what's new before updating is this version's own", versions(checked.body.whatsNew))

const notes = await api('/api/notifications')
check(
  (notes.body.items ?? []).some((n) => n.kind === 'update' && n.title.includes(NEW)),
  'update notification raised',
  JSON.stringify((notes.body.items ?? []).map((n) => n.title)),
)
await post('/api/update/check')
const again = await api('/api/notifications')
check((again.body.items ?? []).filter((n) => n.kind === 'update').length === 1, 'not notified twice')

// ---------------------------------------------------------------- apply
const applied = await post('/api/update/apply')
check(applied.status === 202, 'apply accepted', JSON.stringify(applied.body))

check(await waitVersion(NEW, 60), `new version ${NEW} answering on the same port`)
for (let i = 0; i < 40 && oldExit === null; i++) await sleep(250)
check(oldExit === 0, 'old process exited cleanly', `exit=${oldExit}`)

check(readFileSync(join(root, 'kuro.exe')).equals(readFileSync(join(release, 'kuro.exe'))), 'kuro.exe is the new binary')
check(!existsSync(join(root, 'kuro.exe.old')), 'old binary cleaned up by the new process')
check(!existsSync(join(root, 'kuro.exe.new')), 'no staging file left')
check(!existsSync(join(root, 'cache', 'update', asset)), 'downloaded zip removed')

const status = await api('/api/update')
check(status.ok && status.body.current === NEW && !status.body.available, 'new process sees itself up to date', JSON.stringify(status.body))

// After the update: said once, and what's new covers every version since the old one.
let after = status.body
for (let i = 0; i < 40 && !after.whatsNew?.length; i++) {
  await sleep(250)
  after = (await api('/api/update')).body
}
check(after.updatedFrom === OLD, 'the new process knows what it replaced', after.updatedFrom)
check(versions(after.whatsNew) === `${NEW},${MID}`, "what's new covers both releases since the old one", versions(after.whatsNew))
const updated = ((await api('/api/notifications')).body.items ?? []).filter((n) => n.title === `Updated to kuro ${NEW}`)
check(updated.length === 1, 'an "Updated to" notification is raised once')

const browser = await chromium.launch()
const page = await browser.newPage()
await page.goto(`${BASE}/settings?tab=About`, { waitUntil: 'domcontentloaded' })
// This bare install has no ffmpeg, so the first load is sent to Setup once; then About within the app.
await page.waitForURL(/\/setup/, { timeout: 5000 }).catch(() => {})
await page.evaluate(() => {
  history.pushState(null, '', '/settings?tab=About')
  dispatchEvent(new PopStateEvent('popstate'))
})
const whatsNew = page.getByRole('heading', { name: `What's new since ${OLD}` })
const shown = await whatsNew.waitFor({ timeout: 15000 }).then(() => true, () => false)
check(shown, "the About tab says what's new since the old version")
// Skipped when absent, so the teardown below still runs.
if (shown) {
  const section = page.locator('section', { has: whatsNew })
  check((await section.locator('details').count()) === 2, 'one fold per release')
  check((await section.locator('details[open]').count()) === 1, 'the newest open, the older folded')
  check(await section.getByRole('heading', { name: 'The big one' }).isVisible(), 'a markdown heading is a heading')
  check((await section.locator('details[open] li').count()) === 2, 'bullets are a list, not raw dashes')
  check((await section.locator('details[open] strong', { hasText: 'Keep all' }).count()) === 1, 'bold is bold')
  check(!(await section.innerText()).includes('**'), 'no markdown left showing')
}
await browser.close()

if (!/restarting into the new version/.test(oldLog)) console.log(oldLog.slice(-2000))

// ---------------------------------------------------------------- teardown
// The new process broke away from ours; stop it by path so a real kuro survives.
spawnSync('powershell', [
  '-NoProfile', '-Command',
  `Get-Process kuro -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq '${join(root, 'kuro.exe')}' } | Stop-Process -Force`,
])
gh.close()
await sleep(500)
rmSync(scratch, { recursive: true, force: true })

console.log(failures ? `\n${failures} check(s) failed` : '\nall checks passed')
process.exit(failures ? 1 : 0)
