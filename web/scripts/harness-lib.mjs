// Shared by checks that need the harness's test episode playable: points the
// library at the generated file and assigns it to the catalogue id as ep 1.

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

export async function prepareLocalEpisode({ base, lib, anime, autoplay = true }) {
  const post = (path, body) =>
    fetch(base + path, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })

  for (let i = 0; i < 60; i++) {
    try {
      if ((await fetch(`${base}/api/setup`)).ok) break
    } catch {}
    await sleep(1000)
  }
  await post('/api/local/paths', { paths: [lib] })
  await post('/api/local/scan', {})
  let file
  for (let i = 0; i < 30 && !file; i++) {
    await sleep(1000)
    const files = await (await fetch(`${base}/api/local/files`)).json()
    file = (files.items ?? []).find((f) => String(f.path ?? '').includes('Kuro Test Show'))
  }
  if (!file) return false
  const assign = await post('/api/local/assign', { id: file.id, animeId: Number(anime), episode: 1 })
  if (autoplay) await post('/api/prefs', { key: 'playback.autoplay', value: 'true' })
  return assign.ok
}
