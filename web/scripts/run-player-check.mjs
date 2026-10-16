// Builds, then runs a browser check (player-check.mjs by default) on a throwaway kuro, and tears it down.
// Env: PORT=4399 (own scratch), KURO_ANIME=id, SKIP_BUILD=1, KEEP=1, BIN_ONLY=a,b|none, ANILIST_LIVE=1.
import { spawn, spawnSync, execFileSync } from 'node:child_process'
import { mkdirSync, rmSync, writeFileSync, existsSync, openSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { delimiter, dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { linkBin } from './harness-lib.mjs'

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
const DEFAULT_PORT = PORT === '4399'
const scratch = join(tmpdir(), DEFAULT_PORT ? 'kuro-e2e' : `kuro-e2e-${PORT}`)
// Off the real app's 4240 and the other scripts' 4341–4347.
const PEER_PORT = DEFAULT_PORT ? 4341 : Number(PORT) - 60
const root = join(scratch, 'root')
const appdata = join(scratch, 'appdata')
const isolated = {
  KURO_ROOT: root,
  // APPDATA too: the engine imports rqbit's per-user session when it has none.
  LOCALAPPDATA: appdata,
  APPDATA: appdata,
  KURO_NO_WINDOW: '1',
}
const lib = join(scratch, 'lib')
const shots = join(scratch, 'shots')
const serverLog = join(scratch, 'server.log')
// The binary lives outside the scratch wiped each run, so SKIP_BUILD has something to reuse.
// KURO_BIN_DIR: a build of its own, while another run still has the shared binary open.
const binCache = process.env.KURO_BIN_DIR ?? join(tmpdir(), 'kuro-e2e-bin')
const kuroExe = join(binCache, exe('kuro'))

let server
// AniList answers come through a caching proxy (see anilist-cache.mjs) unless ANILIST_LIVE=1.
let anilist
const ANILIST_PORT = Number(PORT) + 1000

const stage = (msg) => console.log(`\n▶ ${msg}`)
const run = (cmd, args, opts = {}) => {
  const r = spawnSync(cmd, args, { stdio: 'inherit', ...opts })
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(' ')} exited ${r.status ?? r.signal}`)
}

// kuro also finds a program on PATH, so BIN_ONLY hides any copy installed there.
function binOnlyPath() {
  if (!process.env.BIN_ONLY) return {}
  const keep = process.env.BIN_ONLY.split(',')
  const hidden = ['ffmpeg', 'ffprobe', 'mpv', 'vlc'].filter((n) => !keep.includes(n))
  const key = Object.keys(process.env).find((k) => k.toLowerCase() === 'path') ?? 'PATH'
  const dirs = (process.env[key] ?? '').split(delimiter)
  return { [key]: dirs.filter((d) => !hidden.some((n) => existsSync(join(d, exe(n))))).join(delimiter) }
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
  // Two audio tracks (low tone Japanese, high English) for the sub/dub switch to choose between.
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
  makeMarkerEpisode()
  return out
}

// Episode 3's subtitles: cue i is a block at x = 40 + 12i from 2i to 2i+1.9s, readable against the clock.
function makeMarkerEpisode() {
  const cues = []
  for (let i = 0; i < 90; i++) {
    const at = (s) => new Date(s * 1000).toISOString().substr(11, 11).replace(/0$/, '')
    cues.push(
      `Dialogue: 0,${at(2 * i)},${at(2 * i + 1.9)},Default,,0,0,0,,{\\an7\\pos(${40 + 12 * i},300)\\p1}m 0 0 l 10 0 l 10 40 l 0 40{\\p0}`,
    )
  }
  const assPath = join(scratch, 'markers.ass')
  writeFileSync(
    assPath,
    `[Script Info]
ScriptType: v4.00+
PlayResX: 1280
PlayResY: 720

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,0,0,7,0,0,0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
${cues.join('\n')}
`,
  )
  run(ffmpeg, [
    '-y', '-hide_banner', '-loglevel', 'error',
    // Not a flat colour: the scanner ignores files under 20 MB as not episodes.
    '-f', 'lavfi', '-i', 'testsrc2=size=1280x720:rate=24:duration=180',
    '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000:duration=180',
    '-i', assPath,
    '-map', '0:v', '-map', '1:a', '-map', '2:s',
    '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-g', '48',
    '-c:a', 'aac', '-c:s', 'ass',
    '-metadata:s:s:0', 'language=eng', '-disposition:s:0', 'default',
    join(lib, 'Kuro Test Show - 03.mkv'),
  ])
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
  // BIN_ONLY=a,b links only those (someone who skipped the rest); none links nothing.
  linkBin(join(repo, 'bin'), join(root, 'bin'), process.env.BIN_ONLY ? process.env.BIN_ONLY.split(',') : null)
  // A port nothing listens on, so searches fail fast and the first-run nudge stays off; e2e-swarm answers
  // there when a check starts it. The peer port is off the app's own.
  writeFileSync(
    join(root, 'config.toml'),
    `addr = "127.0.0.1:${PORT}"\n${process.env.KURO_EXTRA_CONFIG ?? ''}\n[torrent]\nlisten_port = ${PEER_PORT}\nupnp = false\n\n[[indexer]]\ntype = "nyaa"\nurl = "http://127.0.0.1:${Number(PORT) + 2000}"\n`,
  )

  if (!SKIP_BUILD) {
    stage('building web (vite)')
    // One string under shell:true, so a .cmd shim runs without the args-with-shell warning.
    run('npm run build', [], { cwd: webDir, shell: true })
    stage('building server (go)')
    mkdirSync(binCache, { recursive: true })
    run('go', ['build', '-o', kuroExe, './cmd/kuro'], { cwd: repo })
  }
  if (!existsSync(kuroExe)) throw new Error(`no kuro binary at ${kuroExe}; run once without SKIP_BUILD`)

  stage('generating test episode')
  makeEpisode()

  const upstream = {}
  if (process.env.ANILIST_LIVE !== '1') {
    anilist = spawn(process.execPath, [join(scriptDir, 'anilist-cache.mjs'), String(ANILIST_PORT)], { stdio: 'ignore' })
    upstream.KURO_ANILIST_API = `http://127.0.0.1:${ANILIST_PORT}`
  }

  stage(`starting server on ${URL}`)
  const logFd = openSync(serverLog, 'w')
  server = spawn(kuroExe, [], {
    cwd: root,
    // No app window: Playwright is the browser, and the window's profile blocks cleanup.
    env: { ...process.env, ...isolated, ...upstream, ...binOnlyPath() },
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
  killTree(anilist)
  if (KEEP) {
    console.log(`\nscratch kept at ${scratch} (server log: ${serverLog})`)
  } else {
    // Windows holds the killed server's files a moment; a failed cleanup can't change the verdict.
    try {
      rmSync(scratch, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 })
    } catch (err) {
      console.warn(`\ncould not remove ${scratch}: ${err.message}`)
    }
  }
}
