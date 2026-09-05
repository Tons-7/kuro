// For each show: resolve a release, open a stream, and report the subtitle
// track the player would use — and whether that file actually contains cues.
// No browser needed; this is the whole path a viewer depends on.
const BASE = 'http://127.0.0.1:4321'
const PER_SHOW_MS = Number(process.env.SUBS_TIMEOUT ?? 240000)
const REFILLS = Number(process.env.SUBS_REFILLS ?? 4)
const REFILL_EVERY = Number(process.env.SUBS_REFILL_EVERY ?? 20000)

// A spread of eras, sources and languages rather than only what is airing now.
const SHOWS = process.env.SUBS_SHOWS
  ? JSON.parse(process.env.SUBS_SHOWS)
  : [
      [185874, 44, 'Bleach TYBW c4'],
      [235, 1200, 'Detective Conan'],
      [154587, 1, 'Frieren'],
      [113415, 1, 'Jujutsu Kaisen'],
      [16498, 1, 'Attack on Titan'],
      [1535, 1, 'Death Note'],
      [208685, 1, 'Someya-san (adult)'],
    ]

async function api(path, init = {}) {
  const stop = AbortSignal.timeout(PER_SHOW_MS)
  const res = await fetch(BASE + path, { ...init, signal: stop })
  const text = await res.text()
  try {
    return { ok: res.ok, body: JSON.parse(text) }
  } catch {
    return { ok: res.ok, body: text.slice(0, 200) }
  }
}

const ENGLISH = /^(en|eng|english)$/i
const SIGNS = /\b(signs?|songs?|s&s|forced)\b/i

// Mirrors the player's own rule, so this reports what a viewer would see.
function chooseTrack(tracks) {
  const dialogue = (t) => !SIGNS.test(t.title ?? '')
  const english = (t) => ENGLISH.test(t.language ?? '')
  return (
    tracks.find((t) => english(t) && dialogue(t) && t.default) ??
    tracks.find((t) => english(t) && dialogue(t)) ??
    tracks.find((t) => english(t)) ??
    tracks.find((t) => dialogue(t) && t.default) ??
    tracks.find((t) => dialogue(t)) ??
    tracks.find((t) => t.default) ??
    tracks[0]
  )
}

const rows = []
console.log(
  'show'.padEnd(22) + 'tracks'.padStart(7) + '  picked'.padEnd(26) + 'cues'.padStart(7) + '  result',
)

for (const [id, episode, name] of SHOWS) {
  const row = { name, tracks: 0, picked: '', cues: 0, status: '' }
  const began = Date.now()
  try {
    const play = await api('/api/play', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ animeId: id, episode }),
    })
    if (!play.ok) {
      row.status = 'no release — ' + String(play.body.error ?? '').slice(0, 70)
    } else {
      const source = play.body.streamUrl
      const stream = await api(
        `/api/stream/open?id=${id}&episode=${episode}&source=${encodeURIComponent(source)}`,
        { method: 'POST' },
      )
      if (!stream.ok) {
        row.status = 'stream failed — ' + JSON.stringify(stream.body).slice(0, 70)
      } else {
        const tracks = stream.body.subtitles ?? []
        row.tracks = tracks.length
        if (tracks.length === 0) {
          row.status = 'NO SUBTITLE TRACKS'
        } else {
          const track = chooseTrack(tracks)
          row.picked = `${track.language || '??'}${track.title ? ` "${track.title}"` : ''}`

          // The player re-reads the track while the download is still filling
          // in, so measuring one fetch reports a blank track for anything that
          // has only just started. This follows the same loop, faster.
          let status = 0
          for (let attempt = 0; attempt < REFILLS && row.cues === 0; attempt++) {
            if (attempt > 0) await new Promise((r) => setTimeout(r, REFILL_EVERY))
            const res = await fetch(`${BASE}${track.url}?fill=${attempt}`, {
              signal: AbortSignal.timeout(PER_SHOW_MS),
            })
            status = res.status
            const text = res.ok ? await res.text() : ''
            row.cues = (text.match(/^Dialogue:/gm) ?? []).length
          }
          row.status = row.cues > 0 ? 'ok' : `RENDERS NOTHING (HTTP ${status})`
        }
      }
    }
  } catch (e) {
    row.status = 'error — ' + String(e.message ?? e).slice(0, 70)
  }

  row.seconds = Math.round((Date.now() - began) / 1000)
  rows.push(row)
  console.log(
    row.name.padEnd(22) +
      String(row.tracks).padStart(7) +
      '  ' +
      row.picked.padEnd(24) +
      String(row.cues).padStart(7) +
      `  ${row.status} (${row.seconds}s)`,
  )
}

const good = rows.filter((r) => r.status === 'ok').length
console.log(`\n${good}/${rows.length} shows render subtitles`)
process.exit(good === rows.length ? 0 : 1)
