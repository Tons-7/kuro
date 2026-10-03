// Shared by the scripts that start their own kuro.
import { copyFileSync, linkSync, mkdirSync, readdirSync, symlinkSync } from 'node:fs'
import { join } from 'node:path'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// linkBin gives an instance the repo's programs, hard-linked file by file: it
// can delete its copies (kuro removes obsolete binaries) without touching the
// repo's, which a junction to the whole folder would. only: names to link.
export function linkBin(from, to, only = null) {
  const exe = (n) => (process.platform === 'win32' ? `${n}.exe` : n)
  mkdirSync(to, { recursive: true })
  for (const entry of readdirSync(from, { withFileTypes: true })) {
    if (only && !only.some((n) => entry.name === exe(n) || entry.name === n)) continue
    const src = join(from, entry.name)
    const dst = join(to, entry.name)
    if (entry.isDirectory()) {
      symlinkSync(src, dst, process.platform === 'win32' ? 'junction' : 'dir')
      continue
    }
    try {
      linkSync(src, dst)
    } catch {
      copyFileSync(src, dst)
    }
  }
}

// prepareLocalEpisode makes the harness's test episode playable: points the
// library at the generated file and assigns it to the catalogue id as ep 1.
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
