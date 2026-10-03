// Prints info hashes of releases the finder offers, one per HASH entry.
//   HASH="19:2:CBM;185875:1:Erai.*1080p.*AVC" node scripts/run-player-check.mjs print-hash.mjs
const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
for (const spec of (process.env.HASH ?? '19:2:CBM').split(';')) {
  const [id, ep, pattern] = spec.split(':')
  const src = await (await fetch(`${BASE}/api/episode/sources?id=${id}&episode=${ep}`)).json()
  const rel = (src.results ?? []).find((r) => new RegExp(pattern, 'i').test(r.Torrent.title))
  console.log(`HASH ${rel?.Torrent.infoHash} ${rel?.Torrent.seeders} ${rel?.Torrent.title}`)
}
