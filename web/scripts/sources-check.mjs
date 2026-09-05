// What a user sees around release sources: a fresh download, an existing
// install updating with its old config, and a config with sites filled in.
// Runs each against a throwaway instance, restarting between them.
//
//   KURO_NYAA=<url> KURO_TOKYOTOSHO=<url> node scripts/sources-check.mjs
//
// The sites are never in the repository; without them the last scenario is
// skipped. SKIP_BUILD=1 reuses the last build, KEEP=1 keeps the scratch dir.
import { spawn, spawnSync, execFileSync } from 'node:child_process'
import { mkdirSync, rmSync, writeFileSync, readFileSync, symlinkSync, existsSync, openSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { chromium } from 'playwright'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const webDir = resolve(scriptDir, '..')
const repo = resolve(webDir, '..')

// The default port: a fresh install has no config to say otherwise.
const PORT = process.env.PORT ?? '4321'
const URL = `http://127.0.0.1:${PORT}`
const ANIME = 185874 // Bleach TYBW: The Calamity, a later cour: the hard case
const EPISODE = 2
const NYAA = process.env.KURO_NYAA
const TOKYO = process.env.KURO_TOKYOTOSHO

const exe = (name) => (process.platform === 'win32' ? `${name}.exe` : name)
const scratch = join(tmpdir(), 'kuro-sources-e2e')
const root = join(scratch, 'root')
const appdata = join(scratch, 'appdata')
const shots = join(scratch, 'shots')
const binCache = join(tmpdir(), 'kuro-e2e-bin')
const kuroExe = join(binCache, exe('kuro'))
const configPath = join(root, 'config.toml')

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
    await sleep(250)
  }
  return false
}
const api = async (p) => {
  const res = await fetch(URL + p)
  let body
  try {
    body = await res.json()
  } catch {}
  return { ok: res.ok, status: res.status, body }
}

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

async function start(name) {
  const logFd = openSync(join(scratch, `${name}.log`), 'w')
  server = spawn(kuroExe, [], {
    cwd: root,
    env: { ...process.env, KURO_ROOT: root, LOCALAPPDATA: appdata, KURO_NO_WINDOW: '1' },
    stdio: ['ignore', logFd, logFd],
    detached: process.platform !== 'win32',
  })
  for (let i = 0; i < 90; i++) {
    try {
      if ((await fetch(`${URL}/api/setup`)).ok) return
    } catch {}
    if (server.exitCode !== null) throw new Error(`server exited early (${server.exitCode}); see ${name}.log`)
    await sleep(1000)
  }
  throw new Error('server did not come up')
}

async function stop() {
  killTree(server)
  await until(() => server.exitCode !== null, 15000)
  server = null
}

