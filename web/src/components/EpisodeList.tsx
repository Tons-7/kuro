import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { api, hasFiller, type Episode } from '../lib/api'
import { clockTime, cx, relativeTime } from '../lib/format'
import { Badge, useDismiss } from './ui'

const PAGE_SIZE = 50

/**
 * Only filler and recap get a badge; marking canon too would bury the two rows
 * that matter. An episode can be both.
 */
export function EpisodeKind({ episode }: { episode: Episode }) {
  const filler = hasFiller(episode)
  if (!filler && !episode.recap) return null

  return (
    <span className="flex gap-1">
      {filler && <Badge tone="filler">{episode.filler === 'mixed' ? 'Mixed' : 'Filler'}</Badge>}
      {episode.recap && <Badge tone="recap">Recap</Badge>}
    </span>
  )
}

// An episode with an air date in the future has no release to find, so it is
// shown as scheduled rather than offered as something to play or download.
export function isUnaired(episode: Episode) {
  return !!episode.airDate && episode.airDate * 1000 > Date.now()
}

function airDay(unix?: number) {
  if (!unix) return ''
  return new Date(unix * 1000).toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  })
}

type Show = 'all' | 'unwatched' | 'downloaded'

export function EpisodeList({
  animeId,
  episodes,
  current,
  compact,
  cover,
  selectable,
  upNext,
}: {
  animeId: number
  episodes: Episode[]
  current?: number
  compact?: boolean
  cover?: string | null
  /** Offer a select mode that queues a hand-picked set of episodes. */
  selectable?: boolean
  /** The episode the page's main button would play. */
  upNext?: number
}) {
  const [term, setTerm] = useState('')
  const [show, setShow] = useState<Show>('all')
  const [page, setPage] = useState(0)

  const [selecting, setSelecting] = useState(false)
  const [picked, setPicked] = useState<Set<number>>(() => new Set())
  const [note, setNote] = useState<string | null>(null)
  const qc = useQueryClient()
  const download = useMutation({
    meta: { inline: true },
    mutationFn: (numbers: number[]) =>
      api.post<{ queued: number }>('/api/download/episodes', { animeId, episodes: numbers }),
    onSuccess: (res, numbers) => {
      void qc.invalidateQueries({ queryKey: ['download-queue'] })
      setNote(
        res.queued === 0
          ? 'Already queued or downloaded'
          : `Queued ${res.queued} of ${numbers.length} episode${numbers.length === 1 ? '' : 's'}`,
      )
      stopSelecting()
    },
  })
  const stopSelecting = () => {
    setSelecting(false)
    setPicked(new Set())
  }
  const toggle = (number: number) =>
    setPicked((prev) => {
      const next = new Set(prev)
      if (next.has(number)) next.delete(number)
      else next.add(number)
      return next
    })

  // Only the next unaired episode is worth a row: it answers "when is the new
  // one", which the rest of the schedule does not.
  const aired = useMemo(() => {
    const now = Date.now() / 1000
    const out: Episode[] = []
    let shownUpcoming = false
    for (const e of episodes) {
      const upcoming = !!e.airDate && e.airDate > now
      if (upcoming) {
        if (shownUpcoming) continue
        shownUpcoming = true
      }
      out.push(e)
    }
    return out
  }, [episodes])

  const released = aired.filter((e) => !isUnaired(e))
  const watchedCount = released.filter((e) => e.watched).length
  const downloadedCount = released.filter((e) => e.onDisk).length

  const filtered = useMemo(() => {
    let list = aired
    if (show === 'unwatched') list = list.filter((e) => !e.watched)
    if (show === 'downloaded') list = list.filter((e) => e.onDisk)
    const q = term.trim().toLowerCase()
    if (!q) return list
    // A bare number should find that episode, not every title containing it.
    const asNumber = Number(q)
    if (Number.isInteger(asNumber) && asNumber > 0) {
      return list.filter((e) => String(e.display || e.number).includes(q))
    }
    return list.filter((e) =>
      `${e.titleEn ?? ''} ${e.titleJa ?? ''} ${e.overview ?? ''}`.toLowerCase().includes(q),
    )
  }, [aired, term, show])

  // Ticking N marks everything before it too; the first unwatched one says
  // how far back that reaches.
  const firstUnwatched = useMemo(() => aired.find((e) => !e.watched && !isUnaired(e)), [aired])

  const pages = Math.ceil(filtered.length / PAGE_SIZE)
  const paged = pages > 1 ? filtered.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE) : filtered

  // Open on the page holding the episode being watched, not on the first. Once
  // per episode: a refetch rebuilds `filtered` and would undo manual paging.
  const placed = useRef<number | null>(null)
  useEffect(() => {
    if (!current || placed.current === current) return
    const index = filtered.findIndex((e) => e.number === current)
    if (index < 0) return
    placed.current = current
    setPage(Math.floor(index / PAGE_SIZE))
  }, [current, filtered])

  // Not on mount: it would run after the effect above and undo it.
  const typed = useRef(false)
  useEffect(() => {
    if (typed.current) setPage(0)
    typed.current = true
  }, [term, show])

  const searchable = episodes.length > 12

  // Nothing is known about these beyond how many there are supposed to be, and
  // a row that looks like every other row does not say so.
  const planned = episodes.length > 0 && episodes.every((e) => e.planned)

  return (
    <div className="space-y-3">
      {planned && (
        <p className="rounded-xl border border-base-800 bg-base-900/50 px-3 py-2 text-xs text-base-400">
          No episode data for this show — the numbering comes from its episode
          count, so an episode may not exist or may be numbered differently by
          the release. Releases are being searched for; anything found stops
          being marked expected.
        </p>
      )}

      {!compact && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          {/* Where you are in the show, before any row. */}
          <div className="min-w-40 flex-1">
            <p className="text-sm text-base-300">
              <span className="font-semibold text-white tabular-nums">{watchedCount}</span>
              <span className="text-base-500"> / {released.length} watched</span>
              {downloadedCount > 0 && (
                <span className="text-base-500"> · {downloadedCount} downloaded</span>
              )}
            </p>
            <div className="mt-1.5 h-1 max-w-64 overflow-hidden rounded-full bg-base-800">
              <div
                className="h-full rounded-full bg-gradient-to-r from-accent-500 to-accent-400 transition-[width] duration-500"
                style={{ width: `${released.length ? (watchedCount / released.length) * 100 : 0}%` }}
              />
            </div>
          </div>

          <Filters
            value={show}
            onChange={setShow}
            options={[
              { value: 'all', label: 'All' },
              { value: 'unwatched', label: 'Unwatched', hidden: watchedCount === 0 },
              { value: 'downloaded', label: 'Downloaded', hidden: downloadedCount === 0 },
            ]}
          />

          {searchable && (
            <div className="relative w-full sm:w-56">
              <SearchGlyph />
              <input
                value={term}
                onChange={(e) => setTerm(e.target.value)}
                placeholder={`Find an episode`}
                aria-label="Search episodes"
                className="w-full rounded-lg border border-base-800 bg-base-900 py-1.5 pr-3 pl-8 text-sm text-base-100 placeholder:text-base-500 focus:border-accent-500 focus:outline-none"
              />
            </div>
          )}
        </div>
      )}

      {compact && searchable && (
        <input
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          placeholder={`Search ${episodes.length} episodes…`}
          aria-label="Search episodes"
          className="w-full rounded-lg border border-base-800 bg-base-900 px-3 py-1.5 text-sm text-base-100 placeholder:text-base-500 focus:border-accent-500 focus:outline-none"
        />
      )}

      {selectable && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          {selecting ? (
            <>
              <span className="text-xs tabular-nums text-base-400">{picked.size} selected</span>
              <div className="flex items-center gap-1.5">
                <PageStep
                  label="Select page"
                  disabled={false}
                  onClick={() =>
                    setPicked((prev) => {
                      const next = new Set(prev)
                      for (const e of paged) if (!isUnaired(e)) next.add(e.number)
                      return next
                    })
                  }
                />
                <PageStep label="Cancel" disabled={download.isPending} onClick={stopSelecting} />
                <button
                  onClick={() => download.mutate([...picked].sort((a, b) => a - b))}
                  disabled={picked.size === 0 || download.isPending}
                  className="rounded-lg bg-accent-500 px-3 py-1 text-xs font-semibold text-white transition-colors hover:bg-accent-600 disabled:opacity-40"
                >
                  {download.isPending ? 'Queuing…' : 'Download selected'}
                </button>
              </div>
            </>
          ) : (
            <>
              <span className="text-xs text-base-500">{note}</span>
              <PageStep
                label="Select episodes…"
                disabled={false}
                onClick={() => {
                  setNote(null)
                  setSelecting(true)
                }}
              />
            </>
          )}
          {download.isError && (
            <p className="w-full text-xs text-recap">{(download.error as Error).message}</p>
          )}
        </div>
      )}

      {/* Above the list and grouped in the middle: paging from the bottom
          means scrolling the whole page to move one page. */}
      {pages > 1 && (
        <div className="flex items-center justify-center gap-1.5">
          <PageStep
            label="Previous"
            disabled={page === 0}
            onClick={() => setPage((p) => p - 1)}
          />
          {/* A thousand-episode show is twenty pages; stepping to the end one
              at a time is twenty clicks, so the range is a menu. */}
          <RangeMenu
            page={page}
            pages={pages}
            total={filtered.length}
            rangeFor={(n) => {
              const slice = filtered.slice(n * PAGE_SIZE, (n + 1) * PAGE_SIZE)
              const first = slice[0]
              const last = slice[slice.length - 1]
              return first && last ? `${first.display} – ${last.display}` : ''
            }}
            onPick={setPage}
          />
          <PageStep
            label="Next"
            disabled={page >= pages - 1}
            onClick={() => setPage((p) => p + 1)}
          />
        </div>
      )}

      <ul
        className={cx(
          compact
            ? 'surface max-h-[70vh] divide-y divide-white/[0.04] overflow-y-auto scrollbar-thin'
            : 'space-y-1.5',
        )}
      >
        {paged.map((episode) => (
          <EpisodeRow
            key={episode.number}
            animeId={animeId}
            episode={episode}
            watched={episode.watched}
            marksFrom={
              firstUnwatched && firstUnwatched.number < episode.number
                ? firstUnwatched.display || firstUnwatched.number
                : undefined
            }
            active={episode.number === current}
            upNext={episode.number === upNext}
            compact={compact}
            cover={cover}
            selected={picked.has(episode.number)}
            onToggle={selecting ? () => toggle(episode.number) : undefined}
          />
        ))}
        {paged.length === 0 && (
          <li className="rounded-xl p-6 text-center text-sm text-base-500">
            {show === 'unwatched' ? 'All caught up.' : show === 'downloaded' ? 'Nothing downloaded yet.' : 'No episode matches that.'}
          </li>
        )}
      </ul>
    </div>
  )
}

