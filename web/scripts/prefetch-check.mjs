// The prefetch pipeline end to end on a local swarm (scripts/e2e-swarm, the instance's only site): the
// series page readies an episode, play downloads the next ahead, Continue and Next resume it, once each.
import { execFileSync, spawn } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { createInterface } from 'node:readline'
import { join } from 'node:path'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const ANIME = Number(process.env.KURO_ANIME ?? 127230)
const LIB = process.env.KURO_LIB
const REPO = process.env.KURO_REPO
const SHOTS = process.env.SHOTS ?? '.'
// The same arithmetic as run-player-check.mjs: the engine's peer port, and the indexer port it wrote.
const PORT = Number(new URL(BASE).port)
const PEER_PORT = PORT === 4399 ? 4341 : PORT - 60
const SEED_PORT = PEER_PORT + 1000
const MOCK = `http://127.0.0.1:${PORT + 2000}`
const EPISODES = 4

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const until = async (fn, ms = 30_000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await Promise.resolve().then(fn).catch(() => false)) return true
    await sleep(250)
  }
  return false
}
const get = async (p) => (await fetch(BASE + p)).json()
const post = (p, b) =>
  fetch(BASE + p, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b) })
const searches = async () => (await (await fetch(`${MOCK}/e2e/searches`)).json()).searches
const downloads = async () => (await get('/api/downloads')).items ?? []
const listed = async (episode, hash) => (await downloads()).some((d) => d.episode === String(episode) && d.infoHash === hash)
// From the instance's log: the site cannot tell a search from one the indexer cache answered.
const serverLog = join(LIB, '..', 'server.log')
const logged = (msg, episode) => {
  const re = new RegExp(`msg="${msg}".*episode=${episode}\\b`, 'g')
  return (readFileSync(serverLog, 'utf8').match(re) ?? []).length
}
const resolved = (episode) => logged('next episode resolved', episode)
const fetched = (episode) => logged('fetched episode', episode)
const resumed = (episode) => logged('resumed the release already held', episode)
const waitedFor = (episode) => logged('waiting for the release already being made ready', episode)
const searchedAgain = () => (readFileSync(serverLog, 'utf8').match(/did not deliver|not delivering|searching again/g) ?? []).length

await until(async () => (await fetch(`${BASE}/api/setup`)).ok, 60_000)
const title = (await get(`/api/anime/${ANIME}`)).title
check(!!title, 'the show is known to the instance', title)

// The swarm: one torrent per episode, plus the site that lists them.
const swarm = spawn('go', ['run', './scripts/e2e-swarm',
  '-src', join(LIB, 'Kuro Test Show - 01.mkv'), '-dir', join(LIB, '..', 'swarm'), '-title', title,
  '-count', String(EPISODES), '-listen', String(SEED_PORT), '-peer', `127.0.0.1:${PEER_PORT}`,
  '-http', `127.0.0.1:${PORT + 2000}`,
], { cwd: REPO, stdio: ['ignore', 'pipe', 'inherit'] })
const killSwarm = () => {
  try {
    if (process.platform === 'win32') execFileSync('taskkill', ['/PID', String(swarm.pid), '/T', '/F'], { stdio: 'ignore' })
    else process.kill(-swarm.pid, 'SIGKILL')
  } catch {}
}
process.on('exit', killSwarm)
const hashes = await new Promise((resolve) => {
  const lines = createInterface({ input: swarm.stdout })
  const timer = setTimeout(() => resolve(null), 180_000)
  lines.on('line', (line) => {
    if (!line.startsWith('{')) return
    const msg = JSON.parse(line)
    if (msg.ready) {
      clearTimeout(timer)
      resolve(Object.fromEntries(msg.torrents.map((t) => [t.episode, t.infoHash])))
    }
  })
})
check(!!hashes && Object.keys(hashes).length === EPISODES, 'the swarm seeds one torrent per episode')
if (!hashes) {
  killSwarm()
  process.exit(1)
}

for (const [key, value] of [['cache.prefetch_next', 'true'], ['cache.prefetch_count', '1'], ['playback.autoplay', 'true']]) {
  await post('/api/prefs', { key, value })
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)))
const prepared = []
page.on('request', (r) => {
  if (r.method() === 'POST' && r.url().endsWith('/api/prepare')) prepared.push(JSON.parse(r.postData() ?? '{}').episode)
})
const playing = () => page.evaluate(() => {
  const v = document.querySelector('video')
  return !!v && v.currentTime > 1 && !v.paused
})
const played = () => page.waitForResponse((r) => r.url().endsWith('/api/play') && r.request().method() === 'POST', { timeout: 120_000 })

