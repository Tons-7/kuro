import { Link } from 'react-router-dom'
import type { DiscoverItem, LibraryItem } from '../lib/api'
import { cx, tint } from '../lib/format'
import { Badge, ProgressBar } from './ui'
import { HoverInfo } from './HoverInfo'
import { StatusMenu } from './StatusMenu'

export interface CardAnime {
  id: number
  title: string
  cover?: string | null
  color?: string | null
  episodes?: number | null
  format?: string | null
  score?: number | null
  progress?: number
  status?: string | null
  badge?: string
  percent?: number
  /** Your own rating, 0-100; shown as /10 on the card. */
  myScore?: number
  romaji?: string | null
  english?: string | null
  seasonYear?: number | null
  genres?: string[] | null
  description?: string | null
  /** Airing state, which the list status field shadows on a library item. */
  airing?: string | null
}

export function toCard(item: DiscoverItem | LibraryItem): CardAnime {
  const discover = item as DiscoverItem
  const library = item as LibraryItem
  return {
    id: item.id,
    title: item.title,
    cover: item.cover ?? null,
    color: (discover.color ?? library.color) ?? null,
    episodes: item.episodes ?? null,
    format: discover.format ?? null,
    score: discover.score ?? null,
    progress: item.progress,
    status: library.status ?? null,
    romaji: discover.romaji ?? null,
    english: discover.english ?? null,
    seasonYear: discover.seasonYear ?? null,
    genres: discover.Genres ?? null,
    description: discover.description ?? null,
    airing: discover.status ?? null,
  }
}

/**
 * Poster with a hover layer carrying the two things worth doing without
 * opening the show: play it, or tag it. On touch there is no hover, so the
 * overlay is always visible at reduced strength instead of unreachable.
 */
export function PosterCard({ anime, to }: { anime: CardAnime; to?: string }) {
  const href = to ?? `/anime/${anime.id}`
  const accent = tint(anime.color, 0.55)

  return (
    <HoverInfo anime={{ ...anime, status: anime.airing }}>
    <div className="group/card relative">
      <Link
        to={href}
        className="block rounded-card focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-400"
      >
        {/* Lifts on hover and picks up the show's own colour as a glow, so the
            card under the pointer is unmistakable in a dense grid. */}
        <div
          className="relative aspect-[2/3] overflow-hidden rounded-card bg-base-850 shadow-card transition-[transform,box-shadow] duration-300 ease-out-quint group-hover/card:-translate-y-1 group-hover/card:shadow-lift"
          style={
            accent
              ? ({ '--tw-shadow-color': accent } as React.CSSProperties)
              : undefined
          }
        >
          {anime.cover ? (
            <img
              src={anime.cover}
              alt=""
              loading="lazy"
              decoding="async"
              className="size-full object-cover transition-transform duration-300 group-hover/card:scale-[1.04]"
            />
          ) : (
            <div
              className="flex size-full flex-col items-center justify-center gap-2 bg-gradient-to-b from-base-800 to-base-900 p-3 text-center"
              style={accent ? { backgroundImage: `linear-gradient(to bottom, ${accent}, var(--color-base-900))` } : undefined}
            >
              <span className="text-3xl font-bold text-white/80">{anime.title.slice(0, 1)}</span>
              <span className="line-clamp-3 text-xs font-medium text-white/80">{anime.title}</span>
            </div>
          )}

          <div className="pointer-events-none absolute inset-0 bg-gradient-to-t from-base-950/90 via-base-950/10 to-transparent opacity-80 transition-opacity duration-200 group-hover/card:opacity-100" />

          {anime.badge && (
            <div className="absolute top-1.5 left-1.5">
              <Badge tone="accent">{anime.badge}</Badge>
            </div>
          )}

          <div className="pointer-events-none absolute inset-x-0 bottom-0 p-2">
            {typeof anime.percent === 'number' && anime.percent > 0 && (
              <ProgressBar value={anime.percent} className="mb-1.5" />
            )}
            <div className="flex items-center gap-1.5 text-[11px] text-base-300">
              {anime.format && <span>{anime.format.replace('_', ' ')}</span>}
              {anime.episodes ? <span>· {anime.episodes} ep</span> : null}
              {anime.myScore ? (
                <span className="ml-auto text-accent-300" title="Your score">
                  ★ {Math.round(anime.myScore / 10)}/10
                </span>
              ) : anime.score ? (
                <span className="ml-auto">★ {anime.score}</span>
              ) : null}
            </div>
          </div>

          <div className="absolute inset-0 flex items-center justify-center opacity-0 transition-opacity duration-200 group-hover/card:opacity-100">
            <span className="grid size-11 place-items-center rounded-full bg-base-950/70 ring-1 ring-white/20 backdrop-blur-sm">
              <PlayIcon />
            </span>
          </div>
        </div>
      </Link>

      <div className="absolute top-1.5 right-1.5 opacity-0 transition-opacity duration-200 group-hover/card:opacity-100 focus-within:opacity-100 max-sm:opacity-100">
        <StatusMenu animeId={anime.id} current={anime.status} compact />
      </div>

      <Link to={href} className="mt-2 block">
        <p
          className="line-clamp-2 text-sm leading-snug text-base-200 transition-colors group-hover/card:text-white"
          title={anime.title}
        >
          {anime.title}
        </p>
      </Link>
    </div>
    </HoverInfo>
  )
}

export function PosterGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-3 gap-x-3 gap-y-5 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-7">
      {children}
    </div>
  )
}

export function PlayIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={cx('size-5 translate-x-px', className)} aria-hidden>
      <path d="M8 5.5v13l11-6.5-11-6.5Z" fill="currentColor" />
    </svg>
  )
}
