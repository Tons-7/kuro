import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type DownloadFile, type Page } from '../lib/api'
import { bytes, cx, relativeTime } from '../lib/format'
import { useNotifications, type Notification } from '../lib/queries'
import {
  Badge,
  Button,
  Empty,
  ErrorState,
  FilterToggle,
  LinkButton,
  PageHeader,
  ProgressBar,
  Skeleton,
  useDebounced,
} from '../components/ui'
import { notificationTarget } from '../components/NotificationPanel'

export function Notifications() {
  const { data, isPending, isError, error, refetch } = useNotifications()
  const qc = useQueryClient()

  const [confirmClear, setConfirmClear] = useState(false)

  const markRead = useMutation({
    mutationFn: (id?: number) =>
      api.post(`/api/notifications/read${id ? `?id=${id}` : ''}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notifications'] }),
  })
  const remove = useMutation({
    mutationFn: (id?: number) => api.del(`/api/notifications${id ? `?id=${id}` : ''}`),
    onSuccess: () => {
      setConfirmClear(false)
      void qc.invalidateQueries({ queryKey: ['notifications'] })
    },
  })

  if (isError) return <ErrorState error={error} retry={() => refetch()} />
  if (isPending) return <Skeleton className="h-64 w-full" />
  if (data.items.length === 0) {
    return (
      <div className="space-y-4">
        <PageHeader title="Notifications" />
        <Empty
          title="No notifications"
          hint="Follow a show, or tag it as watching, and new episodes are announced here."
          icon={<BellIcon />}
          action={<LinkButton to="/schedule">See what airs this week</LinkButton>}
        />
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <h1 className="page-title">
          Notifications
          {data.unread > 0 && <span className="ml-2 text-sm text-accent-400">{data.unread} new</span>}
        </h1>
        <div className="flex gap-2">
          {data.unread > 0 && !confirmClear && (
            <button
              onClick={() => markRead.mutate(undefined)}
              className="rounded-md bg-base-850 px-3 py-1.5 text-sm text-base-200 hover:bg-base-750"
            >
              Mark all read
            </button>
          )}
          {confirmClear ? (
            <>
              <button
                onClick={() => remove.mutate(undefined)}
                className="rounded-md bg-red-500/90 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-500"
              >
                Delete all
              </button>
              <button
                onClick={() => setConfirmClear(false)}
                className="rounded-md px-3 py-1.5 text-sm text-base-400 hover:text-base-100"
              >
                Cancel
              </button>
            </>
          ) : (
            <button
              onClick={() => setConfirmClear(true)}
              className="rounded-md bg-base-850 px-3 py-1.5 text-sm text-base-200 hover:bg-base-750"
            >
              Clear all
            </button>
          )}
        </div>
      </div>

      <ul className="surface divide-y divide-white/[0.05] overflow-hidden">
        {data.items.map((n: Notification) => (
          <li
            key={n.id}
            className={cx('group flex items-center', !n.read && 'bg-accent-500/5')}
          >
            <Link
              to={notificationTarget(n)}
              onClick={() => !n.read && markRead.mutate(n.id)}
              className="flex min-w-0 flex-1 items-start gap-3 p-3 transition-colors hover:bg-base-900"
            >
              {!n.read && <span className="mt-1.5 size-2 shrink-0 rounded-full bg-accent-500" />}
              <div className={cx('min-w-0 flex-1', n.read && 'pl-5')}>
                <p className="text-sm text-base-100">{n.title}</p>
                <p className="mt-0.5 truncate text-xs text-base-500">{n.body}</p>
              </div>
              <span className="shrink-0 text-xs text-base-500">{relativeTime(n.createdAt)}</span>
            </Link>
            <button
              onClick={() => remove.mutate(n.id)}
              aria-label="Delete notification"
              className="mr-2 rounded px-2 py-1 text-base-600 opacity-0 transition-opacity group-hover:opacity-100 hover:bg-base-800 hover:text-base-200 max-sm:opacity-100"
            >
              ✕
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

interface Download {
  infoHash: string
  /** The release filename. `title` is the show it belongs to. */
  name: string
  title?: string
  cover?: string
  animeId?: number
  /** Episodes joined for display; a pack has several. */
  episode?: string
  episodes: string[]
  totalBytes: number
  bytesOnDisk: number
  percent: number
  pinned: boolean
  /** Asked for, so outside the cache budget and never evicted. */
  kept: boolean
  state: string
  paused?: boolean
  /** rqbit verifying the file after a launch. */
  checking?: boolean
  mbps?: number
  peers?: number
}

function BellIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-6" aria-hidden>
      <path
        d="M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15L6 16ZM10 20a2 2 0 0 0 4 0"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function DownloadIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-6" aria-hidden>
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

function FolderIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-6" aria-hidden>
      <path
        d="M4 7.5A1.5 1.5 0 0 1 5.5 6h3.4a1.5 1.5 0 0 1 1.2.6l.9 1.2h7.5A1.5 1.5 0 0 1 20 9.3v8.2a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 17.5v-10Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function PauseGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M9 5v14M15 5v14" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" />
    </svg>
  )
}

function PlayGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M8 5.5v13l10-6.5-10-6.5Z" fill="currentColor" />
    </svg>
  )
}

interface Queued {
  animeId: number
  epKey: string
  episode: number
  state: string
  error?: string
  title?: string
  cover?: string
}

// A pack's episodes, each with its own keep: the row's toggle moves all of
// them, which is rarely what "keep episode 5" meant.
function PackEpisodes({ hash }: { hash: string }) {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const files = useQuery({
    enabled: open,
    queryKey: ['download-files', hash],
    queryFn: () => api.get<{ items: DownloadFile[] }>(`/api/downloads/${hash}/files`),
  })
  const keep = useMutation({
    mutationFn: (f: DownloadFile) =>
      api.post(`/api/downloads/${hash}/files/${f.fileIndex}/${f.kept ? 'unkeep' : 'keep'}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['download-files', hash] })
      void qc.invalidateQueries({ queryKey: ['downloads'] })
      void qc.invalidateQueries({ queryKey: ['cache'] })
    },
  })

  return (
    <div className="mt-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="text-xs text-base-500 hover:text-base-200"
      >
        {open ? 'Hide episodes' : 'Keep episodes one by one…'}
      </button>
      {open && (
        <ul className="mt-1 grid gap-1 sm:grid-cols-2">
          {(files.data?.items ?? []).map((f) => (
            <li
              key={f.fileIndex}
              className="flex items-center justify-between rounded-md bg-base-900 px-2 py-1 text-xs"
            >
              <span className="text-base-300">
                Episode {f.epKey}
                {!f.complete && <span className="text-base-600"> · downloading</span>}
              </span>
              <button
                onClick={() => keep.mutate(f)}
                disabled={keep.isPending}
                aria-pressed={f.kept}
                className={cx(
                  'rounded px-2 py-0.5',
                  f.kept ? 'bg-accent-500/15 text-accent-300' : 'text-base-400 hover:bg-base-800',
                )}
              >
                {f.kept ? '✓ Kept' : 'Keep'}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function setKept(qc: ReturnType<typeof useQueryClient>, infoHash: string, kept: boolean) {
  qc.setQueryData<{ items: Download[]; count: number }>(['downloads'], (old) =>
    old
      ? { ...old, items: old.items.map((d) => (d.infoHash === infoHash ? { ...d, kept } : d)) }
      : old,
  )
}

export function Downloads() {
  const qc = useQueryClient()
  const [confirmClear, setConfirmClear] = useState(false)

  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ['downloads'],
    queryFn: () => api.get<{ items: Download[]; count: number }>('/api/downloads'),
    // Percentages only move while something is downloading; the rest of the
    // time a poll is a torrent-engine round trip for nothing.
    refetchInterval: (q) =>
      (q.state.data?.items ?? []).some((d) => !d.paused && d.percent < 100) ? 5000 : 30_000,
  })

  const queue = useQuery({
    queryKey: ['download-queue'],
    queryFn: () => api.get<{ items: Queued[] }>('/api/download/queue'),
    refetchInterval: (q) => ((q.state.data?.items ?? []).length ? 5000 : 30_000),
  })

  const done = () => {
    setConfirmClear(false)
    void qc.invalidateQueries({ queryKey: ['downloads'] })
    void qc.invalidateQueries({ queryKey: ['download-queue'] })
    void qc.invalidateQueries({ queryKey: ['cache'] })
  }

  // Which episodes the queue is holding, so a download paused by the queue can
  // say so rather than looking abandoned.
  const waitingKeys = new Set(
    (queue.data?.items ?? [])
      .filter((q) => q.state === 'pending')
      .map((q) => `${q.animeId}-${q.epKey}`),
  )

  const dequeue = useMutation({
    mutationFn: (q: Queued) =>
      api.del(`/api/download/all/${q.animeId}?episode=${encodeURIComponent(q.epKey)}`),
    onSuccess: done,
  })

  const requeue = useMutation({
    mutationFn: (q: Queued) =>
      api.post('/api/download', { animeId: q.animeId, episode: q.episode }),
    onSuccess: done,
  })

  const remove = useMutation({
    mutationFn: (hash: string) => api.del(`/api/downloads/${hash}`),
    onSuccess: done,
  })

  const pause = useMutation({
    mutationFn: (hash: string) => api.post(`/api/downloads/${hash}/pause`),
    onSuccess: done,
  })

  const resume = useMutation({
    mutationFn: (hash: string) => api.post(`/api/downloads/${hash}/resume`),
    onSuccess: done,
  })
  const clear = useMutation({
    mutationFn: (scope: string) => api.post(`/api/downloads/clear?scope=${scope}`),
    onSuccess: done,
  })
  // Optimistic: a click on a list fetched a moment ago used to send the wrong
  // action and look like nothing happened.
  const keep = useMutation({
    mutationFn: (d: Download) =>
      api.post<{ infoHash: string; kept: boolean }>(
        `/api/downloads/${d.infoHash}/${d.kept ? 'unkeep' : 'keep'}`,
      ),
    onMutate: async (d: Download) => {
      await qc.cancelQueries({ queryKey: ['downloads'] })
      const previous = qc.getQueryData<{ items: Download[]; count: number }>(['downloads'])
      setKept(qc, d.infoHash, !d.kept)
      return { previous }
    },
    onError: (_err, _d, context) => {
      if (context?.previous) qc.setQueryData(['downloads'], context.previous)
    },
    onSuccess: (res) => setKept(qc, res.infoHash, res.kept),
    onSettled: done,
  })

  if (isError) return <ErrorState error={error} retry={() => refetch()} />

  const removable = (data?.items ?? []).filter((d) => !d.pinned).length

  return (
    <div className="space-y-4">
      <PageHeader
        title="Downloads"
        meta={data ? `${data.items.length} on disk` : undefined}
        actions={
          removable > 0 &&
          (confirmClear ? (
            <>
              <Button onClick={() => clear.mutate('completed')}>Finished only</Button>
              <Button variant="danger" onClick={() => clear.mutate('all')}>
                Everything
              </Button>
              <Button variant="ghost" onClick={() => setConfirmClear(false)}>
                Cancel
              </Button>
            </>
          ) : (
            <Button onClick={() => setConfirmClear(true)}>Clear…</Button>
          ))
        }
      />

      {(remove.isError || clear.isError || keep.isError) && (
        <p className="text-xs text-recap">
          {((remove.error ?? clear.error ?? keep.error) as Error)?.message}
        </p>
      )}

      {isPending ? (
        <Skeleton className="h-64 w-full" />
      ) : data.items.length === 0 ? (
        <Empty
          title="Nothing downloaded"
          hint="Episodes you watch are cached here, and anything you download stays until you remove it."
          icon={<DownloadIcon />}
          action={<LinkButton to="/recent">Find something to watch</LinkButton>}
        />
      ) : (
        <ul className="surface divide-y divide-white/[0.05] overflow-hidden">
          {data.items.map((d) => (
            <li key={d.infoHash} className="p-3">
              <div className="flex items-start justify-between gap-3">
                {d.cover && (
                  <Link to={`/anime/${d.animeId}`} className="shrink-0">
                    <img
                      src={d.cover}
                      alt=""
                      loading="lazy"
                      className="h-14 w-10 rounded object-cover shadow-card"
                    />
                  </Link>
                )}
                <div className="min-w-0 flex-1">
                  {/* The show first: a release filename does not say what you
                      downloaded, which is the question this page answers. */}
                  <p className="truncate text-sm font-medium text-base-100">
                    {d.title ?? d.name}
                    {d.episodes.length > 0 && (
                      <span className="ml-1.5 font-normal text-base-400">
                        episode{d.episodes.length > 1 ? 's' : ''} {d.episode}
                      </span>
                    )}
                  </p>
                  {d.title && (
                    <p className="truncate text-xs text-base-600" title={d.name}>
                      {d.name}
                    </p>
                  )}
                  <p className="mt-0.5 text-xs text-base-500">
                    {bytes(d.bytesOnDisk)} of {bytes(d.totalBytes)}
                    {/* Speed and peers are what separate slow from stalled. */}
                    {!d.paused && d.percent < 100 && d.mbps ? (
                      <span className="text-base-400"> · {d.mbps.toFixed(1)} Mbps</span>
                    ) : null}
                    {!d.paused && d.percent < 100 && d.peers ? ` · ${d.peers} peers` : ''}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  {d.pinned && <Badge tone="accent">Playing</Badge>}
                  {d.checking && <Badge>Checking file…</Badge>}
                  {/* Downloads run one at a time, so a paused row is usually
                      just waiting its turn — "Paused" would read as stuck. */}
                  {d.paused && d.percent < 100 && (
                    <Badge tone={d.episodes.some((e) => waitingKeys.has(`${d.animeId}-${e}`)) ? 'neutral' : 'warning'}>
                      {d.episodes.some((e) => waitingKeys.has(`${d.animeId}-${e}`)) ? 'Queued' : 'Paused'}
                    </Badge>
                  )}
                  {!d.paused && !d.checking && d.percent < 100 && <Badge tone="accent">Downloading</Badge>}
                  {/* Cached is what watching leaves behind and the sweep may
                      take; Downloaded was asked for and stays. */}
                  {d.percent >= 100 ? (
                    <Badge tone={d.kept ? 'success' : 'neutral'}>{d.kept ? 'Downloaded' : 'Cached'}</Badge>
                  ) : (
                    <span className="w-10 text-right text-xs tabular-nums text-base-400">
                      {Math.round(d.percent)}%
                    </span>
                  )}

                  {/* Pausing keeps the file; removing does not. Finished ones
                      have nothing left to pause. */}
                  {d.percent < 100 && !d.checking && (
                    <button
                      onClick={() =>
                        (d.paused ? resume : pause).mutate(d.infoHash)
                      }
                      disabled={pause.isPending || resume.isPending}
                      aria-label={d.paused ? 'Resume download' : 'Pause download'}
                      title={d.paused ? 'Resume' : 'Pause'}
                      className="grid size-7 place-items-center rounded-md text-base-400 transition-colors hover:bg-base-800 hover:text-white"
                    >
                      {d.paused ? <PlayGlyph /> : <PauseGlyph />}
                    </button>
                  )}

                  {/* Labelled by state: an action label next to the badge read
                      as the state and looked wrong. */}
                  <button
                    onClick={() => keep.mutate(d)}
                    aria-pressed={d.kept}
                    title={
                      d.kept
                        ? `Kept: never evicted, outside the cache budget. Click to let the cache evict ${
                            d.episodes.length > 1 ? `all ${d.episodes.length} episodes` : 'it'
                          } again.`
                        : `Keep${
                            d.episodes.length > 1 ? ` all ${d.episodes.length} episodes` : ''
                          }: never evicted, not counted in the cache budget.`
                    }
                    className={cx(
                      'rounded-md px-2 py-1 text-xs transition-colors',
                      d.kept
                        ? 'bg-accent-500/15 text-accent-300 hover:bg-accent-500/25'
                        : 'text-base-400 hover:bg-base-800 hover:text-white',
                    )}
                  >
                    {d.kept ? '✓ Kept' : 'Keep'}
                  </button>

                  {!d.pinned && (
                    <button
                      onClick={() => remove.mutate(d.infoHash)}
                      disabled={remove.isPending}
                      aria-label={`Delete ${d.title ?? d.name}`}
                      title="Remove and delete the file"
                      className="grid size-7 place-items-center rounded-md text-base-600 transition-colors hover:bg-base-800 hover:text-recap"
                    >
                      ✕
                    </button>
                  )}
                </div>
              </div>
              <ProgressBar value={d.percent} className="mt-2" />
              {d.episodes.length > 1 && <PackEpisodes hash={d.infoHash} />}
            </li>
          ))}
        </ul>
      )}

      <QueueList
        items={queue.data?.items ?? []}
        onRemove={(q) => dequeue.mutate(q)}
        onRetry={(q) => requeue.mutate(q)}
      />
    </div>
  )
}

/**
 * What is waiting its turn. Downloads run one at a time, so showing the order
 * tells you "yours is fourth" rather than "nothing is happening".
 */
function QueueList({
  items,
  onRemove,
  onRetry,
}: {
  items: Queued[]
  onRemove: (q: Queued) => void
  onRetry: (q: Queued) => void
}) {
  const waiting = items.filter((q) => q.state !== 'active')
  if (items.length === 0) return null

  return (
    <section className="space-y-2">
      <div className="flex items-baseline justify-between">
        <h2 className="section-title">Queue</h2>
        <p className="text-sm text-base-500">
          {waiting.length} waiting · one at a time
        </p>
      </div>

      <ul className="surface divide-y divide-white/[0.05] overflow-hidden">
        {items.map((q) => (
          <li key={`${q.animeId}-${q.epKey}`} className="group flex items-center gap-3 p-2.5">
            {q.cover ? (
              <img src={q.cover} alt="" loading="lazy" className="h-10 w-7 shrink-0 rounded object-cover" />
            ) : (
              <div className="h-10 w-7 shrink-0 rounded bg-base-850" />
            )}

            <div className="min-w-0 flex-1">
              <p className="truncate text-sm text-base-100">
                {q.title ?? `Anime ${q.animeId}`}
                <span className="ml-1.5 text-base-400">episode {q.episode}</span>
              </p>
              {q.error && <p className="truncate text-xs text-recap">{q.error}</p>}
            </div>

            {q.state === 'active' ? (
              <Badge tone="accent">Downloading</Badge>
            ) : q.state === 'failed' ? (
              <Badge tone="recap">Failed</Badge>
            ) : (
              <Badge>Queued</Badge>
            )}

            {q.state === 'failed' && (
              <button
                onClick={() => onRetry(q)}
                className="shrink-0 rounded-md px-2 py-1 text-xs text-base-300 transition-colors hover:bg-base-800 hover:text-white"
              >
                Try again
              </button>
            )}

            {/* Including the one downloading: cancelling stops it and keeps
                what is already on disk. */}
            <button
              onClick={() => onRemove(q)}
              aria-label={q.state === 'active' ? 'Stop this download' : 'Remove from queue'}
              className="grid size-7 shrink-0 place-items-center rounded-md text-base-600 opacity-0 transition-opacity group-hover:opacity-100 hover:text-recap focus-visible:opacity-100"
            >
              ✕
            </button>
          </li>
        ))}
      </ul>
    </section>
  )
}

interface LocalFile {
  id: number
  path: string
  size: number
  animeId?: number
  episode?: number
  parsedTitle?: string
  resolution?: string
  releaseGroup?: string
  confidence: number
}

interface SearchHit {
  id: number
  title: { romaji?: string; english?: string }
  coverImage?: { medium?: string }
  format?: string
  seasonYear?: number
  episodes?: number
}

// Match a file to a show and episode by hand: search, pick, number, save.
function AssignFile({ file, onDone }: { file: LocalFile; onDone: () => void }) {
  const qc = useQueryClient()
  const [term, setTerm] = useState(file.parsedTitle ?? '')
  const q = useDebounced(term, 300)
  const [picked, setPicked] = useState<SearchHit | null>(null)
  const [episode, setEpisode] = useState(String(file.episode || 1))

  const results = useQuery({
    enabled: q.trim().length >= 3 && !picked,
    queryKey: ['search', q],
    queryFn: () => api.get<{ results: SearchHit[] }>(`/api/search?q=${encodeURIComponent(q)}&limit=8`),
    staleTime: 60_000,
  })

  const assign = useMutation({
    mutationFn: () =>
      api.post('/api/local/assign', { id: file.id, animeId: picked!.id, episode: Number(episode) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['localFiles'] })
      void qc.invalidateQueries({ queryKey: ['local'] })
      onDone()
    },
  })

  const name = (h: SearchHit) => h.title.english || h.title.romaji || `Anime ${h.id}`

  return (
    <div className="mt-2 space-y-2 rounded-lg border border-base-850 bg-base-950/40 p-3">
      {picked ? (
        <div className="flex items-center gap-3">
          {picked.coverImage?.medium && (
            <img src={picked.coverImage.medium} alt="" className="h-12 w-8 rounded object-cover" />
          )}
          <p className="min-w-0 flex-1 truncate text-sm text-base-100">{name(picked)}</p>
          <button onClick={() => setPicked(null)} className="text-xs text-base-400 hover:text-white">
            Change
          </button>
        </div>
      ) : (
        <>
          <input
            value={term}
            onChange={(e) => setTerm(e.target.value)}
            placeholder="Search for the show…"
            aria-label="Search for the show"
            autoFocus
            className="w-full rounded-md border border-base-800 bg-base-900 px-3 py-1.5 text-sm text-base-100 placeholder:text-base-500 focus:border-accent-500 focus:outline-none"
          />
          {results.isPending && q.trim().length >= 3 && <Skeleton className="h-10 w-full" />}
          {results.data && results.data.results.length === 0 && (
            <p className="text-xs text-base-500">Nothing matched.</p>
          )}
          {results.data && results.data.results.length > 0 && (
            <ul className="max-h-56 space-y-0.5 overflow-y-auto scrollbar-thin">
              {results.data.results.map((h) => (
                <li key={h.id}>
                  <button
                    onClick={() => setPicked(h)}
                    className="flex w-full items-center gap-2.5 rounded-md px-2 py-1 text-left hover:bg-base-850"
                  >
                    {h.coverImage?.medium ? (
                      <img src={h.coverImage.medium} alt="" className="h-10 w-7 rounded object-cover" />
                    ) : (
                      <div className="h-10 w-7 rounded bg-base-850" />
                    )}
                    <span className="min-w-0 flex-1 truncate text-sm text-base-100">{name(h)}</span>
                    <span className="shrink-0 text-[11px] text-base-500">
                      {[h.format?.replace('_', ' '), h.seasonYear, h.episodes && `${h.episodes} ep`]
                        .filter(Boolean)
                        .join(' · ')}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </>
      )}

      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1.5 text-xs text-base-400">
          Episode
          <input
            type="number"
            min={1}
            value={episode}
            onChange={(e) => setEpisode(e.target.value)}
            aria-label="Episode number"
            className="w-16 rounded-md border border-base-800 bg-base-900 px-2 py-1 text-sm text-base-100 focus:border-accent-500 focus:outline-none"
          />
        </label>
        <button
          onClick={() => assign.mutate()}
          disabled={!picked || Number(episode) < 1 || assign.isPending}
          className="rounded-md bg-accent-500 px-3 py-1 text-xs font-medium text-white hover:bg-accent-600 disabled:opacity-50"
        >
          {assign.isPending ? 'Saving…' : 'Save'}
        </button>
        {assign.isError && <span className="text-xs text-recap">{(assign.error as Error).message}</span>}
      </div>
    </div>
  )
}

export function LocalFiles() {
  const [unmatchedOnly, setUnmatched] = useState(false)
  const qc = useQueryClient()
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ['localFiles', unmatchedOnly],
    queryFn: () =>
      api.get<Page<LocalFile>>(`/api/local/files?perPage=100${unmatchedOnly ? '&unmatched=true' : ''}`),
  })
  const stats = useQuery({
    queryKey: ['local'],
    queryFn: () => api.get<{ stats: { missing: number } }>('/api/local'),
  })
  const missing = stats.data?.stats.missing ?? 0
  const forget = useMutation({
    mutationFn: () => api.post<{ forgotten: number }>('/api/local/forget'),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['local'] })
      void qc.invalidateQueries({ queryKey: ['localFiles'] })
    },
  })
  const [assigning, setAssigning] = useState<number | null>(null)

  if (isError) return <ErrorState error={error} retry={() => refetch()} />

  return (
    <div className="space-y-4">
      <PageHeader
        title="Local files"
        meta={data && data.items.length > 0 ? `${data.total} files` : undefined}
        actions={
          <>
            {missing > 0 && (
              <Button
                onClick={() => forget.mutate()}
                disabled={forget.isPending}
                title="Files a scan no longer found. Forgetting drops their records; unplug a drive and they stay."
              >
                Forget {missing} missing
              </Button>
            )}
            <FilterToggle on={unmatchedOnly} onChange={setUnmatched}>
              Unmatched only
            </FilterToggle>
            <LinkButton to="/settings?tab=Library">Manage folders</LinkButton>
          </>
        }
      />

      {isPending ? (
        <Skeleton className="h-64 w-full" />
      ) : data.items.length === 0 ? (
        <Empty
          title={unmatchedOnly ? 'Every file is matched' : 'No files scanned'}
          hint={
            unmatchedOnly
              ? 'Nothing needs assigning by hand.'
              : 'Add a folder in settings and run a scan to play anime you already have.'
          }
          icon={<FolderIcon />}
          action={
            unmatchedOnly ? undefined : (
              <LinkButton to="/settings?tab=Library" variant="primary">
                Add a folder
              </LinkButton>
            )
          }
        />
      ) : (
        <>
          <ul className="surface divide-y divide-white/[0.05] overflow-hidden">
            {data.items.map((f) => (
              <li key={f.id} className="p-2.5">
                <div className="flex items-center gap-3">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm text-base-100">{f.path.split(/[\\/]/).pop()}</p>
                    <p className="mt-0.5 truncate text-xs text-base-500">
                      {bytes(f.size)}
                      {f.resolution && ` · ${f.resolution}`}
                      {f.releaseGroup && ` · ${f.releaseGroup}`}
                    </p>
                  </div>
                  {f.animeId ? (
                    <Link
                      to={`/watch/${f.animeId}/${f.episode ?? 1}`}
                      className="shrink-0 rounded-md bg-base-800 px-2.5 py-1 text-xs text-base-100 hover:bg-base-700"
                    >
                      Play ep {f.episode ?? '?'}
                    </Link>
                  ) : (
                    <Badge tone="filler">Unmatched</Badge>
                  )}
                  <button
                    onClick={() => setAssigning(assigning === f.id ? null : f.id)}
                    className="shrink-0 rounded-md px-2.5 py-1 text-xs text-base-300 transition-colors hover:bg-base-800 hover:text-white"
                  >
                    {assigning === f.id ? 'Cancel' : f.animeId ? 'Reassign' : 'Assign'}
                  </button>
                </div>
                {assigning === f.id && (
                  <AssignFile file={f} onDone={() => setAssigning(null)} />
                )}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}
