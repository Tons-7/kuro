import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { clockTime, cx, relativeTime } from '../lib/format'
import {
  Badge,
  Button,
  Empty,
  ErrorState,
  LinkButton,
  PageHeader,
  ProgressBar,
  Skeleton,
} from '../components/ui'

interface HistoryEntry {
  animeId: number
  title: string
  cover?: string
  thumb?: string
  epKey: string
  episode: number
  position: number
  duration?: number
  percent: number
  watched: boolean
  dismissed: boolean
  playCount: number
  lastPlayed: number
}

interface HistoryPage {
  items: HistoryEntry[]
  total: number
  page: number
  hasMore: boolean
}

export function History() {
  const [params, setParams] = useSearchParams()
  const page = Number(params.get('page') ?? 1)
  const qc = useQueryClient()
  const [confirmClear, setConfirmClear] = useState(false)

  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ['history', page],
    queryFn: () => api.get<HistoryPage>(`/api/history?page=${page}&perPage=40`),
  })

  const forget = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.post('/api/history/forget', body),
    onSuccess: () => {
      setConfirmClear(false)
      qc.invalidateQueries({ queryKey: ['history'] })
      qc.invalidateQueries({ queryKey: ['home'] })
    },
  })

  const items = data?.items ?? []

  return (
    <div className="space-y-5">
      <PageHeader
        title="Watch history"
        meta={data ? `${data.total} episodes` : undefined}
        actions={
          items.length > 0 &&
          (confirmClear ? (
            <>
              <Button variant="danger" onClick={() => forget.mutate({ all: true })}>
                Clear everything
              </Button>
              <Button variant="ghost" onClick={() => setConfirmClear(false)}>
                Cancel
              </Button>
            </>
          ) : (
            <Button onClick={() => setConfirmClear(true)}>Clear all</Button>
          ))
        }
      />
      <Stats />

      {isError ? (
        <ErrorState error={error} retry={() => refetch()} />
      ) : isPending ? (
        <div className="space-y-2">
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-20 w-full" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <Empty
          title="Nothing watched yet"
          hint="Episodes you play show up here, with where you left off."
          action={<LinkButton to="/recent">See what just aired</LinkButton>}
        />
      ) : (
        <>
          <ul className="space-y-1.5">
            {items.map((entry) => (
              <Row
                key={`${entry.animeId}-${entry.epKey}`}
                entry={entry}
                onForget={() =>
                  forget.mutate({ animeId: entry.animeId, epKey: entry.epKey })
                }
              />
            ))}
          </ul>

          {(page > 1 || data?.hasMore) && (
            <div className="flex items-center justify-center gap-2">
              <PageButton disabled={page <= 1} onClick={() => setParams({ page: String(page - 1) })}>
                Previous
              </PageButton>
              <span className="text-xs text-base-500">Page {page}</span>
              <PageButton disabled={!data?.hasMore} onClick={() => setParams({ page: String(page + 1) })}>
                Next
              </PageButton>
            </div>
          )}
        </>
      )}
    </div>
  )
}

interface WatchStats {
  weekSeconds: number
  monthSeconds: number
  totalSeconds: number
  episodes: number
  completed: number
  days: Array<{ day: string; seconds: number }>
}

function hours(seconds: number) {
  const h = seconds / 3600
  return h >= 10 ? `${Math.round(h)} h` : h >= 1 ? `${h.toFixed(1)} h` : `${Math.round(seconds / 60)} min`
}

// Time actually played, by window, with the last thirty days as a strip.
function Stats() {
  const { data } = useQuery({
    queryKey: ['watch-stats'],
    queryFn: () => api.get<WatchStats>('/api/history/stats'),
    staleTime: 60_000,
  })
  if (!data || data.totalSeconds === 0) return null
  const peak = Math.max(1, ...data.days.map((d) => d.seconds))

  return (
    <section className="surface p-4" aria-label="Watch stats">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-5">
        <Stat label="This week" value={hours(data.weekSeconds)} />
        <Stat label="This month" value={hours(data.monthSeconds)} />
        <Stat label="All time" value={hours(data.totalSeconds)} />
        <Stat label="Episodes" value={String(data.episodes)} />
        <Stat label="Completed" value={String(data.completed)} />
      </div>
      <div className="mt-3 flex h-10 items-end gap-0.5" title="Last 30 days">
        {data.days.map((d) => (
          <div
            key={d.day}
            title={`${d.day}: ${hours(d.seconds)}`}
            className={cx('flex-1 rounded-sm', d.seconds > 0 ? 'bg-accent-500/70' : 'bg-base-800')}
            style={{ height: `${Math.max(8, (d.seconds / peak) * 100)}%` }}
          />
        ))}
      </div>
    </section>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-[11px] tracking-wide text-base-500 uppercase">{label}</p>
      <p className="text-lg font-semibold text-white tabular-nums">{value}</p>
    </div>
  )
}

function Row({ entry, onForget }: { entry: HistoryEntry; onForget: () => void }) {
  const art = entry.thumb ?? entry.cover

  return (
    <li className="group surface flex items-center gap-3 p-2 transition-colors hover:bg-base-850/80">
      <Link to={`/anime/${entry.animeId}`} className="shrink-0">
        {art ? (
          <img src={art} alt="" loading="lazy" className="h-16 w-11 rounded object-cover" />
        ) : (
          <div className="h-16 w-11 rounded bg-base-850" />
        )}
      </Link>

      <div className="min-w-0 flex-1">
        <Link
          to={`/anime/${entry.animeId}`}
          className="line-clamp-1 text-sm text-base-100 hover:text-white"
        >
          {entry.title}
        </Link>
        <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-base-500">
          <span>Episode {entry.episode}</span>
          <span>·</span>
          <span>{relativeTime(entry.lastPlayed)}</span>
          {entry.playCount > 1 && <Badge>Watched {entry.playCount}×</Badge>}
          {entry.watched ? (
            <Badge tone="accent">Finished</Badge>
          ) : (
            <span>{clockTime(entry.position)}</span>
          )}
          {entry.dismissed && <Badge>Dismissed</Badge>}
        </div>
        {!entry.watched && entry.percent > 0 && (
          <div className="mt-1.5 max-w-xs">
            <ProgressBar value={entry.percent} />
          </div>
        )}
      </div>

      <LinkButton
        to={`/watch/${entry.animeId}/${entry.episode}`}
        size="sm"
        variant={entry.watched ? 'secondary' : 'primary'}
      >
        {entry.watched ? 'Watch again' : 'Resume'}
      </LinkButton>
      <button
        onClick={onForget}
        aria-label="Remove from history"
        className="shrink-0 rounded-md px-2 py-1.5 text-base-600 opacity-0 transition-opacity group-hover:opacity-100 hover:bg-base-800 hover:text-base-200"
      >
        ✕
      </button>
    </li>
  )
}

function PageButton({
  disabled,
  onClick,
  children,
}: {
  disabled?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      disabled={disabled}
      onClick={onClick}
      className={cx(
        'rounded-md px-3 py-1.5 text-sm transition-colors',
        disabled
          ? 'cursor-default text-base-700'
          : 'bg-base-800 text-base-100 hover:bg-base-700',
      )}
    >
      {children}
    </button>
  )
}
