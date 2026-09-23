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

  if (loading) {
    return <Skeleton className="h-[clamp(22rem,46vw,34rem)] w-full rounded-2xl" />
  }
  // Trending failed or came back empty: nothing to feature, and a skeleton
  // that never resolves reads as broken.
  if (slides.length === 0) return null

  const active = slides[Math.min(index, slides.length - 1)]
  const caughtUp =
    active.listStatus === 'COMPLETED' ||
    (!!active.episodes && (active.progress ?? 0) >= active.episodes)

  return (
    <section
      className="relative h-[clamp(22rem,46vw,34rem)] overflow-hidden rounded-2xl bg-base-900 ring-1 ring-white/[0.06]"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      // Keyboard too: rotating under focus sent Enter to a different show.
      onFocus={() => setPaused(true)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setPaused(false)
      }}
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
              // A slow drift keeps the strip alive; reduced motion stops it.
              className={cx(
                'size-full object-cover object-center transition-transform duration-[9000ms] ease-linear',
                i === index ? 'scale-105' : 'scale-100',
              )}
            />
          )}
          <div className="absolute inset-0 bg-gradient-to-r from-base-950 via-base-950/75 to-transparent" />
          <div className="absolute inset-0 bg-gradient-to-t from-base-950 via-base-950/10 to-transparent" />
          {/* The show's own colour, low in the corner. */}
          <div
            className="absolute inset-0"
            style={{
              background: `radial-gradient(60% 70% at 0% 100%, ${tint(anime.color, 0.35) ?? 'rgb(111 92 255 / 0.25)'}, transparent 70%)`,
            }}
          />
        </div>
      ))}

      <div className="relative flex h-full flex-col justify-end p-5 sm:p-10">
        <div className="max-w-2xl">
          <div className="mb-3 flex flex-wrap items-center gap-2 text-xs font-semibold">
            <span className="rounded-md bg-accent-500 px-2 py-0.5 tracking-wider text-white uppercase">
              #{index + 1} Trending
            </span>
            {active.format && (
              <span className="rounded-md bg-base-950/60 px-2 py-0.5 text-base-100 uppercase ring-1 ring-white/10 backdrop-blur-md">
                {active.format.replace('_', ' ')}
              </span>
            )}
            {active.episodes ? (
              <span className="rounded-md bg-base-950/60 px-2 py-0.5 text-base-100 uppercase ring-1 ring-white/10 backdrop-blur-md">
                {active.episodes} eps
              </span>
            ) : null}
            {active.score ? (
              <span className="rounded-md bg-base-950/60 px-2 py-0.5 text-amber-300 ring-1 ring-amber-400/30 backdrop-blur-md">
                ★ {(active.score / 10).toFixed(1)}
              </span>
            ) : null}
          </div>

          <h1 className="font-display text-3xl leading-[1.05] font-bold tracking-tight text-white text-balance drop-shadow-lg sm:text-5xl lg:text-6xl">
            {active.title}
          </h1>

          {active.Genres && active.Genres.length > 0 && (
            <p className="mt-3 text-sm font-medium text-base-200">{active.Genres.slice(0, 4).join(' · ')}</p>
          )}
          {active.description && (
            <p className="mt-2 line-clamp-2 max-w-xl text-sm leading-relaxed text-base-300 max-sm:hidden">
              {active.description.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim()}
            </p>
          )}

          <div className="mt-6 flex items-center gap-2">
            {/* Finished or caught up: there is no next episode to link to, so
                the show page decides (rewatch, or wait for the next one). */}
            {caughtUp ? null : (
              <Link
                to={`/watch/${active.id}/${(active.progress ?? 0) + 1}`}
                className="flex items-center gap-2 rounded-xl bg-gradient-to-b from-accent-400 to-accent-500 px-5 py-2.5 text-sm font-semibold text-white shadow-[0_8px_24px_-8px_rgb(111_92_255/0.7)] transition-[transform,filter] hover:scale-[1.02] hover:brightness-110 active:scale-95"
              >
                <PlayIcon />
                {active.progress ? `Continue ep ${active.progress + 1}` : 'Watch'}
              </Link>
            )}
            <Link
              to={`/anime/${active.id}`}
              className={cx(
                'rounded-xl px-5 py-2.5 text-sm font-medium backdrop-blur-md transition-colors',
                caughtUp
                  ? 'bg-gradient-to-b from-accent-400 to-accent-500 text-white hover:brightness-110'
                  : 'bg-white/10 text-white ring-1 ring-white/15 hover:bg-white/20',
              )}
            >
              Details
            </Link>
            {/* Keyed by slide: rotation must close a menu opened for the last one. */}
            <StatusMenu key={active.id} animeId={active.id} current={active.listStatus ?? null} compact />
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
