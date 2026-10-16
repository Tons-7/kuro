import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { copyText, downloadProgress, downloadsReport, retryNote, sortDownloads } from '../lib/downloads'
import { cx } from '../lib/format'
import { refreshDownloads, useDownloadQueue, useDownloads, type Download, type Queued } from '../lib/queries'
import { ProgressBar, useDismiss } from './ui'
import { plainKey, usePortalHome } from './keys'

/** Downloads at a glance over the episode playing (D toggles it); look-only, so a stray key removes nothing. */
export function DownloadsPanel() {
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close, open)
  const trigger = useRef<HTMLButtonElement>(null)
  const home = usePortalHome()

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (plainKey(e) === 'd') setOpen((v) => !v)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const downloads = useDownloads()
  const queue = useDownloadQueue()
  const sections = sortDownloads(downloads.data?.items ?? [], queue.data?.items ?? [])
  const { active, held, finished, searching, waitingQueue, failed } = sections
  const busy = active.length + searching.length

  // The one control here: it stops and starts everything, and cannot lose a download.
  const qc = useQueryClient()
  const paused = queue.data?.paused ?? false
  const pauseAll = useMutation({
    mutationFn: (pause: boolean) => api.post(`/api/download/queue/${pause ? 'pause' : 'resume'}`),
    onSuccess: () => refreshDownloads(qc),
  })

  const copy = async () => {
    setCopied(await copyText(downloadsReport(sections), home))
    window.setTimeout(() => setCopied(false), 2000)
  }

  // Under its button, or the corner when fullscreen hides the button.
  // A hidden button (a narrow window) measures zero, so that is the corner too.
  const measured = home === document.body ? trigger.current?.getBoundingClientRect() : undefined
  const box = measured && measured.width > 0 ? measured : undefined

  return (
    <div className="relative shrink-0" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={busy ? `Downloads, ${busy} in progress` : 'Downloads'}
        title="Downloads (D)"
        onClick={() => setOpen((v) => !v)}
        className="tap relative grid size-9 place-items-center rounded-md text-base-300 transition-colors hover:bg-base-800 hover:text-white"
      >
        <ArrowIcon />
        {busy > 0 && (
          <span className="absolute top-1.5 right-1.5 size-2 rounded-full bg-accent-500 ring-2 ring-base-950" />
        )}
      </button>

      {open &&
        createPortal(
          <div
            role="dialog"
            aria-label="Downloads"
            data-portal-menu
            style={{
              top: box ? box.bottom + 8 : 16,
              right: box ? Math.max(8, window.innerWidth - box.right) : 16,
            }}
            className="fixed z-50 w-96 max-w-[calc(100vw-1rem)] animate-rise overflow-hidden rounded-xl border border-base-750 bg-base-850 shadow-panel"
          >
            <div className="flex items-center justify-between gap-2 border-b border-base-750 px-3 py-2">
              <p className="text-sm font-medium text-base-100">
                Downloads
                {paused && <span className="ml-1.5 rounded bg-amber-400/15 px-1.5 text-[11px] text-amber-300">Paused</span>}
              </p>
              <div className="flex items-center gap-1">
                {(paused || busy + held.length + waitingQueue.length > 0) && (
                  <button
                    onClick={() => pauseAll.mutate(!paused)}
                    disabled={pauseAll.isPending}
                    title={paused ? 'Start downloading again' : 'Stop every download until you resume'}
                    className="rounded px-1.5 py-0.5 text-[11px] text-base-300 transition-colors hover:bg-base-800 hover:text-white"
                  >
                    {paused ? 'Resume' : 'Pause all'}
                  </button>
                )}
                <button
                  onClick={copy}
                  title="Copy this list as text, to paste into a report"
                  className="rounded px-1.5 py-0.5 text-[11px] text-base-400 transition-colors hover:bg-base-800 hover:text-white"
                >
                  {copied ? 'Copied' : 'Copy details'}
                </button>
              </div>
            </div>

            {/* Shows, in the order the queue works them; each episode line says where it stands. */}
            {busy + held.length + waitingQueue.length + failed.length === 0 ? (
              <p className="px-3 py-6 text-center text-xs text-base-500">Nothing is downloading.</p>
            ) : (
              <ul className="max-h-[60vh] space-y-2.5 overflow-y-auto p-2.5 scrollbar-thin">
                {byShow([
                  ...searching.map((q) => ({ q, note: 'Finding a release…' })),
                  ...active.map((d) => ({ d })),
                  ...held.map((d) => ({ d })),
                  ...waitingQueue.map((q) => ({ q, note: 'waiting' })),
                  ...failed.map((q) => ({ q, note: q.error ?? 'failed', failed: true })),
                ]).map((g) => (
                  <ShowCard key={g.key} group={g} />
                ))}
              </ul>
            )}

            <Link
              to="/downloads"
              onClick={close}
              className="block border-t border-base-750 px-3 py-2 text-center text-xs text-base-400 transition-colors hover:bg-base-800 hover:text-white"
            >
              {finished.length > 0 ? `${finished.length} on disk · open Downloads` : 'Open Downloads'}
            </Link>
          </div>,
          home,
        )}
    </div>
  )
}