// 1. The series page readies the first episode: one search, nothing in the engine.
await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
const watch = page.getByRole('link', { name: /^Watch episode 1/ })
check(await until(() => watch.isVisible(), 60_000), 'the series page offers episode 1')
check(await until(() => prepared.includes(1)), 'and asks for it to be readied')
check(await until(async () => (await searches()) > 0, 60_000), 'which searches the site')
check(await until(() => resolved(1) === 1, 30_000) && (await downloads()).length === 0, 'and resolves it without downloading anything')

// 2. Playing it uses the release readied, then resolves and downloads episode 2 ahead, once.
let session = played()
await watch.click()
let s = await (await session).json()
check(s.infoHash === hashes[1], 'episode 1 plays the release readied ahead', s.infoHash ?? s.error)
// Waiting shows in the log only when play arrives mid-resolve; either way it searches no second time.
check(resolved(1) === 1 && searchedAgain() === 0, 'without a second search of its own', `waited ${waitedFor(1)}`)
check(await until(playing, 90_000), 'and the video plays')
check(await until(() => listed(2, hashes[2]), 90_000), 'episode 2 is downloaded ahead while it plays')
check(resolved(2) === 1 && fetched(2) === 1 && waitedFor(2) === 1, 'resolved once and fetched from that resolve', `resolved ${resolved(2)} fetched ${fetched(2)}`)
check((await downloads()).length === 2, 'one download per episode', String((await downloads()).length))
await page.screenshot({ path: `${SHOTS}/prefetch-playing.png` })

// 3. Episode 1 watched (player closed, then its last reports), back on the series page: Continue names
// the episode downloaded ahead, readying it searches nothing, and opening it resumes that torrent.
await page.goto('about:blank')
await sleep(1500)
for (let i = 0; i < 2; i++) await post('/api/progress', { animeId: ANIME, episode: 1, position: 179, duration: 180, played: 100 })
await page.goto(`${BASE}/anime/${ANIME}`, { waitUntil: 'domcontentloaded' })
const cont = page.getByRole('link', { name: /^Continue episode 2/ })
const offered = () => page.getByRole('link', { name: /episode \d/ }).first().textContent().catch(() => '?')
check(await until(() => cont.isVisible(), 60_000), 'the series page offers Continue with episode 2', await offered())
check(await until(() => prepared.includes(2)), 'and asks for it to be readied')
await sleep(3000)
check(resolved(2) === 1, 'readying an episode already held resolves nothing again', String(resolved(2)))
session = played()
await cont.click()
s = await (await session).json()
check(s.infoHash === hashes[2], 'Continue resumes the torrent downloaded ahead', s.infoHash ?? s.error)
check(resumed(2) === 1, 'by resuming it, not fetching it again')
check(await until(playing, 90_000), 'and it plays')
check(await until(() => listed(3, hashes[3]), 90_000), 'episode 3 is downloaded ahead in turn')
check(resolved(3) === 1 && fetched(3) === 1, 'resolved once and fetched once', `resolved ${resolved(3)} fetched ${fetched(3)}`)
check((await downloads()).length === 3, 'still one download per episode', String((await downloads()).length))

// 4. Next in the player: the same, by the other route.
await page.mouse.move(640, 400)
// The player's control; the up-next card offers one too.
const next = page.getByRole('button', { name: 'Next episode' }).first()
check(await until(() => next.isVisible()), 'the player offers the next episode')
session = played()
await next.click()
s = await (await session).json()
check(s.infoHash === hashes[3], 'Next resumes the torrent downloaded ahead', s.infoHash ?? s.error)
check(resumed(3) === 1, 'by resuming it')
check(await until(playing, 90_000), 'and it plays')
check(await until(() => listed(4, hashes[4]), 90_000), 'episode 4 is downloaded ahead')
check(resolved(4) === 1 && fetched(4) === 1, 'resolved once and fetched once', `resolved ${resolved(4)} fetched ${fetched(4)}`)
await sleep(3000)
const final = await downloads()
check(final.length === EPISODES && new Set(final.map((d) => d.infoHash)).size === EPISODES, 'every episode has exactly one download', final.map((d) => `${d.episode}:${d.infoHash.slice(0, 6)}`).join(' '))
check(searchedAgain() === 0 && fetched(1) === 0, 'nothing was searched for again or fetched beside what was held', String(searchedAgain()))
await page.screenshot({ path: `${SHOTS}/prefetch-next.png` })

check(errors.length === 0, 'no page errors', errors.join(' | '))
await browser.close()
killSwarm()
console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
