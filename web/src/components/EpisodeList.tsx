import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { api, hasFiller, type Episode } from '../lib/api'
import { clockTime, cx } from '../lib/format'
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
function isUnaired(episode: Episode) {
  return !!episode.airDate && episode.airDate * 1000 > Date.now()
}

function airsOn(unix?: number) {
  if (!unix) return ''
  return new Date(unix * 1000).toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
  })
}

export function EpisodeList({
  animeId,
  episodes,
  progress,
  current,
  compact,
  cover,
  selectable,
  upNext,
}: {
  animeId: number
  episodes: Episode[]
  progress: number
  current?: number
  compact?: boolean
  cover?: string | null
  /** Offer a select mode that queues a hand-picked set of episodes. */
  selectable?: boolean
  /** The episode the page's main button would play. */
  upNext?: number
}) {
  const [term, setTerm] = useState('')
  const [page, setPage] = useState(0)

  const [selecting, setSelecting] = useState(false)
  const [picked, setPicked] = useState<Set<number>>(() => new Set())
  const [note, setNote] = useState<string | null>(null)
  const qc = useQueryClient()
  const download = useMutation({
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

  const filtered = useMemo(() => {
    const q = term.trim().toLowerCase()
    if (!q) return aired
    // A bare number should find that episode, not every title containing it.
    const asNumber = Number(q)
    if (Number.isInteger(asNumber) && asNumber > 0) {
      return aired.filter((e) => String(e.number).includes(q))
    }
    return aired.filter((e) =>
      `${e.titleEn ?? ''} ${e.titleJa ?? ''} ${e.overview ?? ''}`.toLowerCase().includes(q),
    )
  }, [aired, term])

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
  }, [term])

  const searchable = episodes.length > 12

  // Nothing is known about these beyond how many there are supposed to be, and
  // a row that looks like every other row does not say so.
  const planned = episodes.length > 0 && episodes.every((e) => e.planned)

  return (
    <div className="space-y-2">
      {planned && (
        <p className="rounded-md border border-base-850 bg-base-900/50 px-3 py-2 text-xs text-base-500">
          No episode data for this show — the numbering comes from its episode
          count, so an episode may not exist or may be numbered differently by
          the release. Releases are being searched for; anything found stops
          being marked expected.
        </p>
      )}

      {searchable && (
        <input
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          placeholder={`Search ${episodes.length} episodes…`}
          aria-label="Search episodes"
          className="w-full rounded-md border border-base-800 bg-base-900 px-3 py-1.5 text-sm text-base-100 placeholder:text-base-500 focus:border-accent-500 focus:outline-none"
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
                  className="rounded-md bg-white px-3 py-1 text-xs font-semibold text-base-950 transition-colors hover:bg-base-100 disabled:opacity-40"
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
          'surface divide-y divide-white/[0.05] overflow-hidden',
          compact && 'max-h-[70vh] overflow-y-auto scrollbar-thin',
        )}
      >
        {paged.map((episode) => (
          <EpisodeRow
            key={episode.number}
            animeId={animeId}
            episode={episode}
            watched={episode.watched || episode.number <= progress}
            active={episode.number === current}
            upNext={episode.number === upNext}
            compact={compact}
            cover={cover}
            selected={picked.has(episode.number)}
            onToggle={selecting ? () => toggle(episode.number) : undefined}
          />
        ))}
        {paged.length === 0 && (
          <li className="p-6 text-center text-sm text-base-500">No episode matches that.</li>
        )}
      </ul>

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
        className="flex min-w-36 items-center justify-center gap-1.5 rounded-md px-2 py-1 text-xs tabular-nums text-base-300 transition-colors hover:bg-base-850 hover:text-white"
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
      className="rounded-md bg-base-850 px-2.5 py-1 text-xs text-base-200 transition-colors hover:bg-base-750 disabled:opacity-40"
    >
      {label}
    </button>
  )
}

