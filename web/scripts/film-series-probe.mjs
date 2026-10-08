// What kuro offers for each film of a series, and which file it starts for one of them.
//   KURO_EXTRA_CONFIG='[[indexer]] ...' FILMS=5204,5205 PLAY=5204 node scripts/run-player-check.mjs film-series-probe.mjs
// Reports only. Indexers come from the environment.
const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const FILMS = (process.env.FILMS ?? '').split(',').filter(Boolean).map(Number)
const PLAY = Number(process.env.PLAY ?? 0)
const get = async (path) => {
  const res = await fetch(`${BASE}${path}`)
  return res.ok ? res.json() : { error: `${res.status} ${(await res.text()).slice(0, 200)}` }
}

for (const id of FILMS) {
  const detail = await get(`/api/anime/${id}`)
  const sources = await get(`/api/episode/sources?id=${id}&episode=1`)
  const results = sources.results ?? []
  const picks = results.filter((r) => r.autoPick)
  const blocked = {}
  for (const r of results) if (r.blocked) blocked[r.blocked] = (blocked[r.blocked] ?? 0) + 1
  console.log(`\n=== ${id} ${detail.anime?.title?.english ?? detail.title ?? ''}: ${results.length} found, ${picks.length} usable${sources.error ? ` (${sources.error})` : ''}`)
  for (const r of picks.slice(0, 5)) {
    console.log(`  ${r.Confirmed ? 'confirmed' : 'unconfirmed'}  seeders ${String(r.Torrent.seeders).padStart(3)}  ${r.Release.Batch ? 'batch ' : 'single'}  ${r.Torrent.title.slice(0, 95)}`)
  }
  console.log('  blocked:', JSON.stringify(blocked))
}

if (PLAY) {
  console.log(`\n=== starting ${PLAY}`)
  const t = Date.now()
  const res = await fetch(`${BASE}/api/play`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ animeId: PLAY, episode: 1 }),
  })
  const session = await res.json().catch(() => ({}))
  console.log(`  ${res.status} after ${((Date.now() - t) / 1000).toFixed(0)}s  release: ${session.title ?? session.error}`)
  console.log(`  file index ${session.fileIndex}`)
  const downloads = await get('/api/downloads')
  for (const d of downloads.items ?? []) console.log(`  holding: ${JSON.stringify({ name: d.name, file: d.file ?? d.path ?? d.filePath, episode: d.episode }).slice(0, 240)}`)
}
