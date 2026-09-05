// A track can only be read as far as the download reached. This watches one
// grow: start an episode nothing has been fetched for, then sample the cue
// count as pieces arrive.
const BASE = 'http://127.0.0.1:4321'
const [anime, episode] = (process.argv[2] ?? '21/1100').split('/')
const MINUTES = Number(process.env.FILL_MINUTES ?? 8)

const api = async (path, init) => {
  const res = await fetch(BASE + path, { ...init, signal: AbortSignal.timeout(300000) })
  const text = await res.text()
  try {
    return { ok: res.ok, body: JSON.parse(text) }
  } catch {
    return { ok: res.ok, body: text.slice(0, 200) }
  }
}

const play = await api('/api/play', {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ animeId: Number(anime), episode: Number(episode) }),
})
if (!play.ok) {
  console.log('no release:', play.body.error)
  process.exit(1)
}
console.log('release:', (play.body.title ?? '').slice(0, 70))

const stream = await api(
  `/api/stream/open?id=${anime}&episode=${episode}&source=${encodeURIComponent(play.body.streamUrl)}`,
  { method: 'POST' },
)
if (!stream.ok) {
  console.log('stream failed:', JSON.stringify(stream.body).slice(0, 120))
  process.exit(1)
}

const tracks = stream.body.subtitles ?? []
const track = tracks.find((t) => /^(en|eng|english)$/i.test(t.language ?? '')) ?? tracks[0]
if (!track) {
  console.log('no subtitle tracks')
  process.exit(1)
}
console.log(`track: ${track.language} "${track.title ?? ''}"  duration ${Math.round(stream.body.duration)}s`)
console.log('')
console.log('   at    cues   covers   downloaded')

const percentOf = async () => {
  const got = await api('/api/downloads')
  const row = (got.body.items ?? []).find((x) => String(x.episode) === String(episode))
  return row ? Math.round(row.percent) : 0
}

let last = -1
for (let i = 0; i <= MINUTES * 2; i++) {
  const res = await fetch(`${BASE}${track.url}?fill=${i}`, { signal: AbortSignal.timeout(300000) })
  const text = res.ok ? await res.text() : ''
  const cues = (text.match(/^Dialogue:/gm) ?? []).length

  // How far into the episode the last cue sits — what a viewer can actually
  // watch with subtitles.
  let covers = 0
  for (const line of text.split('\n')) {
    if (!line.startsWith('Dialogue:')) continue
    const [h, m, s] = (line.split(',')[2] ?? '').split(':')
    const t = Number(h) * 3600 + Number(m) * 60 + Number(s)
    if (Number.isFinite(t) && t > covers) covers = t
  }

  const percent = await percentOf()
  const grew = cues > last ? '  +' : '   '
  console.log(
    `${String(i * 30).padStart(5)}s ${String(cues).padStart(7)}${grew}` +
      `${String(Math.round(covers)).padStart(6)}s ${String(percent).padStart(10)}%`,
  )

  // Not "the last cue reaches the end": the engine fetches the head and tail
  // first, so a handful of cues can span the episode while the middle is a
  // hole. Only the download being finished means the track is whole.
  last = cues
  if (percent >= 100) {
    console.log(`\ncomplete: ${cues} cues, download finished`)
    break
  }
  await new Promise((r) => setTimeout(r, 30000))
}
