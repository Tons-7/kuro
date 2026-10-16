// Pre-release run: the Go suite, then each browser check on its own instance; logs in %TEMP%/kuro-release-check.
//   node scripts/release-check.mjs [only.mjs …]    env: LANES=4, SKIP_BUILD=1, SKIP_GO=1
import { spawn, spawnSync } from 'node:child_process'
import { createWriteStream, mkdirSync, rmSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const webDir = resolve(scriptDir, '..')
const repo = resolve(webDir, '..')
const logs = join(tmpdir(), 'kuro-release-check')

// ALONE checks run one at a time: AniList throttles parallel catalogue loads, or they need the foreground.
const ALONE = true

// [check, extra env, alone]. Missing ones are tools or need real sites (subs-lag-probe, sources-, autodelete-,
// access-approval-check); subs-sync-check never starts under Playwright, which draws hidden tabs.
const CHECKS = [
  ['player-check.mjs'],
  ['features-check.mjs'],
  ['recovery-check.mjs'],
  ['thumbs-check.mjs'],
  ['prefs-scope-check.mjs'],
  ['about-folders-check.mjs'],
  ['anime4k-tiers-check.mjs', {}, ALONE],
  ['backup-check.mjs'],
  ['browse-filters-check.mjs', {}, ALONE],
  ['browse-paging-check.mjs', {}, ALONE],
  ['change-release-check.mjs'],
  ['continue-next-check.mjs'],
  ['downloads-fresh-check.mjs'],
  ['downloads-groups-check.mjs'],
  ['episode-download-check.mjs'],
  ['fs-autonext-check.mjs'],
  ['fullscreen-check.mjs'],
  ['home-rails-check.mjs', {}, ALONE],
  ['hover-panel-check.mjs', {}, ALONE],
  ['list-state-check.mjs', {}, ALONE],
  ['lossless-audio-check.mjs'],
  ['no-transcoder-check.mjs', { BIN_ONLY: 'none' }],
  ['over-budget-note-check.mjs'],
  ['pip-check.mjs'],
  // Real sites would compete with the local swarm's.
  ['prefetch-check.mjs', { KURO_EXTRA_CONFIG: '' }, ALONE],
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
  ['ui-shared-check.mjs'],
  ['vlc-check.mjs'],
  ['sound-check.mjs'],
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
if (!only.length) step('format helpers', 'node scripts/format-check.mjs', webDir)
if (!only.length) step('release notes', 'node scripts/notes-check.mjs', webDir)

let built = process.env.SKIP_BUILD === '1'
if (!built) {
  const exe = join(tmpdir(), 'kuro-e2e-bin', process.platform === 'win32' ? 'kuro.exe' : 'kuro')
  built = step('web build', 'npm run build', webDir) && step('server build', `go build -o "${exe}" ./cmd/kuro`, repo)
}

// The updater end to end on two real builds; its own ports, so it never meets a lane.
if (built && !only.length) step('self-update', 'node scripts/update-check.mjs', webDir)

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
