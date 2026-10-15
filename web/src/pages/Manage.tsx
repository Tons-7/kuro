import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type DownloadFile, type Page } from '../lib/api'
import { retryNote, sortDownloads } from '../lib/downloads'
import { bytes, cx, relativeTime } from '../lib/format'
import {
  refreshDownloads,
  useDownloadQueue,
  useDownloads,
  useNotifications,
  type Download,
  type Notification,
  type Queued,
} from '../lib/queries'
import {
  Badge,
  Button,
  buttonClass,
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
import { ReleasePicker } from '../components/ReleasePicker'

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

// A bookmark, filled once kept: the fill carries the state, so the row needs no second word for it.
function KeepGlyph({ filled }: { filled: boolean }) {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M6 4h12v17l-6-4.5L6 21V4Z" fill={filled ? 'currentColor' : 'none'} {...stroke} />
    </svg>
  )
}

const stroke = { stroke: 'currentColor', strokeWidth: 2, strokeLinecap: 'round', strokeLinejoin: 'round' } as const

function SwapGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M4 8h13m0 0-3-3m3 3-3 3M20 16H7m0 0 3-3m-3 3 3 3" fill="none" {...stroke} />
    </svg>
  )
}

function FrontGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M5 5h14M12 20V9m-5 5 5-5 5 5" fill="none" {...stroke} />
    </svg>
  )
}

function RetryGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5" fill="none" {...stroke} />
    </svg>
  )
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
      refreshDownloads(qc)
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
                aria-label="Keep"
                title={f.kept ? 'Kept. Click to let the cache evict it again.' : 'Keep: never evicted.'}
                className={cx(keepButton(f.kept), 'size-6')}
              >
                <KeepGlyph filled={f.kept} />
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
  const [openShows, setOpenShows] = useState<ReadonlySet<number>>(new Set())
  const toggleShow = (id: number) =>
    setOpenShows((open) => {
      const next = new Set(open)
      if (!next.delete(id)) next.add(id)
      return next
    })

  const { data, isPending, isError, error, refetch } = useDownloads()
  const queue = useDownloadQueue()

  const done = () => {
    setConfirmClear(false)
    refreshDownloads(qc)
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

  // A release chosen for one episode replaces whatever it has or would get.
  const [picking, setPicking] = useState<{ animeId: number; episode: number; current?: string } | null>(null)
  const changeRelease = useMutation({
    mutationFn: (pick: { animeId: number; episode: number; infoHash: string; title: string }) =>
      api.post('/api/download/release', pick),
    onSuccess: done,
  })

  const requeue = useMutation({
    mutationFn: (q: Queued) =>
      api.post('/api/download', { animeId: q.animeId, episode: q.episode }),
    onSuccess: done,
  })

  const now = useMutation({
    mutationFn: (q: Queued) =>
      api.post('/api/download/queue/now', { animeId: q.animeId, epKey: q.epKey }),
    onSuccess: done,
  })

  // The whole queue: pausing one row only lets the next in line start.
  const queuePaused = queue.data?.paused ?? false
  const pauseAll = useMutation({
    mutationFn: (paused: boolean) => api.post(`/api/download/queue/${paused ? 'pause' : 'resume'}`),
    onSuccess: done,
  })

  const remove = useMutation({
    meta: { inline: true },
    mutationFn: (hashes: string[]) => Promise.all(hashes.map((h) => api.del(`/api/downloads/${h}`))),
    // Settled, not succeeded: some of a show's files may be gone even when one delete failed.
    onSettled: () => {
      setConfirmRemove(null)
      done()
    },
  })
  // Deleting takes the files; rows also shift as downloads finish, so the ✕ asks before it acts.
  // Holds a download's hash, or a show's key when the whole group is being deleted.
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null)

  const pause = useMutation({
    mutationFn: (hash: string) => api.post(`/api/downloads/${hash}/pause`),
    onSuccess: done,
  })

  const resume = useMutation({
    mutationFn: (hash: string) => api.post(`/api/downloads/${hash}/resume`),
    onSuccess: done,
  })
  const clear = useMutation({
    meta: { inline: true },
    mutationFn: (scope: string) => api.post(`/api/downloads/clear?scope=${scope}`),
    onSuccess: done,
  })
  // Optimistic, so a click on a slightly stale list sends the right action.
  const keep = useMutation({
    meta: { inline: true },
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

  const items = data?.items ?? []
  const queued = queue.data?.items ?? []
  // Clear never takes a kept download or one playing now.
  const removable = items.filter((d) => !d.pinned && !d.kept).length

  const { active, held, finished, searching, waitingQueue, failed } = sortDownloads(items, queued)
  const waiting = held.length + waitingQueue.length
  const keptBytes = finished.reduce((n, d) => n + (d.kept ? d.bytesOnDisk : 0), 0)
  const cachedBytes = finished.reduce((n, d) => n + (d.kept ? 0 : d.bytesOnDisk), 0)

  const rows: Row[] = [
    ...(active.length + searching.length > 0 ? [{ group: 'Downloading', meta: 'one at a time' }] : []),
    ...searching.map((q) => ({ q })),
    ...active.map((d) => ({ d })),
    ...(waiting > 0 ? [{ group: 'Waiting', meta: String(waiting) }] : []),
    ...held.map((d, i) => ({ d, position: i + 1 })),
    ...waitingQueue.map((q, i) => ({ q, position: held.length + i + 1 })),
    ...(failed.length > 0 ? [{ group: 'Failed' }] : []),
    ...failed.map((q) => ({ q })),
    ...(finished.length > 0
      ? [{ group: 'On disk', meta: `${bytes(cachedBytes)} cached · ${bytes(keptBytes)} downloaded` }]
      : []),
    ...byShow(finished).map((x) => ('items' in x ? { show: x } : { d: x })),
  ]

  const actions: RowActions = {
    // Paused by the queue, not by hand: its section already says it is waiting its turn.
    queueHeld: (d) => d.episodes.some((e) => waitingKeys.has(`${d.animeId}-${e}`)),
    changeRelease: (animeId, episode, current) => setPicking({ animeId, episode, current }),
    chosenFor: (d) => queued.find((q) => q.animeId === d.animeId && d.episodes.includes(q.epKey) && !!q.release),
    confirming: confirmRemove,
    confirm: setConfirmRemove,
    removing: remove.isPending,
    toggling: pause.isPending || resume.isPending,
    keep: (d) => keep.mutate(d),
    remove: (hashes) => remove.mutate(hashes),
    pauseResume: (d) => (d.paused ? resume : pause).mutate(d.infoHash),
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="Downloads"
        meta={
          data
            ? [
                active.length > 0 && `${active.length} downloading`,
                waiting > 0 && `${waiting} waiting`,
                finished.length > 0 && `${finished.length} on disk`,
              ]
                .filter(Boolean)
                .join(' · ')
            : undefined
        }
        actions={
          <>
            {/* Only with something to pause, or already paused: otherwise the button does nothing visible. */}
            {!confirmClear && (queuePaused || active.length + searching.length + waiting > 0) && (
              <Button
                onClick={() => pauseAll.mutate(!queuePaused)}
                disabled={pauseAll.isPending}
                title={
                  queuePaused
                    ? 'Start downloading again, one at a time'
                    : 'Stop every download. Pausing a single one lets the next in line start.'
                }
              >
                {queuePaused ? 'Resume downloads' : 'Pause all'}
              </Button>
            )}
            {removable > 0 &&
              (confirmClear ? (
                <>
                  <span className="self-center text-xs text-base-400">
                    Cached episodes only; downloaded ones stay.
                  </span>
                  <Button onClick={() => clear.mutate('completed')}>Finished</Button>
                  <Button variant="danger" onClick={() => clear.mutate('all')}>
                    All, unfinished too
                  </Button>
                  <Button variant="ghost" onClick={() => setConfirmClear(false)}>
                    Cancel
                  </Button>
                </>
              ) : (
                <Button onClick={() => setConfirmClear(true)}>Clear…</Button>
              ))}
          </>
        }
      />

      {(remove.isError || clear.isError || keep.isError) && (
        <p className="text-xs text-recap">
          {((remove.error ?? clear.error ?? keep.error) as Error)?.message}
        </p>
      )}

      {/* Said outright: a paused queue otherwise looks like downloads that will not start. */}
      {queuePaused && (
        <p role="status" className={cx(card, 'flex items-center justify-between gap-3 px-3 py-2 text-sm text-base-200')}>
          Downloads are paused. Nothing starts until you resume.
          <button
            onClick={() => pauseAll.mutate(false)}
            disabled={pauseAll.isPending}
            className="rounded-md bg-accent-500/15 px-2.5 py-1 text-xs font-medium text-accent-300 hover:bg-accent-500/25"
          >
            Resume
          </button>
        </p>
      )}

      {isPending ? (
        <Skeleton className="h-64 w-full" />
      ) : rows.length === 0 ? (
        <Empty
          title="Nothing downloaded"
          hint="Episodes you watch are cached here, and anything you download stays until you remove it."
          icon={<DownloadIcon />}
          action={<LinkButton to="/recent">Find something to watch</LinkButton>}
        />
      ) : (
        <ul className="space-y-2">
          {rows.map((r) => {
            if ('group' in r) return <Group key={r.group} label={r.group} meta={r.meta} />
            if ('q' in r)
              return (
                <QueueRow
                  key={`q-${r.q.animeId}-${r.q.epKey}`}
                  q={r.q}
                  position={r.position}
                  actions={actions}
                  onNow={() => now.mutate(r.q)}
                  onRetry={() => requeue.mutate(r.q)}
                  onRemove={() => dequeue.mutate(r.q)}
                  onChangeRelease={() => actions.changeRelease(r.q.animeId, r.q.episode, r.q.infoHash)}
                />
              )
            if ('show' in r)
              return (
                <ShowRows
                  key={`show-${r.show.animeId}`}
                  show={r.show}
                  open={openShows.has(r.show.animeId)}
                  onToggle={() => toggleShow(r.show.animeId)}
                  actions={actions}
                />
              )
            return <DownloadRow key={r.d.infoHash} d={r.d} position={r.position} actions={actions} />
          })}
        </ul>
      )}
      {changeRelease.isError && <p className="text-xs text-recap">{(changeRelease.error as Error).message}</p>}

      {picking && (
        <ReleasePicker
          animeId={picking.animeId}
          episode={picking.episode}
          current={picking.current}
          purpose="download"
          onClose={() => setPicking(null)}
          onPick={(infoHash, title) => {
            changeRelease.mutate({ animeId: picking.animeId, episode: picking.episode, infoHash, title })
            setPicking(null)
          }}
        />
      )}
    </div>
  )
}

type Row =
  | { group: string; meta?: string }
  | { d: Download; position?: number }
  | { q: Queued; position?: number }
  | { show: Show }

/** One show's finished downloads, listed together. */
interface Show {
  animeId: number
  title?: string
  cover?: string
  items: Download[]
}

// Grouped by catalogue entry, not by name: a later season is its own entry, so it gets its own group.
// A show with a single download stays a plain row.
function byShow(finished: Download[]): (Download | Show)[] {
  const shows = new Map<number, Download[]>()
  for (const d of finished) {
    if (d.animeId) shows.set(d.animeId, [...(shows.get(d.animeId) ?? []), d])
  }
  const placed = new Set<number>()
  return finished.flatMap((d): (Download | Show)[] => {
    const items = d.animeId ? shows.get(d.animeId)! : [d]
    if (!d.animeId || items.length < 2) return [d]
    if (placed.has(d.animeId)) return []
    placed.add(d.animeId)
    // A special's key ("S1") is not a number; it sorts first.
    const first = (x: Download) => Number(x.episodes[0]) || 0
    return [{ animeId: d.animeId, title: d.title, cover: d.cover, items: [...items].sort((a, b) => first(a) - first(b)) }]
  })
}

interface RowActions {
  queueHeld: (d: Download) => boolean
  /** Open the release picker for one episode, to download another release in its place. */
  changeRelease: (animeId: number, episode: number, current?: string) => void
  /** The release chosen to replace this download, while that is still queued. */
  chosenFor: (d: Download) => Queued | undefined
  /** What the delete confirmation is open for: a download's hash or a show's key. */
  confirming: string | null
  confirm: (key: string | null) => void
  removing: boolean
  toggling: boolean
  keep: (d: Download) => void
  remove: (hashes: string[]) => void
  pauseResume: (d: Download) => void
}

function ConfirmDelete({ size, actions, onDelete }: { size: number; actions: RowActions; onDelete: () => void }) {
  return (
    <span className="flex items-center gap-1 text-xs">
      <button
        onClick={onDelete}
        disabled={actions.removing}
        className="rounded-md bg-recap/80 px-2 py-1 font-medium text-white hover:bg-recap"
      >
        Delete {bytes(size)}
      </button>
      <button
        onClick={() => actions.confirm(null)}
        className="rounded-md px-2 py-1 text-base-400 hover:bg-base-800"
      >
        Cancel
      </button>
    </span>
  )
}

// The rail every row ends with: same-size labelled icons, so columns line up; hover only colours them.
const iconButton =
  'grid size-7 shrink-0 place-items-center rounded-md text-base-400 transition-colors hover:bg-base-800 hover:text-white'

const keepButton = (kept: boolean) =>
  cx(iconButton, kept && 'bg-accent-500/15 text-accent-300 hover:bg-accent-500/25 hover:text-accent-200')

const deleteButton =
  'grid size-7 place-items-center rounded-md text-base-600 transition-colors hover:bg-base-800 hover:text-recap'

// Each download is its own card: rows sharing one surface ran into each other.
const card = 'rounded-lg border border-white/[0.08] bg-base-900/60'

// A show's downloads behind one line: collapsed it answers "what do I have", opened it lists the episodes.
function ShowRows({
  show,
  open,
  onToggle,
  actions,
}: {
  show: Show
  open: boolean
  onToggle: () => void
  actions: RowActions
}) {
  const key = `show-${show.animeId}`
  const episodes = show.items.reduce((n, d) => n + Math.max(d.episodes.length, 1), 0)
  const size = show.items.reduce((n, d) => n + d.bytesOnDisk, 0)
  const kept = show.items.filter((d) => d.kept).length
  const allKept = kept === show.items.length
  // Never the one playing now.
  const removable = show.items.filter((d) => !d.pinned)

  return (
    // One card for the show; its episodes open inside it, so they read as its own.
    <li className={cx(card, 'overflow-hidden')}>
      <div className="flex items-center gap-3 p-3">
        <button
          onClick={onToggle}
          aria-expanded={open}
          aria-label={`${open ? 'Hide' : 'Show'} downloads of ${show.title ?? `Anime ${show.animeId}`}`}
          className="grid size-7 shrink-0 place-items-center rounded-md text-base-500 hover:bg-base-800 hover:text-white"
        >
          <svg viewBox="0 0 24 24" className={cx('size-4 transition-transform', open && 'rotate-90')} aria-hidden>
            <path d="m9 6 6 6-6 6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
        {show.cover && (
          <Link to={`/anime/${show.animeId}`} className="shrink-0">
            <img src={show.cover} alt="" loading="lazy" className="h-14 w-10 rounded object-cover shadow-card" />
          </Link>
        )}
        <button onClick={onToggle} className="min-w-0 flex-1 text-left">
          <p className="truncate text-sm font-medium text-base-100">{show.title ?? `Anime ${show.animeId}`}</p>
          <p className="mt-0.5 text-xs text-base-400">
            {episodes} episodes · {bytes(size)}
          </p>
        </button>
        <div className="flex shrink-0 items-center gap-1.5">
          {show.items.some((d) => d.pinned) && <Badge tone="accent">Playing</Badge>}
          <Badge tone={allKept ? 'success' : 'neutral'}>
            {allKept ? 'Downloaded' : kept === 0 ? 'Cached' : `${kept} of ${show.items.length} kept`}
          </Badge>
          <button
            onClick={() => show.items.filter((d) => d.kept === allKept).forEach(actions.keep)}
            aria-pressed={allKept}
            aria-label="Keep all"
            title={allKept ? 'All kept. Click to let the cache evict these again' : 'Keep every episode: never evicted, outside the cache budget'}
            className={keepButton(allKept)}
          >
            <KeepGlyph filled={allKept} />
          </button>
          {actions.confirming === key ? (
            <ConfirmDelete
              size={removable.reduce((n, d) => n + d.bytesOnDisk, 0)}
              actions={actions}
              onDelete={() => actions.remove(removable.map((d) => d.infoHash))}
            />
          ) : (
            removable.length > 0 && (
              <button
                onClick={() => actions.confirm(key)}
                disabled={actions.removing}
                aria-label={`Delete every download of ${show.title ?? `Anime ${show.animeId}`}`}
                title="Remove and delete all of these files"
                className={deleteButton}
              >
                ✕
              </button>
            )
          )}
        </div>
      </div>
      {open && (
        <ul>
          {show.items.map((d) => (
            <DownloadRow key={d.infoHash} d={d} actions={actions} nested />
          ))}
        </ul>
      )}
    </li>
  )
}

function DownloadRow({
  d,
  position,
  actions,
  nested = false,
}: {
  d: Download
  position?: number
  actions: RowActions
  /** Inside its show's group, which already carries the cover and the title. */
  nested?: boolean
}) {
  const unfinished = d.percent < 100
  const chosen = actions.chosenFor(d)
  const retry = retryNote(d.queued)
  // What follows "episode": "s 1, 2" for a pack.
  const numbers = d.episodes.length > 0 && `${d.episodes.length > 1 ? 's' : ''} ${d.episode}`

  return (
    <li className={cx('p-3', nested ? 'border-t border-white/[0.06] bg-base-950/30' : card)}>
      <div className="flex items-start gap-3">
        {/* The place in line while waiting, the episode inside a group. */}
        <Gutter>{position ?? (nested ? d.episodes[0] : undefined)}</Gutter>
        {d.cover && !nested && (
          <Link to={`/anime/${d.animeId}`} className="shrink-0">
            <img src={d.cover} alt="" loading="lazy" className="h-14 w-10 rounded object-cover shadow-card" />
          </Link>
        )}
        <div className="min-w-0 flex-1">
          {/* The show first: a release filename does not say what you
              downloaded, which is the question this page answers. */}
          {nested ? (
            <p className="truncate text-sm font-medium text-base-100">{numbers ? `Episode${numbers}` : d.name}</p>
          ) : (
            <p className="truncate text-sm font-medium text-base-100">
              {d.title ?? d.name}
              {numbers && <span className="ml-1.5 font-normal text-base-400">episode{numbers}</span>}
            </p>
          )}
          {d.title && (numbers || !nested) && (
            <p className="truncate text-xs text-base-600" title={d.name}>
              {d.name}
            </p>
          )}
          {chosen && (
            <p className="truncate text-xs text-accent-300" title={chosen.release}>
              Changing to: {chosen.release}
            </p>
          )}
          {retry && (
            <p className="truncate text-xs text-amber-300">
              {retry} · {d.queued?.error}
            </p>
          )}
          {/* One line, left to right: how far, how big, how fast, how
              long. Speed and peers separate slow from stalled. */}
          <p className="mt-0.5 text-xs text-base-400">
            {unfinished && <span className="font-medium text-base-100">{Math.round(d.percent)}% · </span>}
            {unfinished ? `${bytes(d.bytesOnDisk)} of ${bytes(d.totalBytes)}` : bytes(d.bytesOnDisk)}
            {!d.paused && unfinished && d.mbps ? ` · ${d.mbps.toFixed(1)} Mbps` : ''}
            {!d.paused && unfinished && d.peers ? ` · ${d.peers} peers` : ''}
            {!d.paused && unfinished && d.mbps ? ` · ${timeLeft(d.totalBytes - d.bytesOnDisk, d.mbps)}` : ''}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {d.pinned && <Badge tone="accent">Playing</Badge>}
          {d.checking && <Badge>Checking file…</Badge>}
          {d.paused && unfinished && !actions.queueHeld(d) && <Badge tone="warning">Paused</Badge>}
          {/* Cached is what watching leaves behind and the sweep may
              take; Downloaded was asked for and stays. */}
          {!unfinished && <Badge tone={d.kept ? 'success' : 'neutral'}>{d.kept ? 'Downloaded' : 'Cached'}</Badge>}

          {/* Pausing keeps the file; removing does not. Finished ones
              have nothing left to pause. */}
          {unfinished && !d.checking && (
            <button
              onClick={() => actions.pauseResume(d)}
              disabled={actions.toggling}
              aria-label={d.paused ? 'Resume download' : 'Pause download'}
              title={d.paused ? 'Resume this one' : 'Pause this one. The next in line starts; "Pause all" stops everything.'}
              className={iconButton}
            >
              {d.paused ? <PlayGlyph /> : <PauseGlyph />}
            </button>
          )}

          {/* One episode, not on screen: another release can take its place. */}
          {!d.pinned && d.animeId && d.episodes.length === 1 && Number(d.episodes[0]) > 0 && (
            <button
              onClick={() => actions.changeRelease(d.animeId!, Number(d.episodes[0]), d.infoHash)}
              aria-label={`Change release for episode ${d.episodes[0]}`}
              title="Change release: pick another one; this copy is replaced by it"
              className={iconButton}
            >
              <SwapGlyph />
            </button>
          )}

          {/* The badge says kept (Downloaded), the fill repeats it; "✓ Kept" as a label was a third time. */}
          <button
            onClick={() => actions.keep(d)}
            aria-pressed={d.kept}
            aria-label="Keep"
            title={
              d.kept
                ? `Kept: never evicted, outside the cache budget. Click to let the cache evict ${
                    d.episodes.length > 1 ? `all ${d.episodes.length} episodes` : 'it'
                  } again.`
                : `Keep${
                    d.episodes.length > 1 ? ` all ${d.episodes.length} episodes` : ''
                  }: never evicted, not counted in the cache budget.`
            }
            className={keepButton(d.kept)}
          >
            <KeepGlyph filled={d.kept} />
          </button>

          {!d.pinned &&
            (actions.confirming === d.infoHash ? (
              <ConfirmDelete size={d.bytesOnDisk} actions={actions} onDelete={() => actions.remove([d.infoHash])} />
            ) : (
              <button
                onClick={() => actions.confirm(d.infoHash)}
                disabled={actions.removing}
                aria-label={`Delete ${d.title ?? d.name}`}
                title="Remove and delete the file"
                className={deleteButton}
              >
                ✕
              </button>
            ))}
        </div>
      </div>
      {unfinished && <ProgressBar value={d.percent} className="mt-2" />}
      {d.episodes.length > 1 && <PackEpisodes hash={d.infoHash} />}
    </li>
  )
}

// The column every row starts with, so posters and titles line up: a square holding a number, or nothing.
function Gutter({ children }: { children?: React.ReactNode }) {
  return (
    <span
      className={cx(
        'grid h-7 min-w-7 shrink-0 place-items-center self-center rounded-md px-1 text-xs tabular-nums text-base-400',
        children != null && 'bg-white/[0.05]',
      )}
    >
      {children}
    </span>
  )
}

function Group({ label, meta }: { label: string; meta?: string }) {
  return (
    <li className="flex items-baseline justify-between gap-3 px-1 pt-3 first:pt-0">
      <span className="text-[11px] font-semibold tracking-wider text-base-400 uppercase">{label}</span>
      {meta && <span className="text-[11px] text-base-600">{meta}</span>}
    </li>
  )
}

// Queued but not started, so nothing to pause or keep; a failed one carries what its torrent got.
function QueueRow({
  q,
  position,
  actions,
  onNow,
  onRetry,
  onRemove,
  onChangeRelease,
}: {
  q: Queued
  position?: number
  actions: RowActions
  onNow: () => void
  onRetry: () => void
  onRemove: () => void
  onChangeRelease: () => void
}) {
  const key = `q-${q.animeId}-${q.epKey}`
  const got = q.torrent
  return (
    <li className={cx(card, 'group p-3')}>
      <div className="flex items-center gap-3">
      <Gutter>{position}</Gutter>
      {q.cover ? (
        <img src={q.cover} alt="" loading="lazy" className="h-14 w-10 shrink-0 rounded object-cover shadow-card" />
      ) : (
        <div className="h-14 w-10 shrink-0 rounded bg-base-850" />
      )}

      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-base-100">
          {q.title ?? `Anime ${q.animeId}`}
          <span className="ml-1.5 font-normal text-base-400">episode {q.episode}</span>
        </p>
        {q.error && q.state === 'failed' && <p className="truncate text-xs text-recap">{q.error}</p>}
        {q.retryAt && (
          <p className="truncate text-xs text-amber-300">
            {retryNote(q)} · {q.error}
          </p>
        )}
        {got ? (
          <p className="truncate text-xs text-base-600" title={got.name}>
            {got.name}
          </p>
        ) : q.release ? (
          <p className="truncate text-xs text-base-600" title={q.release}>
            Chosen: {q.release}
          </p>
        ) : (
          q.state === 'active' && <p className="text-xs text-base-400">Finding a release…</p>
        )}
        {got && (
          <p className="mt-0.5 text-xs text-base-400">
            <span className="font-medium text-base-100">{Math.round(got.percent)}% · </span>
            {bytes(got.bytesOnDisk)} of {bytes(got.totalBytes)}
          </p>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-1.5">
        <button
          onClick={onChangeRelease}
          aria-label={`Change release for episode ${q.episode}`}
          title="Change release: pick the one to download instead of what the search finds"
          className={iconButton}
        >
          <SwapGlyph />
        </button>
        {q.state === 'active' ? null : q.state === 'failed' ? (
          <button onClick={onRetry} aria-label="Try again" title="Try again" className={iconButton}>
            <RetryGlyph />
          </button>
        ) : (
          <button
            onClick={onNow}
            aria-label="Download now"
            title="Download now. The one downloading goes back to wait behind it."
            className={iconButton}
          >
            <FrontGlyph />
          </button>
        )}
        {/* With a torrent behind it, removing takes the bytes too, so it asks first like any delete. */}
        {got && actions.confirming === key ? (
          <ConfirmDelete
            size={got.bytesOnDisk}
            actions={actions}
            onDelete={() => {
              onRemove()
              actions.remove([got.infoHash])
            }}
          />
        ) : (
          <button
            onClick={got ? () => actions.confirm(key) : onRemove}
            aria-label="Remove from queue"
            title={got ? 'Remove from the queue and delete what it got' : 'Remove from the queue'}
            className={deleteButton}
          >
            ✕
          </button>
        )}
      </div>
      </div>
      {got && <ProgressBar value={got.percent} className="mt-2" />}
    </li>
  )
}

// Remaining bytes over the speed the engine reports; Mbps is megabits.
function timeLeft(remaining: number, mbps: number): string {
  const seconds = remaining / ((mbps * 1_000_000) / 8)
  if (!isFinite(seconds) || seconds <= 0) return ''
  if (seconds < 60) return 'under a minute left'
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min left`
  const hours = Math.floor(minutes / 60)
  return `${hours} h ${minutes % 60} min left`
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
    queryFn: ({ signal }) =>
      api.get<{ results: SearchHit[] }>(`/api/search?q=${encodeURIComponent(q)}&limit=8`, signal),
    staleTime: 60_000,
  })

  const assign = useMutation({
    meta: { inline: true },
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
          className={buttonClass('primary', 'sm')}
        >
          {assign.isPending ? 'Saving…' : 'Save'}
        </button>
        {assign.isError && <span className="text-xs text-recap">{(assign.error as Error).message}</span>}
      </div>
    </div>
  )
}

export function LocalFiles() {
  const [unmatchedOnly, setUnmatchedOnly] = useState(false)
  const [page, setPage] = useState(1)
  const setUnmatched = (v: boolean) => {
    setUnmatchedOnly(v)
    setPage(1)
  }
  const qc = useQueryClient()
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ['localFiles', unmatchedOnly, page],
    queryFn: () =>
      api.get<Page<LocalFile>>(
        `/api/local/files?perPage=100&page=${page}${unmatchedOnly ? '&unmatched=true' : ''}`,
      ),
    placeholderData: (prev) => prev,
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
          {(page > 1 || data.hasMore) && (
            <div className="flex items-center justify-center gap-2 pt-2">
              <Button disabled={page <= 1} onClick={() => setPage(page - 1)}>
                Previous
              </Button>
              <span className="text-sm text-base-400">Page {page}</span>
              <Button disabled={!data.hasMore} onClick={() => setPage(page + 1)}>
                Next
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
