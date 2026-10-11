// Everything to run before a release that needs no release sites: the Go suite,
// then each browser check against its own throwaway instance, a few at a time.
//
//   node scripts/release-check.mjs                  # all of it
//   node scripts/release-check.mjs pip-check.mjs    # only these checks, no Go suite
//
// Env: LANES=4 checks at once, SKIP_BUILD=1 reuses the last build, SKIP_GO=1.
// Logs land in %TEMP%/kuro-release-check. Not covered, run by hand with real
// sites: subs-lag-probe, sources-check, autodelete-check, access-approval-check.
// Nor subs-sync-check: Playwright's Chromium keeps drawing a hidden tab, so its scenario never starts.
import { spawn, spawnSync } from 'node:child_process'
import { createWriteStream, mkdirSync, rmSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const webDir = resolve(scriptDir, '..')
const repo = resolve(webDir, '..')
const logs = join(tmpdir(), 'kuro-release-check')

// ALONE checks run one at a time: they load live catalogue pages, which AniList
// throttles when several instances ask at once, or need the one foreground window.
const ALONE = true

// [check, extra env, alone]. A check missing here is a tool or needs real sites.
const CHECKS = [
  ['player-check.mjs'],
  ['features-check.mjs'],
  ['recovery-check.mjs'],
  ['thumbs-check.mjs'],
  ['prefs-scope-check.mjs'],
  ['about-folders-check.mjs'],
  ['anime4k-tiers-check.mjs', {}, ALONE],
  ['backup-check.mjs'],
  ['browse-paging-check.mjs', {}, ALONE],
  ['continue-next-check.mjs'],
  ['downloads-fresh-check.mjs'],
  ['downloads-groups-check.mjs'],
  ['fs-autonext-check.mjs'],
  ['fullscreen-check.mjs'],
  ['home-rails-check.mjs', {}, ALONE],
  ['hover-panel-check.mjs', {}, ALONE],
  ['list-state-check.mjs', {}, ALONE],
  ['lossless-audio-check.mjs'],
  ['no-transcoder-check.mjs', { BIN_ONLY: 'none' }],
  ['pip-check.mjs'],
  ['related-check.mjs', {}, ALONE],
  ['saved-copy-check.mjs', {}, ALONE],
  ['seek-spinner-check.mjs'],
  ['segmented-scroll-check.mjs'],
  ['setup-transcoder-check.mjs', { BIN_ONLY: 'ffmpeg' }],
  ['shortcuts-check.mjs'],
  ['show-groups-check.mjs'],
  ['starting-note-check.mjs'],
  ['studio-check.mjs', {}, ALONE],
  ['subs-recovery-check.mjs', {}, ALONE],
  ['subtitle-look-check.mjs'],
  ['tour-check.mjs', {}, ALONE],
  ['ui-bits-check.mjs'],
  ['vlc-check.mjs'],
]

const only = process.argv.slice(2)
const checks = only.length ? only.map((name) => CHECKS.find(([c]) => c === name) ?? [name]) : CHECKS
const LANES = Number(process.env.LANES ?? 4)
const failed = []

const step = (name, cmd, cwd) => {
  console.log(`\n▶ ${name}`)
  const ok = spawnSync(cmd, [], { cwd, shell: true, stdio: 'inherit' }).status === 0
  if (!ok) failed.push(name)
  return ok
}

function runCheck([name, env = {}], port) {
  return new Promise((done) => {
    const started = Date.now()
    const child = spawn(process.execPath, [join(scriptDir, 'run-player-check.mjs'), name], {
      cwd: webDir,
      env: { ...process.env, ...env, PORT: String(port), SKIP_BUILD: '1' },
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    const log = createWriteStream(join(logs, name.replace(/\.mjs$/, '.log')))
    child.stdout.pipe(log)
    child.stderr.pipe(log)
    child.on('close', (code) => {
      const secs = Math.round((Date.now() - started) / 1000)
      console.log(`  ${code === 0 ? '✓' : '✗'} ${name} (${secs}s)`)
      if (code !== 0) failed.push(name)
      done()
    })
  })
}

rmSync(logs, { recursive: true, force: true })
mkdirSync(logs, { recursive: true })

if (!only.length && process.env.SKIP_GO !== '1') step('go vet', 'go vet ./...', repo) && step('go test', 'go test ./...', repo)

let built = process.env.SKIP_BUILD === '1'
if (!built) {
  const exe = join(tmpdir(), 'kuro-e2e-bin', process.platform === 'win32' ? 'kuro.exe' : 'kuro')
  built = step('web build', 'npm run build', webDir) && step('server build', `go build -o "${exe}" ./cmd/kuro`, repo)
}

if (built) {
  console.log(`\n▶ ${checks.length} checks (logs: ${logs})`)
  const alone = checks.filter(([, , a]) => a)
  const rest = checks.filter(([, , a]) => !a)
  // Each lane keeps one port, so its scratch dir and peer port never overlap another's.
  const lane = async (queue, port) => {
    for (let next = queue.shift(); next; next = queue.shift()) await runCheck(next, port)
  }
  await Promise.all([lane(alone, 4400), ...Array.from({ length: LANES }, (_, i) => lane(rest, 4410 + 10 * i))])
}

console.log(failed.length ? `\n✗ failed: ${failed.join(', ')}` : '\n✓ all passed')
process.exitCode = failed.length ? 1 : 0