function EpisodeRow({
  animeId,
  episode,
  watched,
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

  const qc = useQueryClient()
  const mark = useMutation({
    mutationFn: (next: boolean) =>
      api.post('/api/watched', { animeId, episode: episode.number, watched: next }),
    onSuccess: () => {
      for (const key of ['episodes', 'anime', 'continue', 'library', 'history']) {
        void qc.invalidateQueries({ queryKey: [key] })
      }
    },
  })

  const body = (
    <>
      <span
        className={cx(
          'w-8 shrink-0 text-center text-sm font-medium tabular-nums',
          active || upNext
            ? 'text-accent-400'
            : unaired
              ? 'text-base-600'
              : watched
                ? 'text-base-500'
                : 'text-base-300',
        )}
      >
        {episode.display || episode.number}
      </span>

      {/* Always the same box, still or not: a recent episode often has none for
          days, and dropping the image reflows every row. Cover stands in, dimmed. */}
      {!compact && (
        <div className="h-12 w-20 shrink-0 overflow-hidden rounded bg-base-850">
          {(episode.still ?? cover) && (
            <img
              src={episode.still ?? cover ?? undefined}
              alt=""
              loading="lazy"
              className={cx('size-full object-cover', !episode.still && 'opacity-30 blur-[1px]')}
            />
          )}
        </div>
      )}

      <div className="min-w-0 flex-1">
        <p
          className={cx(
            'line-clamp-1 text-sm',
            unaired ? 'text-base-500' : watched && !active ? 'text-base-400' : 'text-base-100',
          )}
        >
          {episode.titleEn || episode.titleJa || `Episode ${episode.display || episode.number}`}
        </p>
        {!compact && episode.overview && (
          <p className="mt-0.5 line-clamp-1 text-xs text-base-500">{episode.overview}</p>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-1.5">
        {upNext && !active && !unaired && <Badge tone="accent">Up next</Badge>}
        <EpisodeKind episode={episode} />
        {unaired ? (
          <Badge>Airs {airsOn(episode.airDate)}</Badge>
        ) : (
          <>
            {/* Nothing has been found for this one, so it is a number from the
                episode count rather than an episode known to exist. */}
            {episode.planned && !episode.onDisk && <Badge>Expected</Badge>}
            {episode.runtime ? (
              <span className="text-[11px] text-base-500">{episode.runtime}m</span>
            ) : null}
          </>
        )}
      </div>
    </>
  )

  // Outside the link, or the tick would open the player.
  const tick = !unaired && !onToggle && (
    <button
      type="button"
      onClick={() => mark.mutate(!watched)}
      disabled={mark.isPending}
      aria-label={watched ? 'Mark not watched' : 'Mark watched'}
      title={watched ? 'Mark not watched' : 'Mark watched'}
      className={cx(
        'grid size-7 shrink-0 place-items-center rounded-md transition-colors hover:bg-base-800',
        watched ? 'text-accent-400' : 'text-base-700 hover:text-base-300',
      )}
    >
      <WatchedTick />
    </button>
  )

  return (
    <li ref={row}>
      {unaired ? (
        <div className="flex cursor-default items-center gap-3 p-2.5 opacity-70">{body}</div>
      ) : onToggle ? (
        <button
          type="button"
          onClick={onToggle}
          aria-pressed={selected}
          className={cx(
            'flex w-full items-center gap-3 p-2.5 text-left transition-colors',
            selected ? 'bg-accent-500/10' : 'hover:bg-base-900',
          )}
        >
          <span
            aria-hidden
            className={cx(
              'grid size-4 shrink-0 place-items-center rounded border text-[10px] text-white',
              selected ? 'border-accent-400 bg-accent-500' : 'border-base-600',
            )}
          >
            {selected && '✓'}
          </span>
          {body}
        </button>
      ) : (
        <div className={cx('flex items-center pr-2', active ? 'bg-accent-500/10' : 'hover:bg-base-900')}>
          <Link
            to={`/watch/${animeId}/${episode.number}`}
            aria-current={active ? 'true' : undefined}
            className="flex min-w-0 flex-1 items-center gap-3 p-2.5 transition-colors"
          >
            {body}
          </Link>
          {tick}
        </div>
      )}

      {/* By the server's rule, not the tick: an episode watched to the end and
          wound back to its middle is both watched and resumable. */}
      {episode.resumable && typeof episode.position === 'number' && !unaired && (
        <div className="px-2.5 pb-2 text-[11px] text-base-500">
          Resume at {clockTime(episode.position)}
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
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