function Filters({
  value,
  onChange,
  options,
}: {
  value: Show
  onChange: (v: Show) => void
  options: { value: Show; label: string; hidden?: boolean }[]
}) {
  const shown = options.filter((o) => !o.hidden)
  if (shown.length < 2) return null
  return (
    <div role="tablist" aria-label="Show episodes" className="flex gap-1 rounded-lg bg-base-900 p-1">
      {shown.map((o) => (
        <button
          key={o.value}
          role="tab"
          aria-selected={value === o.value}
          onClick={() => onChange(o.value)}
          className={cx(
            'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
            value === o.value ? 'bg-base-750 text-white shadow-card' : 'text-base-400 hover:text-base-100',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

function RangeMenu({
  page,
  pages,
  total,
  rangeFor,
  onPick,
}: {
  page: number
  pages: number
  total: number
  rangeFor: (page: number) => string
  onPick: (page: number) => void
}) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)
  const trigger = useRef<HTMLButtonElement>(null)

  const box = trigger.current?.getBoundingClientRect()

  return (
    <div className="relative" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex min-w-36 items-center justify-center gap-1.5 rounded-lg px-2 py-1 text-xs tabular-nums text-base-300 transition-colors hover:bg-base-850 hover:text-white"
      >
        {rangeFor(page)}
        <span className="text-base-600">of {total}</span>
        <svg viewBox="0 0 24 24" className="size-3 opacity-70" aria-hidden>
          <path d="m6 9 6 6 6-6" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
      </button>

      {open && box &&
        createPortal(
          <div
            role="listbox"
            data-portal-menu
            style={{
              top: Math.min(box.bottom + 6, window.innerHeight - 300),
              left: Math.max(8, Math.min(box.left, window.innerWidth - 184)),
            }}
            className="fixed z-50 max-h-72 w-44 animate-rise overflow-y-auto rounded-xl border border-base-750 bg-base-850 p-1 shadow-panel scrollbar-thin"
          >
            {Array.from({ length: pages }, (_, n) => (
              <button
                key={n}
                role="option"
                aria-selected={n === page}
                onClick={() => {
                  onPick(n)
                  setOpen(false)
                }}
                className={cx(
                  'w-full rounded-lg px-2.5 py-1.5 text-left text-sm tabular-nums transition-colors',
                  n === page ? 'bg-accent-500/15 text-accent-200' : 'text-base-200 hover:bg-base-800',
                )}
              >
                {rangeFor(n)}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </div>
  )
}

function PageStep({
  label,
  disabled,
  onClick,
}: {
  label: string
  disabled: boolean
  onClick: () => void
}) {
  return (
    <button
      disabled={disabled}
      onClick={onClick}
      className="rounded-lg bg-base-850 px-2.5 py-1 text-xs text-base-200 transition-colors hover:bg-base-750 disabled:opacity-40"
    >
      {label}
    </button>
  )
}

function EpisodeRow({
  animeId,
  episode,
  watched,
  marksFrom,
  active,
  upNext,
  compact,
  cover,
  selected,
  onToggle,
}: {
  animeId: number
  episode: Episode
  watched: boolean
  /** Ticking this one also marks every episode from here. */
  marksFrom?: number
  active: boolean
  upNext?: boolean
  compact?: boolean
  cover?: string | null
  selected?: boolean
  /** In select mode a row toggles instead of opening the player. */
  onToggle?: () => void
}) {
  const row = useRef<HTMLLIElement>(null)

  // The sidebar list scrolls, so the watched episode must be on screen on open.
  // Scroll the nearest scrollable ancestor after a frame, not scrollIntoView
  // (which drags the whole page).
  useEffect(() => {
    if (!active || !compact) return

    const id = requestAnimationFrame(() => {
      const el = row.current
      const list = el?.closest('ul')
      if (!el || !list) return
      // Relative to the list's box: offsetTop is from the nearest positioned
      // ancestor (the page here), so scrolling by that lands at the bottom.
      const delta = el.getBoundingClientRect().top - list.getBoundingClientRect().top
      list.scrollTop += delta - list.clientHeight / 2 + el.clientHeight / 2
    })
    return () => cancelAnimationFrame(id)
  }, [active, compact])

  const unaired = isUnaired(episode)
  const number = episode.display || episode.number
  const title = episode.titleEn || episode.titleJa || `Episode ${number}`
  // By the server's rule, not the tick: an episode watched to the end and
  // wound back to its middle is both watched and resumable.
  const resumeAt = episode.resumable && typeof episode.position === 'number' && !unaired ? episode.position : undefined
  const played =
    resumeAt !== undefined && episode.duration ? Math.min(100, (resumeAt / episode.duration) * 100) : watched ? 100 : 0

  const qc = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const mark = useMutation({
    mutationFn: (next: boolean) =>
      api.post('/api/watched', { animeId, episode: episode.number, watched: next }),
    // Settled, not success: a failure may still have changed something.
    onSettled: () => {
      setConfirming(false)
      // Franchise too: marking the last one completes the show.
      for (const key of ['episodes', 'anime', 'continue', 'library', 'history', 'franchise']) {
        void qc.invalidateQueries({ queryKey: [key] })
      }
    },
  })

  // Always the same box, still or not: a recent episode often has none for
  // days, and dropping the image reflows every row. Cover stands in, dimmed.
  const thumb = (
    <div
      className={cx(
        'relative shrink-0 overflow-hidden rounded-lg bg-base-850',
        compact ? 'aspect-video w-24' : 'aspect-video w-28 sm:w-40',
      )}
    >
      {(episode.still ?? cover) && (
        <img
          src={episode.still ?? cover ?? undefined}
          alt=""
          loading="lazy"
          className={cx(
            'size-full object-cover transition-[transform,filter] duration-300 group-hover/ep:scale-105',
            !episode.still && 'opacity-30 blur-[1px]',
            // Watched and not the one playing: set back, not hidden.
            watched && !active && 'brightness-[0.55] saturate-50',
          )}
        />
      )}
      <span className="absolute top-1 left-1 rounded-md bg-base-950/80 px-1.5 py-0.5 text-[10px] font-bold tracking-wide text-white tabular-nums backdrop-blur-sm">
        {compact ? number : `EP ${number}`}
      </span>
      {watched && !active && (
        <span className="absolute inset-0 grid place-items-center">
          <span className="grid size-7 place-items-center rounded-full bg-accent-500/90 text-white shadow-lg">
            <WatchedTick />
          </span>
        </span>
      )}
      {!unaired && !onToggle && (
        <span className="absolute inset-0 grid place-items-center bg-base-950/40 opacity-0 transition-opacity duration-200 group-hover/ep:opacity-100">
          <span className="grid size-9 place-items-center rounded-full bg-white/95 text-base-950 shadow-lg">
            <PlayGlyph />
          </span>
        </span>
      )}
      {played > 0 && played < 100 && (
        <span className="absolute inset-x-0 bottom-0 h-1 bg-black/60">
          <span className="block h-full bg-accent-400" style={{ width: `${played}%` }} />
        </span>
      )}
    </div>
  )

  const text = (
    <div className="min-w-0 flex-1">
      <div className="flex flex-wrap items-center gap-1.5">
        {upNext && !active && !unaired && (
          <span className="rounded-md bg-accent-500 px-1.5 py-0.5 text-[10px] font-bold tracking-wide text-white uppercase">
            Up next
          </span>
        )}
        {active && (
          <span className="rounded-md bg-accent-500/20 px-1.5 py-0.5 text-[10px] font-bold tracking-wide text-accent-200 uppercase">
            Playing
          </span>
        )}
        <p
          className={cx(
            'min-w-0 line-clamp-1 text-sm font-medium',
            unaired ? 'text-base-400' : watched && !active ? 'text-base-400' : 'text-white',
          )}
        >
          {title}
        </p>
      </div>
      {!compact && episode.overview && (
        // Not on a phone: squeezed beside the still it came out as a few words.
        <p className="mt-1 line-clamp-2 hidden text-xs leading-relaxed text-base-500 sm:block">{episode.overview}</p>
      )}
      <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-base-500">
        {unaired ? (
          <span className="font-medium text-sky-300">Airs {relativeTime(episode.airDate!)}</span>
        ) : (
          <>
            {resumeAt !== undefined && (
              <span className="font-medium text-accent-300">Resume {clockTime(resumeAt)}</span>
            )}
            {episode.runtime ? <span>{episode.runtime} min</span> : null}
            {!compact && episode.airDate ? <span>{airDay(episode.airDate)}</span> : null}
            {episode.onDisk && (
              <span className="flex items-center gap-1 text-emerald-300" title="On this machine">
                <DiskGlyph />
                {compact ? '' : 'Downloaded'}
              </span>
            )}
            {/* Nothing has been found for this one, so it is a number from the
                episode count rather than an episode known to exist. */}
            {episode.planned && !episode.onDisk && <Badge>Expected</Badge>}
          </>
        )}
        <EpisodeKind episode={episode} />
      </div>
    </div>
  )

  // Outside the link, or the tick would open the player. Marking past a gap
  // marks the whole gap, and auto-delete can then remove those files, so a
  // jump asks first.
  const tick =
    !unaired &&
    !onToggle &&
    (confirming ? (
      <span className="flex shrink-0 items-center gap-1 text-xs">
        <button
          type="button"
          onClick={() => mark.mutate(true)}
          disabled={mark.isPending}
          className="rounded-md bg-accent-500/20 px-2 py-1 text-accent-200 hover:bg-accent-500/30"
        >
          Mark {marksFrom}–{number}
        </button>
        <button
          type="button"
          onClick={() => setConfirming(false)}
          className="rounded-md px-2 py-1 text-base-400 hover:bg-base-800"
        >
          Cancel
        </button>
      </span>
    ) : (
      <button
        type="button"
        onClick={() => (!watched && marksFrom !== undefined ? setConfirming(true) : mark.mutate(!watched))}
        disabled={mark.isPending}
        aria-label={watched ? 'Mark not watched' : 'Mark watched'}
        title={mark.isError ? (mark.error as Error).message : watched ? 'Mark not watched' : 'Mark watched'}
        className={cx(
          'grid size-8 shrink-0 place-items-center rounded-full transition-colors',
          mark.isError
            ? 'text-recap'
            : watched
              ? 'bg-accent-500/15 text-accent-300 hover:bg-accent-500/25'
              : 'text-base-500 ring-1 ring-base-700 hover:bg-base-800 hover:text-base-100',
        )}
      >
        <WatchedTick />
      </button>
    ))

  const shell = cx(
    'group/ep flex items-center gap-3 transition-colors',
    compact ? 'p-2' : 'rounded-xl p-2 sm:p-2.5',
    active
      ? 'bg-accent-500/10 ring-1 ring-accent-500/40'
      : upNext && !compact
        ? 'bg-base-900/80 ring-1 ring-accent-500/30'
        : compact
          ? 'hover:bg-base-850'
          : 'bg-base-900/40 hover:bg-base-900',
  )

  return (
    <li ref={row}>
      {unaired ? (
        <div className={cx(shell, 'cursor-default opacity-75')}>
          {thumb}
          {text}
        </div>
      ) : onToggle ? (
        <button
          type="button"
          onClick={onToggle}
          aria-pressed={selected}
          className={cx(shell, 'w-full text-left', selected && 'bg-accent-500/10 ring-1 ring-accent-500/40')}
        >
          <span
            aria-hidden
            className={cx(
              'grid size-5 shrink-0 place-items-center rounded-md border text-[11px] text-white',
              selected ? 'border-accent-400 bg-accent-500' : 'border-base-600',
            )}
          >
            {selected && '✓'}
          </span>
          {thumb}
          {text}
        </button>
      ) : (
        <div className={cx(shell, 'pr-2 sm:pr-3')}>
          <Link
            to={`/watch/${animeId}/${episode.number}`}
            aria-current={active ? 'true' : undefined}
            aria-label={`Episode ${number}: ${title}`}
            className="flex min-w-0 flex-1 items-center gap-3"
          >
            {thumb}
            {text}
          </Link>
          {tick}
        </div>
      )}
    </li>
  )
}

function WatchedTick() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path
        d="m5 13 4 4L19 7"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function PlayGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-4 translate-x-px" aria-hidden>
      <path d="M8 5.5v13l11-6.5-11-6.5Z" fill="currentColor" />
    </svg>
  )
}

function DiskGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-3" fill="none" stroke="currentColor" strokeWidth="2.2" aria-hidden>
      <path d="M12 4v11m0 0-4-4m4 4 4-4M5 20h14" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function SearchGlyph() {
  return (
    <svg
      viewBox="0 0 24 24"
      className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-base-500"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      aria-hidden
    >
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" strokeLinecap="round" />
    </svg>
  )
}
