import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import type { DiscoverItem, ScheduleDay } from '../lib/api'
import { airTime, cx } from '../lib/format'
import { useDiscover, useNow, useSchedule } from '../lib/queries'
import { Badge, Skeleton } from './ui'

// Week and month are derived server-side from popularity gained in the window.
const RANKINGS = [
  { key: 'trending', label: 'Today' },
  { key: 'week', label: 'Week' },
  { key: 'month', label: 'Month' },
  { key: 'popular', label: 'All time' },
] as const

export function Popularity() {
  const [sort, setSort] = useState<string>('trending')
  const { data, isPending } = useDiscover(sort, 10)

  return (
    <Panel title="Popular">
      <div className="mb-3 flex gap-1 rounded-md bg-base-900 p-1">
        {RANKINGS.map((rank) => (
          <button
            key={rank.key}
            onClick={() => setSort(rank.key)}
            className={cx(
              'flex-1 rounded px-1.5 py-1 text-xs font-medium transition-colors',
              sort === rank.key
                ? 'bg-base-750 text-white'
                : 'text-base-400 hover:text-base-200',
            )}
          >
            {rank.label}
          </button>
        ))}
      </div>

      {isPending ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-14 w-full" />
          ))}
        </div>
      ) : (
        <ol className="space-y-1">
          {data?.items?.slice(0, 10).map((anime, i) => (
            <RankRow key={anime.id} rank={i + 1} anime={anime} />
          ))}
        </ol>
      )}
    </Panel>
  )
}

function RankRow({ rank, anime }: { rank: number; anime: DiscoverItem }) {
  return (
    <li>
      <Link
        to={`/anime/${anime.id}`}
        className="flex items-center gap-2.5 rounded-md p-1.5 transition-colors hover:bg-base-850"
      >
        <span
          className={cx(
            'w-5 shrink-0 text-right text-sm font-semibold tabular-nums',
            rank <= 3 ? 'text-accent-400' : 'text-base-600',
          )}
        >
          {rank}
        </span>
        {anime.thumb ?? anime.cover ? (
          <img
            src={anime.thumb ?? anime.cover}
            alt=""
            loading="lazy"
            className="h-12 w-9 shrink-0 rounded object-cover"
          />
        ) : (
          <div className="h-12 w-9 shrink-0 rounded bg-base-850" />
        )}
        <div className="min-w-0">
          <p className="line-clamp-2 text-xs leading-snug text-base-200">{anime.title}</p>
          <p className="mt-0.5 text-[11px] text-base-500">
            {anime.format?.replace('_', ' ')}
            {anime.episodes ? ` · ${anime.episodes} ep` : ''}
          </p>
        </div>
      </Link>
    </li>
  )
}

export function ScheduleWidget() {
  const { data, isPending } = useSchedule(7)
  const days = data?.days ?? []
  const todayIndex = Math.max(0, days.findIndex((d) => d.today))
  const [selected, setSelected] = useState<number | null>(null)
  const active = days[selected ?? todayIndex]

  return (
    <Panel title="Schedule" action={<Link to="/schedule" className="text-xs text-base-400 hover:text-accent-400">Full week</Link>}>
      {isPending ? (
        <Skeleton className="h-56 w-full" />
      ) : (
        <>
          {/* A fixed grid rather than a scroller: seven tabs in a narrow
              column were overflowing, leaving Saturday sliced in half. */}
          <div className="mb-3 grid grid-cols-7 gap-0.5">
            {days.map((day, i) => (
              <button
                key={day.date}
                title={day.weekday}
                onClick={() => setSelected(i)}
                className={cx(
                  'rounded-md px-0.5 py-1.5 text-center text-[11px] font-medium transition-colors',
                  i === (selected ?? todayIndex)
                    ? 'bg-accent-500/20 text-accent-400'
                    : 'text-base-400 hover:bg-base-850 hover:text-base-200',
                )}
              >
                {day.today ? 'Now' : day.weekday.slice(0, 3)}
              </button>
            ))}
          </div>
          <DayList day={active} />
        </>
      )}
    </Panel>
  )
}

function DayList({ day }: { day?: ScheduleDay }) {
  const list = useRef<HTMLUListElement>(null)
  const upcoming = useRef<HTMLLIElement>(null)

  // Before the early returns: a hook that only sometimes runs crashes the
  // render the moment the day changes.
  const now = useNow() / 1000

  // The next episode to air is the one worth looking at, so the column opens
  // on it rather than at 5am. Measured against the list's own box: offsetTop is
  // relative to the nearest positioned ancestor, which is the page here, and
  // scrolling by that lands at the bottom every time.
  useEffect(() => {
    const el = upcoming.current
    const box = list.current
    if (!el || !box) return
    box.scrollTop += el.getBoundingClientRect().top - box.getBoundingClientRect().top - 8
    // Keyed on the date rather than the object: the list refetches on a timer,
    // and rescrolling then would yank the column out from under a reader.
  }, [day?.date])

  if (!day) return null
  if (day.items.length === 0) {
    return <p className="py-6 text-center text-xs text-base-500">Nothing airing</p>
  }

  const next = day.items.find((item) => item.airingAt > now)

  return (
    <ul ref={list} className="max-h-96 space-y-1 overflow-y-auto pr-1 scrollbar-thin">
      {day.items.map((item) => {
        const aired = item.airingAt <= now
        const isNext = item === next

        return (
        <li key={`${item.animeId}-${item.episode}`} ref={isNext ? upcoming : undefined}>
          <Link
            to={`/anime/${item.animeId}`}
            className={cx(
              'flex items-center gap-2.5 rounded-md p-1.5 transition-colors hover:bg-base-850',
              aired && 'opacity-45',
              isNext && 'bg-accent-500/10 ring-1 ring-accent-500/40',
            )}
          >
            {/* Small enough not to crowd the column, large enough to be
                recognised faster than the title can be read. */}
            {item.thumb ?? item.cover ? (
              <img
                src={item.thumb ?? item.cover}
                alt=""
                loading="lazy"
                className="h-10 w-7 shrink-0 rounded object-cover"
              />
            ) : (
              <div className="h-10 w-7 shrink-0 rounded bg-base-850" />
            )}
            <div className="min-w-0 flex-1">
              <p className="line-clamp-1 text-xs text-base-200">{item.title}</p>
              <p className="text-[11px] whitespace-nowrap text-base-500">
                {airTime(item.airingAt)} · Ep {item.episode}
              </p>
            </div>
            {isNext ? (
              <Badge tone="accent">Next</Badge>
            ) : (
              item.behind > 0 && <Badge tone="accent">{item.behind} behind</Badge>
            )}
          </Link>
        </li>
        )
      })}
    </ul>
  )
}

function Panel({
  title,
  action,
  children,
}: {
  title: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="surface p-3">
      <div className="mb-2 flex items-baseline justify-between">
        <h2 className="section-title">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}