// One card per show, as on the Downloads page.
const row = 'rounded-lg border border-white/[0.08] bg-base-900/60 px-3 py-2.5'

/** A torrent or a queue entry, with the note a queue entry carries (why it waits, why it failed). */
interface Entry {
  d?: Download
  q?: Queued
  note?: string
  failed?: boolean
}

interface ShowGroup {
  key: string
  title: string
  cover?: string
  entries: Entry[]
}

// The show is the card, its episodes the lines (one name, not eight); a release under no show is its own card.
function byShow(entries: Entry[]): ShowGroup[] {
  const groups: ShowGroup[] = []
  const index = new Map<string, ShowGroup>()
  for (const e of entries) {
    const animeId = e.d?.animeId ?? e.q?.animeId
    const stray = !animeId || (e.d && !e.d.title)
    const key = stray ? `t-${e.d?.infoHash ?? `${e.q?.animeId}-${e.q?.epKey}`}` : `a-${animeId}`
    let g = index.get(key)
    if (!g) {
      const title = e.d?.title ?? e.q?.title ?? (e.d ? e.d.name : `Anime ${animeId}`)
      g = { key, title, cover: e.d?.cover ?? e.q?.cover ?? undefined, entries: [] }
      index.set(key, g)
      groups.push(g)
    }
    g.entries.push(e)
  }
  return groups
}

function ShowCard({ group }: { group: ShowGroup }) {
  return (
    <li className={row}>
      <div className="flex items-center gap-2">
        {group.cover && <img src={group.cover} alt="" loading="lazy" className="h-9 w-6 shrink-0 rounded object-cover" />}
        <p className="truncate text-xs font-medium text-base-100">{group.title}</p>
      </div>
      <ul className="mt-2.5 space-y-2.5">
        {group.entries.map((e) =>
          e.d ? <EpisodeLine key={e.d.infoHash} d={e.d} /> : <QueuedLine key={`${e.q!.animeId}-${e.q!.epKey}`} {...e} />,
        )}
      </ul>
    </li>
  )
}

// The episode and how it is doing; a release under no show has only the progress, its name being the card.
function EpisodeLine({ d }: { d: Download }) {
  const label = d.title && d.episodes.length > 0 ? `Episode${d.episodes.length > 1 ? 's' : ''} ${d.episode}` : ''
  const retry = retryNote(d.queued)
  return (
    <li title={d.name}>
      <p className="flex items-baseline gap-2 text-[11px]">
        {label && <span className="shrink-0 font-medium text-base-200">{label}</span>}
        {d.pinned && <span className="shrink-0 text-accent-300">playing</span>}
        <span className="min-w-0 truncate text-base-500">{downloadProgress(d)}</span>
      </p>
      {retry && <p className="truncate text-[11px] text-amber-300">{retry} · {d.queued?.error}</p>}
      <ProgressBar value={d.percent} className="mt-1.5" />
    </li>
  )
}

// A failed entry carries what its torrent got before giving up.
function QueuedLine({ q, note, failed }: Entry) {
  const got = q!.torrent
  return (
    <li>
      <p className="flex items-baseline gap-2 text-[11px]">
        <span className="shrink-0 font-medium text-base-200">Episode {q!.episode}</span>
        {got && <span className="shrink-0 text-base-500">{Math.round(got.percent)}% on disk</span>}
        {note && <span className={cx('min-w-0 truncate', failed ? 'text-recap' : 'text-base-500')}>{note}</span>}
      </p>
      {got && <ProgressBar value={got.percent} className="mt-1.5" />}
    </li>
  )
}

function ArrowIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path
        d="M12 4v10m0 0 4-4m-4 4-4-4M5 18h14"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