// Client-side navigation, the way links inside the app move.
const go = (page, path) =>
  page.evaluate((target) => {
    window.history.pushState({}, '', target)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, path)

// The unconfigured experience: nudged to setup, told what to add, and every
// search refused with the same instruction.
async function expectUnconfigured(page, label) {
  const setup = await api('/api/setup')
  check(setup.body?.indexers === 0, `${label}: setup reports no sites`, JSON.stringify(setup.body?.indexers))

  await page.goto(`${URL}/`, { waitUntil: 'domcontentloaded' })
  check(await until(() => page.url().endsWith('/setup'), 15000), `${label}: first visit lands on setup`, page.url())
  check(
    await page.getByText(/ships with no torrent sites/i).isVisible().catch(() => false),
    `${label}: setup explains sites are not shipped`,
  )
  check(
    await page.locator('pre', { hasText: '[[indexer]]' }).isVisible().catch(() => false),
    `${label}: setup shows the config block to add`,
  )
  await page.screenshot({ path: join(shots, `${label}-setup.png`) })

  // The series page, then play, by clicking through the app: a full page load
  // would only be nudged back to setup.
  await go(page, `/anime/${ANIME}`)
  await until(() => page.getByRole('heading', { name: /bleach/i }).first().isVisible().catch(() => false), 60000)
  await go(page, `/watch/${ANIME}/${EPISODE}`)
  const told = await until(
    () => page.getByText(/no release sources configured/i).first().isVisible().catch(() => false),
    60000,
  )
  check(told, `${label}: playing says to add sources and restart`)
  await page.getByRole('button', { name: 'Choose a release' }).click().catch(() => {})
  check(
    await until(
      () => page.getByRole('dialog').getByText(/no release sources configured/i).isVisible().catch(() => false),
      10000,
    ),
    `${label}: the release picker says the same`,
  )
  await page.screenshot({ path: join(shots, `${label}-watch.png`) })
}

try {
  stage(`scratch instance at ${scratch}`)
  rmSync(scratch, { recursive: true, force: true })
  for (const d of [scratch, root, appdata, shots]) mkdirSync(d, { recursive: true })
  symlinkSync(join(repo, 'bin'), join(root, 'bin'), process.platform === 'win32' ? 'junction' : 'dir')

  if (process.env.SKIP_BUILD !== '1') {
    stage('building')
    const r1 = spawnSync('npm run build', [], { cwd: webDir, shell: true, stdio: 'inherit' })
    if (r1.status !== 0) throw new Error('web build failed')
    mkdirSync(binCache, { recursive: true })
    const r2 = spawnSync('go', ['build', '-o', kuroExe, './cmd/kuro'], { cwd: repo, stdio: 'inherit' })
    if (r2.status !== 0) throw new Error('go build failed')
  }

  const browser = await chromium.launch()
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message.split('\n')[0].slice(0, 160)))

  // A: unzipped for the first time. No config at all.
  stage('fresh download: no config.toml')
  await start('fresh')
  check(existsSync(configPath), 'fresh: config.toml is written beside the binary')
  const written = readFileSync(configPath, 'utf8')
  check(written.includes('[[indexer]]') && written.includes('type = "nyaa"'), 'fresh: the written config shows the block to fill in')
  check(!/url\s*=\s*"https?:\/\/[^"]+"/.test(written), 'fresh: and names no site')
  await expectUnconfigured(page, 'fresh')
  await stop()

  // B: an existing install updating. Its config predates sites; its database
  // is the one from before.
  stage('existing install: old config, existing database')
  const old = `addr = "127.0.0.1:${PORT}"\n\n[anilist]\nclient_id = ""\nclient_secret = ""\n`
  writeFileSync(configPath, old)
  await start('update')
  check(readFileSync(configPath, 'utf8') === old, "update: the user's config is left untouched")
  await expectUnconfigured(page, 'update')
  await stop()

  // C: the block filled in, restarted.
  if (!NYAA || !TOKYO) {
    console.log('\nSKIP configured: set KURO_NYAA and KURO_TOKYOTOSHO to run it')
  } else {
    stage('sites configured, restarted')
    writeFileSync(
      configPath,
      old + `\n[[indexer]]\ntype = "nyaa"\nurl = "${NYAA}"\n\n[[indexer]]\ntype = "tokyotosho"\nurl = "${TOKYO}"\n`,
    )
    await start('configured')
    const setup = await api('/api/setup')
    check(setup.body?.indexers === 2, 'configured: setup counts both sites', JSON.stringify(setup.body?.indexers))

    await page.goto(`${URL}/`, { waitUntil: 'domcontentloaded' })
    await sleep(3000)
    check(!page.url().endsWith('/setup'), 'configured: no longer nudged to setup', page.url())

    const sources = await api(`/api/episode/sources?id=${ANIME}&episode=${EPISODE}`)
    const results = sources.body?.results ?? []
    const best = results[0]?.Torrent?.title ?? ''
    check(results.length > 0, 'configured: the search finds releases', `${results.length} results`)
    check(/kashin|calamity|42\b/i.test(best) && !/soukoku|conflict/i.test(best), 'configured: the best is this cour\'s episode', best)

    await page.goto(`${URL}/watch/${ANIME}/${EPISODE}`, { waitUntil: 'domcontentloaded' })
    const video = page.locator('video')
    const playing = await until(() => video.evaluate((v) => v.readyState >= 1).catch(() => false), 300000)
    check(playing, 'configured: the episode streams from a real release',
      await video.evaluate((v) => `ready=${v.readyState} t=${v.currentTime.toFixed(1)}`).catch(() => 'no video'))
    check(
      !(await page.getByText(/no release/i).first().isVisible().catch(() => false)),
      'configured: no failure shown',
    )
    await page.screenshot({ path: join(shots, 'configured-watch.png') })
    await stop()
  }

  check(errors.length === 0, 'no page errors', errors.join(' | '))
  await browser.close()
} catch (err) {
  console.error(`\n✗ ${err.message}`)
  failures++
} finally {
  killTree(server)
  console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
  if (process.env.KEEP === '1') {
    console.log(`scratch kept at ${scratch}`)
  } else {
    try {
      rmSync(scratch, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 })
    } catch (err) {
      console.warn(`could not remove ${scratch}: ${err.message}`)
    }
  }
  process.exit(failures === 0 ? 0 : 1)
}
