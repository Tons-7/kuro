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
  /** Episodes aired, where no total has been announced. */
  aired?: number | null
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
  banner?: string | null
  malId?: number | null
  nextEpisode?: number | null
  nextAiringAt?: number | null
  startDate?: string | null
  endDate?: string | null
  duration?: number | null
  studios?: { id: number; name: string }[] | null
}

export function toCard(item: DiscoverItem | LibraryItem): CardAnime {
  const discover = item as DiscoverItem
  const library = item as LibraryItem
  // Both carry a "status", meaning opposite things: on a search result it is
  // the airing state, on a library row the list tag.
  const fromDiscover = 'onList' in item
  const nextEpisode = (fromDiscover ? discover.nextEpisode : library.nextEpisode) ?? null
  return {
    id: item.id,
    title: item.title,
    cover: item.cover ?? null,
    color: (discover.color ?? library.color) ?? null,
    episodes: item.episodes ?? null,
    // Out so far while airing: "10/13", or "10" for a run with no total yet.
    aired: nextEpisode ? nextEpisode - 1 : null,
    format: discover.format ?? null,
    score: discover.score ?? null,
    progress: item.progress,
    status: fromDiscover ? (discover.listStatus ?? null) : (library.status ?? null),
    romaji: discover.romaji ?? null,
    english: discover.english ?? null,
    seasonYear: discover.seasonYear ?? null,
    genres: discover.Genres ?? null,
    description: discover.description ?? null,
    airing: fromDiscover ? (discover.status ?? null) : null,
    banner: discover.banner ?? null,
    malId: discover.malId ?? null,
    nextEpisode,
    nextAiringAt: (fromDiscover ? discover.nextAiringAt : library.nextAiringAt) ?? null,
    startDate: discover.startDate ?? null,
    endDate: discover.endDate ?? null,
    duration: discover.duration ?? null,
    studios: discover.studios ?? null,
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
  const watched = anime.progress ?? 0
  const released = anime.aired ?? anime.episodes
  const nextEpisode =
    anime.airing === 'NOT_YET_RELEASED' || (released && watched >= released) ? null : watched + 1

  return (
    <HoverInfo
      anime={{
        ...anime,
        status: anime.airing,
        listStatus: anime.status,
        play: nextEpisode
          ? {
              to: `/watch/${anime.id}/${nextEpisode}`,
              label: watched > 0 ? `Continue ep ${nextEpisode}` : 'Watch ep 1',
            }
          : undefined,
      }}
    >
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

          {anime.badge ? (
            <div className="absolute top-1.5 left-1.5">
              <Badge tone="overlay">{anime.badge}</Badge>
            </div>
          ) : anime.airing === 'RELEASING' ? (
            // Airing, and how far: the thing a poster could not say before.
            <span className="absolute top-1.5 left-1.5 flex items-center gap-1 rounded-md bg-base-950/80 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-300 ring-1 ring-emerald-400/30 backdrop-blur-sm">
              <span className="size-1.5 rounded-full bg-emerald-400" />
              {anime.aired ? `Ep ${anime.aired}` : 'Airing'}
            </span>
          ) : null}

          <div className="pointer-events-none absolute inset-x-0 bottom-0 p-2">
            {typeof anime.percent === 'number' && anime.percent > 0 && (
              <ProgressBar value={anime.percent} className="mb-1.5" />
            )}
            {/* One line whatever the card width: on a phone's three columns the
                episode count gives way before anything wraps. */}
            <div className="flex items-center gap-1.5 text-[11px] whitespace-nowrap text-base-300">
              {anime.format && <span>{anime.format.replace('_', ' ')}</span>}
              {anime.episodes ? (
                <span className="min-w-0 truncate" title={anime.aired ? `${anime.aired} of ${anime.episodes} out so far` : undefined}>
                  · {anime.aired && anime.aired < anime.episodes ? `${anime.aired}/` : ''}
                  {anime.episodes} ep
                </span>
              ) : anime.aired ? (
                <span className="min-w-0 truncate" title="Still airing; no total announced">· {anime.aired} aired</span>
              ) : null}
              {anime.myScore ? (
                <span className="ml-auto shrink-0 text-accent-300" title="Your score">
                  ★ {Math.round(anime.myScore / 10)}/10
                </span>
              ) : anime.score ? (
                // Out of ten, as everywhere else shows it.
                <span className="ml-auto shrink-0 text-amber-300/90">★ {(anime.score / 10).toFixed(1)}</span>
              ) : null}
            </div>
          </div>
        </div>
      </Link>

      {/* A sibling over the poster, not inside its link: the play button plays,
          the rest of the card opens the show. Nothing next to play, no button. */}
      {nextEpisode && (
        <div className="pointer-events-none absolute inset-x-0 top-0 grid aspect-[2/3] place-items-center">
          <Link
            to={`/watch/${anime.id}/${nextEpisode}`}
            aria-label={`Play episode ${nextEpisode}`}
            title={`Play episode ${nextEpisode}`}
            // Never on touch: unseen there, a tap on the poster's middle would
            // play instead of opening the show.
            className="pointer-events-auto grid size-11 place-items-center rounded-full bg-base-950/70 opacity-0 ring-1 ring-white/20 backdrop-blur-sm transition-[opacity,transform] duration-200 group-hover/card:opacity-100 hover:scale-110 hover:bg-accent-500 focus-visible:opacity-100 [@media(hover:none)]:hidden"
          >
            <PlayIcon />
          </Link>
        </div>
      )}

      {/* Shown wherever there is no hover (tablets too, not just narrow
          screens): invisible there, it still caught taps. */}
      <div className="absolute top-1.5 right-1.5 opacity-0 transition-opacity duration-200 group-hover/card:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100">
        <StatusMenu animeId={anime.id} current={anime.status} compact />
      </div>

      <Link to={href} className="mt-2 block">
        <p
          className="line-clamp-2 text-sm leading-snug font-medium text-base-200 transition-colors group-hover/card:text-white"
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
