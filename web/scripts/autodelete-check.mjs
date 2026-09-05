// Auto-delete "keep last 2" on a show the list is further into, going back to
// an early episode and then moving to the next: the episode just left must
// survive, whether it was left mid-way or played to the end.
//
//   KURO_NYAA=<url> KURO_TOKYOTOSHO=<url> node scripts/autodelete-check.mjs
//
// Streams two real episodes. SKIP_BUILD=1 reuses the last build.
import { spawn, spawnSync, execFileSync } from 'node:child_process'
import { mkdirSync, rmSync, writeFileSync, symlinkSync, existsSync, openSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { chromium } from 'playwright'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const webDir = resolve(scriptDir, '..')
const repo = resolve(webDir, '..')

const PORT = process.env.PORT ?? '4397'
const URL = `http://127.0.0.1:${PORT}`
const ANIME = 185874
const PROGRESS = 6
const NYAA = process.env.KURO_NYAA
const TOKYO = process.env.KURO_TOKYOTOSHO

const exe = (name) => (process.platform === 'win32' ? `${name}.exe` : name)
const scratch = join(tmpdir(), 'kuro-autodelete-e2e')
const root = join(scratch, 'root')
const appdata = join(scratch, 'appdata')
const binCache = join(tmpdir(), 'kuro-e2e-bin')
const kuroExe = join(binCache, exe('kuro'))
const serverLog = join(scratch, 'server.log')

let server
let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const stage = (msg) => console.log(`\n▶ ${msg}`)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 10000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(500)
  }
  return false
}
const api = async (p, init) => {
  const res = await fetch(URL + p, { ...init, headers: { 'content-type': 'application/json' } })
  let body
  try {
    body = await res.json()
  } catch {}
  return { ok: res.ok, body }
}
const post = (p, b) => api(p, { method: 'POST', body: JSON.stringify(b) })
const go = (page, path) =>
  page.evaluate((target) => {
    window.history.pushState({}, '', target)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, path)

// Which of the show's episodes the Downloads list still holds.
const held = async () => {
  const { body } = await api('/api/downloads')
  return (body?.items ?? []).filter((d) => d.animeId === ANIME).flatMap((d) => d.episodes)
}
const logLines = () =>
  readFileSync(serverLog, 'utf8')
    .split('\n')
    .filter((l) => /removed|evict|paused|abandoned|superseded|orphan|sweep|auto-delete/i.test(l))
    .map((l) => l.replace(/^time=\S+ /, ''))

function killTree(child) {
  if (!child || child.exitCode !== null) return
  try {
    if (process.platform === 'win32') {
      execFileSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' })
    } else {
      process.kill(-child.pid, 'SIGKILL')
    }
  } catch {}
}

try {
  if (!NYAA || !TOKYO) throw new Error('set KURO_NYAA and KURO_TOKYOTOSHO')
  stage(`scratch instance at ${scratch}`)
  rmSync(scratch, { recursive: true, force: true })
  for (const d of [scratch, root, appdata]) mkdirSync(d, { recursive: true })
  symlinkSync(join(repo, 'bin'), join(root, 'bin'), process.platform === 'win32' ? 'junction' : 'dir')
  writeFileSync(
    join(root, 'config.toml'),
    `addr = "127.0.0.1:${PORT}"\n\n[[indexer]]\ntype = "nyaa"\nurl = "${NYAA}"\n\n[[indexer]]\ntype = "tokyotosho"\nurl = "${TOKYO}"\n`,
  )

  if (process.env.SKIP_BUILD !== '1') {
    stage('building')
    const r1 = spawnSync('npm run build', [], { cwd: webDir, shell: true, stdio: 'inherit' })
    if (r1.status !== 0) throw new Error('web build failed')
    mkdirSync(binCache, { recursive: true })
    const r2 = spawnSync('go', ['build', '-o', kuroExe, './cmd/kuro'], { cwd: repo, stdio: 'inherit' })
    if (r2.status !== 0) throw new Error('go build failed')
  }

  stage('starting server')
  const logFd = openSync(serverLog, 'w')
  server = spawn(kuroExe, [], {
    cwd: root,
    env: { ...process.env, KURO_ROOT: root, LOCALAPPDATA: appdata, KURO_NO_WINDOW: '1' },
    stdio: ['ignore', logFd, logFd],
    detached: process.platform !== 'win32',
  })
  for (let i = 0; i < 90; i++) {
    try {
      if ((await fetch(`${URL}/api/setup`)).ok) break
    } catch {}
    if (server.exitCode !== null) throw new Error(`server exited early; see ${serverLog}`)
    await sleep(1000)
  }

  // The user's settings.
  for (const [key, value] of [
    ['cache.autodelete', 'keep2'],
    ['cache.autodelete_downloads', 'true'],
    ['playback.autonext', 'true'],
    ['playback.autoplay', 'true'],
  ]) {
    await post('/api/prefs', { key, value })
  }

  const browser = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] })
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  const video = page.locator('video')
  const streaming = () => video.evaluate((v) => v.readyState >= 1).catch(() => false)

  stage(`series page, list progress ${PROGRESS}`)
  await page.goto(`${URL}/`, { waitUntil: 'domcontentloaded' })
  await go(page, `/anime/${ANIME}`)
  await until(() => page.getByRole('heading', { name: /bleach/i }).first().isVisible().catch(() => false), 60000)
  const marked = await post('/api/watched', { animeId: ANIME, episode: PROGRESS })
  check(marked.ok, `progress set to ${PROGRESS}`)

  stage('play episode 1, leave it mid-way for episode 2')
  await go(page, `/watch/${ANIME}/1`)
  check(await until(streaming, 300000), 'episode 1 streams')
  await sleep(15000)
  check((await held()).includes('1'), 'episode 1 is in Downloads while playing', JSON.stringify(await held()))

  await go(page, `/watch/${ANIME}/2`)
  check(await until(streaming, 300000), 'episode 2 streams')
  await sleep(20000)
  let after = await held()
  check(after.includes('1'), 'episode 1 SURVIVES leaving it mid-way', JSON.stringify(after))

  if (process.env.ONLY_MIDWAY === '1') throw new Error('stopping after the mid-way phase')
  stage('back to episode 1, play it out, auto-next to 2')
  await go(page, `/watch/${ANIME}/1`)
  check(await until(streaming, 300000), 'episode 1 streams again')
  await video.evaluate((v) => {
    v.currentTime = Math.max(0, v.duration - 8)
    void v.play()
  })
  check(await until(() => page.url().endsWith(`/watch/${ANIME}/2`), 90000), 'auto-next moved to episode 2', page.url())
  await until(streaming, 300000)
  await sleep(20000)
  after = await held()
  check(after.includes('1'), 'episode 1 SURVIVES being played to the end', JSON.stringify(after))

  console.log('\nserver log, deletion-related lines:')
  for (const l of logLines()) console.log('  ' + l)
  await browser.close()
} catch (err) {
  console.error(`\n✗ ${err.message}`)
  failures++
} finally {
  killTree(server)
  console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
  if (process.env.KEEP !== '1') {
    try {
      rmSync(scratch, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 })
    } catch {}
  } else {
    console.log(`scratch kept at ${scratch}`)
  }
  process.exit(failures === 0 ? 0 : 1)
}
