// End-to-end check of the self-updater against a real build: an old version
// runs in a scratch root, a local stand-in for GitHub offers a newer one, and
// the app must download it, verify it, swap kuro.exe, hand over to the new
// process and come back answering with the new version.
//
//   KURO_REPO=D:\kuro node scripts/update-check.mjs
import { spawn, spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { createServer } from 'node:http'
import { mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const REPO = process.env.KURO_REPO ?? join(process.cwd(), '..')
const PORT = 4398
const BASE = `http://127.0.0.1:${PORT}`
const OLD = '1.0.0'
const NEW = '1.0.1'

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

const build = (version, out) =>
  spawnSync('go', ['build', '-trimpath', '-ldflags', `-s -w -X kuro/internal/update.Version=${version}`, '-o', out, './cmd/kuro'], {
    cwd: REPO,
    stdio: 'inherit',
    env: { ...process.env, CGO_ENABLED: '0' },
  })
check(build(OLD, join(root, 'kuro.exe')).status === 0, `built ${OLD}`)
check(build(NEW, join(release, 'kuro.exe')).status === 0, `built ${NEW}`)

const asset = `kuro-${NEW}-update.zip`
// Windows' own bsdtar writes zip with -a; runs from the directory so the
// archive holds a bare kuro.exe like Compress-Archive produces.
const zipped = spawnSync(join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'tar.exe'), ['-a', '-c', '-f', asset, 'kuro.exe'], {
  cwd: release,
  stdio: 'inherit',
})
check(zipped.status === 0 && existsSync(join(release, asset)), 'update zip packed')
const zip = readFileSync(join(release, asset))
const sum = createHash('sha256').update(zip).digest('hex')

// ---------------------------------------------------------------- fake github
const gh = createServer((req, res) => {
  const url = req.url ?? ''
  if (url.startsWith('/repos/Tons-7/kuro/releases/latest')) {
    res.setHeader('content-type', 'application/json')
    res.end(
      JSON.stringify({
        tag_name: `v${NEW}`,
        body: 'test release',
        assets: [
          { name: asset, browser_download_url: `${ghBase()}/dl/${asset}` },
          { name: 'SHA256SUMS.txt', browser_download_url: `${ghBase()}/dl/SHA256SUMS.txt` },
        ],
      }),
    )
  } else if (url === `/dl/${asset}`) {
    res.setHeader('content-length', zip.length)
    res.end(zip)
  } else if (url === '/dl/SHA256SUMS.txt') {
    res.end(`${sum}  ${asset}\n`)
  } else {
    res.statusCode = 404
    res.end()
  }
})
await new Promise((r) => gh.listen(0, '127.0.0.1', r))
const ghBase = () => `http://127.0.0.1:${gh.address().port}`

// ---------------------------------------------------------------- run old
writeFileSync(join(root, 'config.toml'), `addr = "127.0.0.1:${PORT}"\n`)
const env = {
  ...process.env,
  KURO_ROOT: root,
  LOCALAPPDATA: data,
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
