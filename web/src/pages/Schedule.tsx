import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { airTime, cx, relativeTime } from '../lib/format'
import { useNow, useSchedule } from '../lib/queries'
import { Badge, Empty, ErrorState, FilterToggle, PageHeader, Skeleton } from '../components/ui'

function dayLabel(date: string) {
  return new Date(`${date}T00:00`).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

export function SchedulePage() {
  const [mine, setMine] = useState(false)
  const { data, isPending, isError, error, refetch } = useSchedule(7, mine)

  const days = data?.days ?? []
  const todayIndex = Math.max(0, days.findIndex((d) => d.today))
  const [selected, setSelected] = useState<number | null>(null)
  const active = days[selected ?? todayIndex]

  const now = useNow() / 1000
  const nextUp = active?.items.find((item) => item.airingAt > now)
  // The "now" line sits between what has aired and what has not.
  const nowLine = active?.today && nextUp && active.items[0] !== nextUp

  // Scroll to what airs next. Keyed on the date, not the object, so the refetch
  // doesn't scroll the page out from under a reader.
  const upcoming = useRef<HTMLLIElement>(null)
  useEffect(() => {
    const el = upcoming.current
    if (!el) return
    const box = el.getBoundingClientRect()
    if (box.top >= 0 && box.bottom <= window.innerHeight) return
    el.scrollIntoView({ block: 'center', behavior: 'auto' })
  }, [active?.date])

  if (isError) return <ErrorState error={error} retry={() => refetch()} />

  return (
    <div className="space-y-5">
      <PageHeader
        title="Schedule"
        subtitle="When new episodes air, in your local time."
        actions={
          <FilterToggle on={mine} onChange={setMine}>
            Only my list
          </FilterToggle>
        }
      />

      {isPending ? (
        <Skeleton className="h-96 w-full" />
      ) : (
        <>
          <div className="no-scrollbar flex gap-1.5 overflow-x-auto pb-1">
            {days.map((day, i) => {
              const isActive = i === (selected ?? todayIndex)
              return (
                <button
                  key={day.date}
                  onClick={() => setSelected(i)}
                  className={cx(
                    'shrink-0 rounded-lg px-4 py-2 text-left ring-1 transition-colors',
                    isActive
                      ? 'bg-accent-500/15 ring-accent-500/40'
                      : 'bg-base-900/60 ring-white/[0.06] hover:bg-base-850',
                  )}
                >
                  <span
                    className={cx(
                      'block text-sm font-medium',
                      isActive ? 'text-accent-300' : 'text-base-200',
                    )}
                  >
                    {day.today ? 'Today' : day.weekday}
                  </span>
                  <span className="block text-[11px] text-base-500">
                    {dayLabel(day.date)} · {day.items.length} ep
                  </span>
                </button>
              )
            })}
          </div>

          {!active || active.items.length === 0 ? (
            <Empty
              title="Nothing airing this day"
              hint={mine ? 'Nothing from your list, at least.' : undefined}
            />
          ) : (
            <ul className="surface divide-y divide-white/[0.05] overflow-hidden">
              {active.items.map((item) => {
                const aired = item.airingAt <= now
                const isNext = item === nextUp

                return (
                  <li key={`${item.animeId}-${item.episode}`} ref={isNext ? upcoming : undefined}>
                    {isNext && nowLine && (
                      <div
                        aria-hidden
                        className="flex items-center gap-2 px-3 py-1 text-[11px] font-medium text-accent-400"
                      >
                        <span className="h-px flex-1 bg-accent-500/50" />
                        Now · {airTime(now)}
                        <span className="h-px flex-1 bg-accent-500/50" />
                      </div>
                    )}
                    <Link
                      to={`/anime/${item.animeId}`}
                      className={cx(
                        'flex items-center gap-3 border-l-2 px-3 py-2.5 transition-colors hover:bg-base-850/70',
                        aired ? 'border-transparent opacity-60 hover:opacity-100' : 'border-transparent',
                        isNext && 'border-accent-500 bg-accent-500/10',
                      )}
                    >
                      <div className="w-20 shrink-0 text-right">
                        <p className="text-sm font-medium whitespace-nowrap tabular-nums text-base-100">
                          {airTime(item.airingAt)}
                        </p>
                        <p
                          className={cx(
                            'text-[11px] whitespace-nowrap',
                            isNext ? 'text-accent-400' : 'text-base-500',
                          )}
                        >
                          {relativeTime(item.airingAt)}
                        </p>
                      </div>

                      {item.thumb ?? item.cover ? (
                        <img
                          src={item.thumb ?? item.cover}
                          alt=""
                          loading="lazy"
                          className="h-14 w-10 shrink-0 rounded object-cover shadow-card"
                        />
                      ) : (
                        <div className="h-14 w-10 shrink-0 rounded bg-base-850" />
                      )}

                      <div className="min-w-0 flex-1">
                        <p className="line-clamp-1 text-sm font-medium text-base-100">{item.title}</p>
                        <p className="mt-0.5 text-xs text-base-500">
                          Episode {item.episode}
                          {/* A late-night broadcast is a different weekday in Japan,
                              which is the one release groups label it with. */}
                          {item.jstWeekday !== active.weekday && ` · ${item.jstWeekday} JST`}
                        </p>
                      </div>

                      <div className="flex shrink-0 items-center gap-1.5">
                        {isNext && <Badge tone="accent">Next</Badge>}
                        {item.watched && <Badge tone="success">Watched</Badge>}
                        {item.behind > 0 && <Badge tone="accent">{item.behind} behind</Badge>}
                        {item.onList && !item.watched && item.behind === 0 && <Badge>On list</Badge>}
                      </div>
                    </Link>
                  </li>
                )
              })}
            </ul>
          )}
        </>
      )}
    </div>
  )
}
