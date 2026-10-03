// Real releases, end to end, without a browser: what the finder offers for an
// episode, what auto-play picks, and whether that pick opens as a stream with
// video, audio and subtitles. Needs live indexers (KURO_EXTRA_CONFIG).
//
//   SURVEY="19:1,37;185875:1,6" PLAY=1 node scripts/run-player-check.mjs playback-survey.mjs
const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const PLAY = process.env.PLAY === '1'
const cases = (process.env.SURVEY ?? '19:1;185875:1').split(';').map((c) => {
  const [id, eps] = c.split(':')
  return { id: Number(id), episodes: eps.split(',').map(Number) }
})

const gb = (n) => (n / 2 ** 30).toFixed(2) + ' GB'
const call = async (method, path, body, ms = 240000) => {
  const res = await fetch(BASE + path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(ms),
  })
  const text = await res.text()
  let json
  try {
    json = JSON.parse(text)
  } catch {
    json = text
  }
  return { status: res.status, json }
}

let problems = 0
const note = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) problems++
}

for (const { id, episodes } of cases) {
  for (const ep of episodes) {
    console.log(`\n===== anime ${id} episode ${ep} =====`)
    const t0 = Date.now()
    const src = await call('GET', `/api/episode/sources?id=${id}&episode=${ep}`)
    if (src.status !== 200) {
      note(false, 'sources', `${src.status} ${JSON.stringify(src.json).slice(0, 300)}`)
      continue
    }
    const { results = [], best, queries = [] } = src.json
    console.log(`  ${results.length} results from ${queries.length} queries in ${Date.now() - t0} ms`)
    console.log(`  queries: ${queries.slice(0, 6).join(' | ')}`)
    for (const r of results.slice(0, 12)) {
      const rel = r.Release ?? r.release ?? {}
      console.log(
        `  ${r.autoPick ? 'AUTO' : '    '} ${String(Math.round(r.score)).padStart(4)} ` +
          `${rel.Batch || rel.batch ? 'BATCH ' : ''}${r.Confirmed ?? r.confirmed ? 'conf ' : ''}` +
          `${gb(r.Torrent?.size ?? r.torrent?.size ?? 0)} s${r.Torrent?.seeders ?? r.torrent?.seeders} ` +
          `${(r.Torrent?.title ?? r.torrent?.title ?? '').slice(0, 110)}` +
          `${r.blocked ? `\n         blocked: ${r.blocked}` : ''}`,
      )
    }
    note(!!best, 'auto-play has a pick', best ? (best.Torrent?.title ?? best.torrent?.title) : 'none')

    if (!PLAY) continue
    const t1 = Date.now()
    // PICK plays the release whose title matches, as the manual picker does.
    const picked = process.env.PICK && results.find((r) => new RegExp(process.env.PICK, 'i').test((r.Torrent ?? r.torrent).title))
    const infoHash = picked ? (picked.Torrent ?? picked.torrent).infoHash : undefined
    // Outlasts kuro's own 6 min wait.
    const play = await call('POST', '/api/play', { animeId: id, episode: ep, infoHash }, 8 * 60000)
    if (play.status !== 200) {
      note(false, 'play', `${play.status} ${JSON.stringify(play.json).slice(0, 400)}`)
      continue
    }
    const s = play.json
    note(true, `play started in ${((Date.now() - t1) / 1000).toFixed(1)}s`, `${s.title} [file ${s.fileIndex}]`)
    if (process.env.PLAY_ONLY === '1') continue

    const open = await call(
      'POST',
      `/api/stream/open?id=${id}&episode=${ep}&source=${encodeURIComponent(s.streamUrl)}&audio=sub`,
    )
    if (open.status !== 200) {
      note(false, 'stream open', `${open.status} ${JSON.stringify(open.json).slice(0, 400)}`)
      continue
    }
    const o = open.json
    console.log(`  plan=${JSON.stringify(o.plan)} duration=${Math.round(o.duration)}s`)
    console.log(`  video=${JSON.stringify(o.video)}`)
    console.log(`  audio=${JSON.stringify(o.audio)} track=${o.audioTrack}`)
    console.log(`  subtitles=${JSON.stringify((o.subtitles ?? []).map((t) => [t.language, t.title, t.codec ?? t.format]))}`)
    note(o.duration > 60, 'duration known', String(o.duration))
    note((o.audio ?? []).length > 0, 'has audio')

    const pl = await fetch(BASE + o.playlist).then((r) => r.text())
    const segs = pl.split('\n').filter((l) => l && !l.startsWith('#'))
    note(segs.length > 3, 'playlist lists segments', `${segs.length}`)
    const t2 = Date.now()
    const init = await fetch(`${BASE}/api/stream/${o.id}/init.mp4`, { signal: AbortSignal.timeout(120000) }).catch((e) => ({ status: e.message }))
    const seg = await fetch(`${BASE}/api/stream/${o.id}/${segs[0]}`, { signal: AbortSignal.timeout(180000) }).catch((e) => ({ status: e.message }))
    const segBytes = seg.arrayBuffer ? (await seg.arrayBuffer()).byteLength : 0
    note(init.status === 200 && seg.status === 200 && segBytes > 10000, 'first segment served',
      `init ${init.status}, seg ${seg.status} ${segBytes} B in ${((Date.now() - t2) / 1000).toFixed(1)}s`)

    for (const t of (o.subtitles ?? []).slice(0, 2)) {
      const sub = await fetch(BASE + t.url, { signal: AbortSignal.timeout(90000) }).catch((e) => ({ status: e.message }))
      const text = sub.text ? await sub.text() : ''
      const lines = (text.match(/^Dialogue:/gm) ?? []).length || (text.match(/-->/g) ?? []).length
      note(sub.status === 200 && lines > 0, `subtitle ${t.language ?? t.title}`, `${sub.status}, ${lines} cues`)
    }

    // SPEED=seconds: how fast the episode keeps downloading while it is open.
    const window = Number(process.env.SPEED ?? 0)
    if (window > 0 && s.infoHash) {
      const samples = []
      for (let t = 0; t < window; t += 10) {
        await new Promise((r) => setTimeout(r, 10000))
        const list = await call('GET', '/api/downloads').then((x) => x.json.items ?? []).catch(() => [])
        const d = list.find((i) => (i.infoHash ?? '').toLowerCase() === s.infoHash.toLowerCase())
        if (d) samples.push(d.mbps ?? 0)
        console.log(`  ${t + 10}s: ${(d?.mbps ?? 0).toFixed(1)} Mbit/s, ${(d?.percent ?? 0).toFixed(1)}% of the episode`)
      }
      const avg = samples.reduce((a, b) => a + b, 0) / Math.max(1, samples.length)
      console.log(`  average ${avg.toFixed(1)} Mbit/s over ${window}s`)
    }

    await call('DELETE', `/api/stream/${o.id}`).catch(() => {})
    await call('POST', '/api/stop', {}).catch(() => {})
  }
}

console.log(problems ? `\n${problems} problem(s)` : '\nall passed')
process.exit(problems ? 1 : 0)
