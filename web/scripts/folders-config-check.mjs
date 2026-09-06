// A running kuro honours data_dir, cache_dir and bin_dir from config.toml.
//   KURO_EXE=...\kuro.exe node scripts/folders-config-check.mjs
import { spawn, spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..')
const exe = process.env.KURO_EXE
const PORT = process.env.PORT ?? 4398
const BASE = `http://127.0.0.1:${PORT}`
if (!exe || !existsSync(exe)) throw new Error('set KURO_EXE to a built kuro.exe')

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const scratch = join(tmpdir(), 'kuro-folders-check')
rmSync(scratch, { recursive: true, force: true })
const root = join(scratch, 'root')
const tools = join(scratch, 'tools')
mkdirSync(root, { recursive: true })
mkdirSync(join(scratch, 'appdata'), { recursive: true })
spawnSyncOrThrow('cmd', ['/c', 'mklink', '/J', tools, join(repo, 'bin')])
writeFileSync(
  join(root, 'config.toml'),
  `addr = "127.0.0.1:${PORT}"\ndata_dir = "data"\ncache_dir = "store"\nbin_dir = '${tools}'\n\n[torrent]\napi_addr = "127.0.0.1:3032"\n\n[[indexer]]\ntype = "nyaa"\nurl = "http://127.0.0.1:1"\n`,
)

const kuro = spawn(exe, ['--no-window'], {
  env: { ...process.env, KURO_ROOT: root, LOCALAPPDATA: join(scratch, 'appdata'), KURO_NO_WINDOW: '1' },
  stdio: 'ignore',
})
try {
  let setup
  for (let i = 0; i < 40 && !setup; i++) {
    await sleep(500)
    setup = await fetch(`${BASE}/api/setup`).then((r) => r.json()).catch(() => null)
  }
  check(!!setup, 'kuro started with the custom folders')
  check(setup?.dataDir === join(root, 'data'), 'data_dir is beside the exe', setup?.dataDir)
  check(setup?.cacheDir === join(root, 'store'), 'cache_dir is beside the exe', setup?.cacheDir)
  check(setup?.binDir === tools, 'bin_dir is the absolute folder as written', setup?.binDir)
  check(setup?.ready === true, 'the engines were found in bin_dir')
  check(existsSync(join(root, 'data', 'kuro.db')), 'the database lives in data_dir')
  check(existsSync(join(root, 'store')), 'the cache folder was created')
  check(!existsSync(join(root, 'cache')) && !existsSync(join(scratch, 'appdata', 'kuro', 'kuro.db')), 'nothing went to the default folders')
} finally {
  kuro.kill()
}
await sleep(1000)
console.log(failures === 0 ? '\nall checks passed' : `\n${failures} check(s) failed`)
process.exit(failures === 0 ? 0 : 1)

function spawnSyncOrThrow(cmd, args) {
  const r = spawnSync(cmd, args, { stdio: 'ignore' })
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(' ')} failed`)
}
