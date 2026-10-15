import { bytes } from './format'
import type { Download, Queued } from './queries'

/** The downloads list by state, as the Downloads page and the panel both show it. */
export interface DownloadSections {
  active: Download[]
  /** Paused part-way: by hand, or by the queue until its turn. */
  held: Download[]
  finished: Download[]
  /** Claimed by the queue, its release not found yet: no torrent to show. */
  searching: Queued[]
  waitingQueue: Queued[]
  failed: Queued[]
}

export function sortDownloads(items: Download[], queued: Queued[]): DownloadSections {
  // A queue entry and its torrent are one download: the torrent shows while the entry is alive; a failed
  // entry shows instead, with the torrent's progress, rather than a red row plus a paused one under Waiting.
  const entries = new Map(queued.map((q) => [`${q.animeId}-${q.epKey}`, q]))
  const paired = items.map((d) => {
    const q = d.episodes.map((e) => entries.get(`${d.animeId}-${e}`)).find(Boolean)
    return q ? { ...d, queued: q } : d
  })
  const absorbed = (d: Download) => d.percent < 100 && !!d.paused && d.queued?.state === 'failed'
  const started = new Set(paired.filter((d) => !absorbed(d)).flatMap((d) => d.episodes.map((e) => `${d.animeId}-${e}`)))
  const unstarted = (q: Queued) => !started.has(`${q.animeId}-${q.epKey}`)
  return {
    active: paired.filter((d) => d.percent < 100 && !d.paused),
    held: paired.filter((d) => d.percent < 100 && d.paused && !absorbed(d)),
    finished: paired.filter((d) => d.percent >= 100),
    searching: queued.filter((q) => q.state === 'active' && unstarted(q)),
    waitingQueue: queued.filter((q) => q.state === 'pending' && unstarted(q)),
    failed: queued
      .filter((q) => q.state === 'failed')
      .map((q) => {
        const torrent = paired.find((d) => absorbed(d) && d.queued === q)
        return torrent ? { ...q, torrent } : q
      }),
  }
}

/** "tries again at 03:41" for an entry waiting out a dead swarm, or nothing. */
export function retryNote(q: Queued | undefined): string {
  if (!q?.retryAt) return ''
  return `tries again at ${new Date(q.retryAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
}

/** What a download is, for a person: the show and episode, or the release when no show is attached. */
export function downloadLabel(d: Download): string {
  if (!d.title) return d.name
  return d.episodes.length > 0 ? `${d.title} episode${d.episodes.length > 1 ? 's' : ''} ${d.episode}` : d.title
}

/** How far, how big, how fast: the line under a download that is still arriving. */
export function downloadProgress(d: Download): string {
  return [
    `${Math.round(d.percent)}%`,
    `${bytes(d.bytesOnDisk)} of ${bytes(d.totalBytes)}`,
    !d.paused && d.mbps ? `${d.mbps.toFixed(1)} Mbps` : '',
    !d.paused && d.peers ? `${d.peers} peers` : '',
    d.paused ? 'paused' : '',
    d.checking ? 'checking file' : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

/** The whole list as plain text, to paste into a bug report. */
export function downloadsReport(s: DownloadSections): string {
  const waiting = (q: Queued | undefined) => (q?.retryAt ? ` · ${retryNote(q)}: ${q.error}` : '')
  const queuedLine = (q: Queued) =>
    `- ${q.title ?? `Anime ${q.animeId}`} episode ${q.episode}` +
    `${q.torrent ? ` — ${Math.round(q.torrent.percent)}% on disk` : ''}${q.error ? ` — ${q.error}` : ''}${waiting(q)}`
  const downloadLine = (d: Download) =>
    `- ${downloadLabel(d)} — ${downloadProgress(d)}${d.pinned ? ' · playing' : ''}${d.kept ? ' · kept' : ''}${waiting(d.queued)}` +
    // The release under the show; one under no show is already named by it.
    (d.title ? `\n  ${d.name}` : '')
  const section = (name: string, lines: string[]) => (lines.length ? [`${name} (${lines.length})`, ...lines, ''] : [])

  return [
    `kuro downloads at ${new Date().toLocaleTimeString()}`,
    '',
    ...section('Downloading', [...s.searching.map((q) => `${queuedLine(q)} — finding a release`), ...s.active.map(downloadLine)]),
    ...section('Waiting', [...s.held.map(downloadLine), ...s.waitingQueue.map(queuedLine)]),
    ...section('Failed', s.failed.map(queuedLine)),
    ...section('On disk', s.finished.map((d) => `- ${downloadLabel(d)} — ${bytes(d.bytesOnDisk)}${d.kept ? ' · kept' : ''}`)),
  ]
    .join('\n')
    .trim()
}

/** Copies text; the clipboard API only exists on https and localhost, so a LAN address needs the old way. */
export async function copyText(text: string, within: HTMLElement = document.body): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Refused or unavailable: the selection-based copy below still works.
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.cssText = 'position:fixed;top:0;left:0;opacity:0'
  // Inside the fullscreen element when there is one: outside it nothing can take focus.
  within.appendChild(area)
  area.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  area.remove()
  return ok
}
