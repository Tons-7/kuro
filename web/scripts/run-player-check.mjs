// Runs player-check.mjs end to end against a throwaway kuro instance: builds
// the app, generates a test episode, starts an isolated server, drives the
// browser check, then tears it all down. One command, repeatable.
//
//   node scripts/run-player-check.mjs
//
// Env:
//   PORT=4399        which port the throwaway server listens on
//   KURO_ANIME=…     catalogue id the test file is assigned to (needs network
//                    the first time, to fetch that show's metadata)
//   SKIP_BUILD=1     reuse the current web/dist and a prebuilt kuro.exe
//   KEEP=1           leave the scratch dir and its logs in place afterwards
//
// Requires a network connection: the series page fetches real metadata for the
// catalogue id from AniList/MAL, exactly as it would in normal use.
import { spawn, spawnSync, execFileSync } from 'node:child_process'
import { mkdirSync, rmSync, writeFileSync, symlinkSync, existsSync, openSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const webDir = resolve(scriptDir, '..')
const repo = resolve(webDir, '..')

const PORT = process.env.PORT ?? '4399'
// Which browser check to drive against the instance.
const CHECK = process.argv[2] ?? process.env.CHECK ?? 'player-check.mjs'
const ANIME = process.env.KURO_ANIME ?? '127230'
const SKIP_BUILD = process.env.SKIP_BUILD === '1'
const KEEP = process.env.KEEP === '1'
const URL = `http://127.0.0.1:${PORT}`

const exe = (name) => (process.platform === 'win32' ? `${name}.exe` : name)
const ffmpeg = join(repo, 'bin', exe('ffmpeg'))
const scratch = join(tmpdir(), 'kuro-e2e')
const root = join(scratch, 'root')
const appdata = join(scratch, 'appdata')
const lib = join(scratch, 'lib')
const shots = join(scratch, 'shots')
const serverLog = join(scratch, 'server.log')
// The binary lives outside the scratch that is wiped each run, so SKIP_BUILD
// has something to reuse.
const binCache = join(tmpdir(), 'kuro-e2e-bin')
const kuroExe = join(binCache, exe('kuro'))

let server

const stage = (msg) => console.log(`\n▶ ${msg}`)
const run = (cmd, args, opts = {}) => {
  const r = spawnSync(cmd, args, { stdio: 'inherit', ...opts })
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(' ')} exited ${r.status ?? r.signal}`)
}

function killTree(child) {
  if (!child || child.exitCode !== null) return
  try {
    if (process.platform === 'win32') {
      execFileSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' })
    } else {
      process.kill(-child.pid, 'SIGKILL')
    }
  } catch {
    // Already gone.
  }
}

// A test episode: 180s (30 six-second segments) of colour bars, a tone, and an
// ASS track with a cue every few seconds so the subtitle checks have content.
function makeEpisode() {
  const cues = []
  for (let t = 2; t < 178; t += 5) {
    const a = new Date(t * 1000).toISOString().substr(11, 8)
    const b = new Date((t + 4) * 1000).toISOString().substr(11, 8)
    cues.push(`Dialogue: 0,${a}.00,${b}.00,Default,,0,0,0,,cue at ${t}s`)
  }
  const ass = `[Script Info]
ScriptType: v4.00+
PlayResX: 1280
PlayResY: 720

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
${cues.join('\n')}
`
  const assPath = join(scratch, 'subs.ass')
  writeFileSync(assPath, ass)
  // Named chapters, the way a real release marks its opening and ending.
  const chapters = join(scratch, 'chapters.ini')
  writeFileSync(
    chapters,
    ';FFMETADATA1\n' +
      [[0, 10000, 'Scene 1'], [10000, 40000, 'Intro'], [40000, 160000, 'Scene 3'], [160000, 180000, 'Credits']]
        .map(([s, e, t]) => `[CHAPTER]\nTIMEBASE=1/1000\nSTART=${s}\nEND=${e}\ntitle=${t}\n`)
        .join(''),
  )

  const out = join(lib, 'Kuro Test Show - 01.mkv')
  // Two audio tracks (a low tone for Japanese, a high one for English) so the
  // dub/sub track switch has something real to choose between.
  run(ffmpeg, [
    '-y', '-hide_banner', '-loglevel', 'error',
    '-f', 'lavfi', '-i', 'testsrc2=size=1280x720:rate=24:duration=180',
    '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000:duration=180',
    '-f', 'lavfi', '-i', 'sine=frequency=880:sample_rate=48000:duration=180',
    '-i', assPath,
    '-i', chapters,
    '-map_metadata', '4',
    '-map', '0:v', '-map', '1:a', '-map', '2:a', '-map', '3:s',
    '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-g', '48',
    '-c:a', 'aac', '-c:s', 'ass',
    '-metadata:s:a:0', 'language=jpn', '-disposition:a:0', 'default',
    '-metadata:s:a:1', 'language=eng',
    '-metadata:s:s:0', 'language=eng', '-disposition:s:0', 'default',
    out,
  ])
  return out
}

async function waitForServer() {
  for (let i = 0; i < 90; i++) {
    try {
      const res = await fetch(`${URL}/api/setup`)
      if (res.ok) return
    } catch {
      // Not up yet.
    }
    if (server.exitCode !== null) {
      throw new Error(`server exited early (code ${server.exitCode}); see ${serverLog}`)
    }
    await new Promise((r) => setTimeout(r, 1000))
  }
  throw new Error(`server did not answer on ${URL} in time; see ${serverLog}`)
}

try {
  if (!existsSync(ffmpeg)) throw new Error(`ffmpeg not found at ${ffmpeg}; build/download deps first`)

  stage(`scratch instance at ${scratch}`)
  rmSync(scratch, { recursive: true, force: true })
  for (const d of [scratch, root, appdata, lib, shots]) mkdirSync(d, { recursive: true })
  // The engine binaries (ffmpeg, ffprobe, rqbit, mpv, shaders) come from the
  // repo's bin via a junction, so the scratch root is otherwise isolated: its
  // own config, cache and database.
  symlinkSync(join(repo, 'bin'), join(root, 'bin'), process.platform === 'win32' ? 'junction' : 'dir')
  // One site that refuses connections: searches fail fast instead of being
  // refused for want of a site, and the first-run nudge stays off.
  writeFileSync(
    join(root, 'config.toml'),
    `addr = "127.0.0.1:${PORT}"\n\n[[indexer]]\ntype = "nyaa"\nurl = "http://127.0.0.1:1"\n`,
  )

  if (!SKIP_BUILD) {
    stage('building web (vite)')
    // A single string under shell:true so a .cmd shim runs on Windows without
    // the args-with-shell deprecation.
    run('npm run build', [], { cwd: webDir, shell: true })
    stage('building server (go)')
    mkdirSync(binCache, { recursive: true })
    run('go', ['build', '-o', kuroExe, './cmd/kuro'], { cwd: repo })
  }
  if (!existsSync(kuroExe)) throw new Error(`no kuro binary at ${kuroExe}; run once without SKIP_BUILD`)

  stage('generating test episode')
  makeEpisode()

  stage(`starting server on ${URL}`)
  const logFd = openSync(serverLog, 'w')
  server = spawn(kuroExe, [], {
    cwd: root,
    // No app window: Playwright is the browser, and the window's Chromium
    // profile outlives taskkill and blocks the scratch cleanup.
    env: { ...process.env, KURO_ROOT: root, LOCALAPPDATA: appdata, KURO_NO_WINDOW: '1' },
    stdio: ['ignore', logFd, logFd],
    detached: process.platform !== 'win32',
  })
  await waitForServer()
  console.log('  server is up')

  stage(`running ${CHECK}`)
  const check = spawnSync(process.execPath, [join(scriptDir, CHECK)], {
    cwd: webDir,
    stdio: 'inherit',
    env: {
      ...process.env,
      KURO_URL: URL, KURO_LIB: lib, KURO_ANIME: ANIME, SHOTS: shots,
      KURO_DB: join(appdata, 'kuro', 'kuro.db'), KURO_REPO: repo,
    },
  })
  process.exitCode = check.status ?? 1
} catch (err) {
  console.error(`\n✗ ${err.message}`)
  process.exitCode = 1
} finally {
  killTree(server)
  if (KEEP) {
    console.log(`\nscratch kept at ${scratch} (server log: ${serverLog})`)
  } else {
    // Windows keeps the killed server's files locked a moment; a failed
    // cleanup must not overwrite the check's verdict.
    try {
      rmSync(scratch, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 })
    } catch (err) {
      console.warn(`\ncould not remove ${scratch}: ${err.message}`)
    }
  }
}
