import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import type { DiscoverItem } from '../lib/api'
import { cx, tint } from '../lib/format'
import { PlayIcon } from './PosterCard'
import { StatusMenu } from './StatusMenu'
import { Skeleton } from './ui'

const ROTATE_MS = 9000

// How far ahead of the current slide artwork is fetched. Two rotations of lead
// is enough for a banner to arrive on a slow connection.
const PRELOAD = 2

export function Hero({ items, loading }: { items?: DiscoverItem[]; loading?: boolean }) {
  const slides = (items ?? []).filter((a) => a.cover).slice(0, 10)
  const [index, setIndex] = useState(0)
  const [paused, setPaused] = useState(false)
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => {
    if (paused || slides.length < 2) return
    timer.current = window.setInterval(
      () => setIndex((i) => (i + 1) % slides.length),
      ROTATE_MS,
    )
    return () => window.clearInterval(timer.current)
  }, [paused, slides.length])

  // A shorter list after a refetch would otherwise leave the index past the end.
  useEffect(() => {
    if (index >= slides.length) setIndex(0)
  }, [index, slides.length])

  // Every slide is in the viewport at opacity zero, so lazy loading holds none
  // of them back. Fetch just ahead of the rotation and keep what has arrived.
  const [fetched, setFetched] = useState<number[]>([])
  useEffect(() => {
    if (slides.length === 0) return
    setFetched((prev) => {
      const next = new Set(prev)
      for (let step = 0; step <= PRELOAD; step++) {
        next.add((index + step) % slides.length)
      }
      return next.size === prev.length ? prev : [...next]
    })
  }, [index, slides.length])

  if (loading || slides.length === 0) {
    return <Skeleton className="h-[clamp(20rem,42vw,30rem)] w-full rounded-xl" />
  }

  const active = slides[index]

  return (
    <section
      className="relative h-[clamp(20rem,42vw,30rem)] overflow-hidden rounded-xl bg-base-900"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      aria-roledescription="carousel"
    >
      {slides.map((anime, i) => (
        <div
          key={anime.id}
          aria-hidden={i !== index}
          className={cx(
            'absolute inset-0 transition-opacity duration-700',
            i === index ? 'opacity-100' : 'pointer-events-none opacity-0',
          )}
        >
          {fetched.includes(i) && (
            <img
              // The banner is 1900x400; a poster is 230 wide and looks blurred
              // the moment it is stretched across the strip.
              src={anime.banner ?? anime.cover}
              alt=""
              decoding="async"
              className="size-full object-cover object-center"
            />
          )}
          <div className="absolute inset-0 bg-gradient-to-r from-base-950 via-base-950/80 to-transparent" />
          <div className="absolute inset-0 bg-gradient-to-t from-base-950 via-transparent to-transparent" />
        </div>
      ))}

      <div className="relative flex h-full flex-col justify-end p-5 sm:p-8">
        <div className="max-w-xl">
          <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-base-300">
            {active.format && <span>{active.format.replace('_', ' ')}</span>}
            {active.episodes ? <span>· {active.episodes} episodes</span> : null}
            {active.score ? (
              <span
                className="rounded px-1.5 py-0.5 font-medium"
                style={{
                  background: tint(active.color, 0.25) ?? 'rgba(111,92,255,0.2)',
                  color: '#fff',
                }}
              >
                ★ {active.score}
              </span>
            ) : null}
          </div>

          <h1 className="text-2xl leading-tight font-semibold text-white text-balance sm:text-4xl">
            {active.title}
          </h1>

          {active.Genres && active.Genres.length > 0 && (
            <p className="mt-2 text-sm text-base-300">{active.Genres.slice(0, 4).join(' · ')}</p>
          )}

          <div className="mt-5 flex items-center gap-2">
            <Link
              to={`/watch/${active.id}/${(active.progress ?? 0) + 1}`}
              className="flex items-center gap-2 rounded-md bg-white px-4 py-2 text-sm font-semibold text-base-950 transition-transform hover:scale-[1.02] active:scale-95"
            >
              <PlayIcon />
              {active.progress ? `Continue ep ${active.progress + 1}` : 'Watch'}
            </Link>
            <Link
              to={`/anime/${active.id}`}
              className="rounded-md bg-base-800/80 px-4 py-2 text-sm font-medium text-base-100 backdrop-blur-sm transition-colors hover:bg-base-700"
            >
              Details
            </Link>
            <StatusMenu animeId={active.id} current={active.onList ? 'CURRENT' : null} compact />
          </div>
        </div>

        <div className="mt-5 flex gap-1.5" role="tablist" aria-label="Featured anime">
          {slides.map((slide, i) => (
            <button
              key={slide.id}
              role="tab"
              aria-selected={i === index}
              aria-label={slide.title}
              onClick={() => setIndex(i)}
              className={cx(
                'h-1 rounded-full transition-all duration-300',
                i === index ? 'w-7 bg-white' : 'w-3 bg-white/30 hover:bg-white/50',
              )}
            />
          ))}
        </div>
      </div>

      {slides.length > 1 && (
        <div className="absolute right-4 bottom-4 flex gap-2 sm:right-6 sm:bottom-6">
          <Arrow
            label="Previous"
            onClick={() => setIndex((i) => (i - 1 + slides.length) % slides.length)}
          />
          <Arrow label="Next" next onClick={() => setIndex((i) => (i + 1) % slides.length)} />
        </div>
      )}
    </section>
  )
}

function Arrow({ label, next, onClick }: { label: string; next?: boolean; onClick: () => void }) {
  return (
    <button
      aria-label={label}
      onClick={onClick}
      className="rounded-full bg-base-950/50 p-2 text-white/70 backdrop-blur-sm transition-colors hover:bg-base-950/80 hover:text-white"
    >
      <svg viewBox="0 0 24 24" fill="none" className={cx('size-5', next && 'rotate-180')}>
        <path
          d="M15 18l-6-6 6-6"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </button>
  )
}
