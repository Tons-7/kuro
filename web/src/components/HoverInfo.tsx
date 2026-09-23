import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { cx, readableTint, relativeTime, tint } from '../lib/format'
import { ProgressBar } from './ui'

export interface HoverAnime {
  id: number
  title: string
  romaji?: string | null
  english?: string | null
  format?: string | null
  status?: string | null
  episodes?: number | null
  /** Episodes out so far while airing. */
  aired?: number | null
  seasonYear?: number | null
  score?: number | null
  genres?: string[] | null
  color?: string | null
  description?: string | null
  progress?: number
  /** Set when the card leads somewhere other than the series page. */
  play?: { to: string; label: string }
  cover?: string | null
  banner?: string | null
  malId?: number | null
  nextEpisode?: number | null
  nextAiringAt?: number | null
  startDate?: string | null
  endDate?: string | null
  duration?: number | null
  studios?: { id: number; name: string }[] | null
  /** Which list it is on; `status` is the airing state. */
  listStatus?: string | null
}

const PANEL_WIDTH = 340
const GAP = 12
const OPEN_DELAY = 380
const CLOSE_DELAY = 140

/**
 * Info panel shown while the pointer rests on a card. A portal, because the
 * rails scroll horizontally and would clip anything positioned inside them.
 * Touch never opens it: a tap belongs to the card underneath.
 */
export function HoverInfo({ anime, children }: { anime: HoverAnime; children: ReactNode }) {
  const anchor = useRef<HTMLDivElement>(null)
  const timer = useRef(0)
  const [box, setBox] = useState<{ top: number; left: number } | null>(null)

  useEffect(() => () => window.clearTimeout(timer.current), [])

  const open = () => {
    if (!window.matchMedia('(hover: hover) and (pointer: fine)').matches) return
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => {
      const el = anchor.current
      if (!el) return
      const rect = el.getBoundingClientRect()

      // Beside the card, flipping sides when there is no room and clamped so a
      // card near the bottom stays readable.
      const right = rect.right + GAP
      const left = right + PANEL_WIDTH < window.innerWidth ? right : rect.left - GAP - PANEL_WIDTH
      const top = Math.min(
        Math.max(GAP, rect.top + rect.height / 2 - 220),
        window.innerHeight - 470,
      )
      setBox({ top: Math.max(GAP, top), left: Math.max(GAP, left) })
    }, OPEN_DELAY)
  }

  const close = () => {
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setBox(null), CLOSE_DELAY)
  }

  return (
    <div ref={anchor} onMouseEnter={open} onMouseLeave={close} onFocus={open} onBlur={close}>
      {children}
      {box &&
        createPortal(
          <Panel anime={anime} top={box.top} left={box.left} onEnter={open} onLeave={close} />,
          document.body,
        )}
    </div>
  )
}

const FORMATS: Record<string, string> = {
  TV_SHORT: 'TV Short',
  MOVIE: 'Movie',
  SPECIAL: 'Special',
  MUSIC: 'Music',
}

// Airing state as a pill: the question a hover answers first after "what is it".
function airingState(anime: HoverAnime): { label: string; tone: string; live?: boolean } | null {
  switch (anime.status) {
    // A dark backing under each: the pill sits on cover art of any brightness.
    case 'RELEASING':
      return { label: 'Airing', tone: 'bg-base-950/80 text-emerald-300 ring-emerald-400/40', live: true }
    case 'FINISHED':
      return { label: 'Finished', tone: 'bg-base-950/80 text-base-200 ring-white/20' }
    case 'NOT_YET_RELEASED':
      return { label: 'Upcoming', tone: 'bg-base-950/80 text-sky-300 ring-sky-400/40' }
    case 'HIATUS':
      return { label: 'On hiatus', tone: 'bg-base-950/80 text-amber-300 ring-amber-400/40' }
    case 'CANCELLED':
      return { label: 'Cancelled', tone: 'bg-base-950/80 text-red-300 ring-red-400/40' }
  }
  return null
}

// "10/13 eps" while airing, "13 eps" once out, "10 eps" with no total yet.
function episodeCount(anime: HoverAnime): string | null {
  const { episodes, aired } = anime
  if (anime.status === 'RELEASING' && aired && episodes && aired < episodes) return `${aired}/${episodes} eps`
  if (episodes) return `${episodes} ${episodes === 1 ? 'ep' : 'eps'}`
  if (aired) return `${aired} eps`
  return null
}

function day(date: string): string {
  const [y, m, d] = date.split('-').map(Number)
  if (!m) return String(y)
  return new Date(y, m - 1, d || 1).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    ...(d ? { day: 'numeric' } : {}),
  })
}

// The whole run: "Apr 7, 2013 – Sep 29, 2013" once finished, "… – now" while
// airing, one date for a film or a single day.
function aired(anime: HoverAnime): string | null {
  const { startDate, endDate } = anime
  if (!startDate) return anime.seasonYear ? String(anime.seasonYear) : null
  const start = day(startDate)
  // An airing show's end date is only the plan; it has aired until now.
  if (anime.status === 'RELEASING') return `${start} – now`
  if (endDate && endDate !== startDate) return `${start} – ${day(endDate)}`
  return start
}

function Panel({
  anime,
  top,
  left,
  onEnter,
  onLeave,
}: {
  anime: HoverAnime
  top: number
  left: number
  onEnter: () => void
  onLeave: () => void
}) {
  // The show's own colour carries the panel: glow, synopsis rule, values.
  const hue = readableTint(anime.color) ?? '#8b7cff'
  const glow = tint(anime.color, 0.45) ?? 'rgba(111,92,255,0.35)'
  const art = anime.banner ?? anime.cover
  const state = airingState(anime)
  const count = episodeCount(anime)
  const other = anime.romaji && anime.romaji !== anime.title ? anime.romaji : anime.english
  const synopsis = anime.description?.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim()
  const upcoming =
    anime.nextEpisode && anime.nextAiringAt && anime.nextAiringAt * 1000 > Date.now()
      ? `Ep ${anime.nextEpisode} ${relativeTime(anime.nextAiringAt)}`
      : null
  const total = anime.episodes ?? anime.aired
  const rows = [
    { label: 'Aired', value: aired(anime) },
    { label: 'Studio', value: anime.studios?.map((s) => s.name).join(', ') },
    { label: 'Genres', value: anime.genres?.slice(0, 4).join(', ') },
  ].filter((r) => r.value)

  return (
    <div
      onMouseEnter={onEnter}
      onMouseLeave={onLeave}
      style={{ top, left, width: PANEL_WIDTH, boxShadow: `0 24px 60px -24px ${glow}, 0 8px 24px -8px rgb(0 0 0 / 0.6)` }}
      className="fixed z-50 animate-rise overflow-hidden rounded-2xl bg-base-900/95 ring-1 ring-white/10 backdrop-blur-xl"
    >
      <div className="relative h-28 overflow-hidden bg-base-850">
        {art && (
          <img
            src={art}
            alt=""
            className={cx(
              'size-full object-cover',
              // A poster stretched to a strip only works blurred into a backdrop.
              !anime.banner && 'scale-110 blur-md brightness-75',
            )}
          />
        )}
        <div className="absolute inset-0 bg-gradient-to-t from-base-900 via-base-900/30 to-transparent" />
        {anime.format && (
          <span
            className="absolute top-2.5 left-2.5 rounded-md px-2 py-0.5 text-[10px] font-extrabold tracking-wider text-base-950 uppercase shadow"
            style={{ background: hue }}
          >
            {FORMATS[anime.format] ?? anime.format.replace('_', ' ')}
          </span>
        )}
        {state && (
          <span
            className={cx(
              'absolute top-2.5 right-2.5 flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-semibold tracking-wide uppercase ring-1 backdrop-blur-md',
              state.tone,
            )}
          >
            {state.live && (
              <span className="relative flex size-1.5">
                <span className="absolute inset-0 animate-ping rounded-full bg-emerald-400 opacity-75" />
                <span className="relative size-1.5 rounded-full bg-emerald-400" />
              </span>
            )}
            {state.label}
          </span>
        )}
      </div>

      <div className="-mt-6 space-y-3 px-4 pb-4">
        <div className="relative">
          <p className="line-clamp-2 text-[15px] leading-tight font-extrabold tracking-wide text-white uppercase">
            {anime.title}
          </p>
          {other && other !== anime.title && (
            <p className="mt-1 line-clamp-1 text-[11px] text-base-400">{other}</p>
          )}
        </div>

        <div className="flex flex-wrap gap-1.5 text-[11px] font-semibold">
          {anime.score ? (
            <span className="flex items-center gap-1 rounded-md bg-amber-400/10 px-2 py-1 text-amber-300 ring-1 ring-amber-400/20">
              <StarIcon />
              {(anime.score / 10).toFixed(1)}
            </span>
          ) : null}
          {count && (
            <span
              className="flex items-center gap-1 rounded-md bg-white/5 px-2 py-1 text-base-100 uppercase ring-1 ring-white/10"
              title={
                anime.status === 'RELEASING' && anime.aired
                  ? `${anime.aired} out${anime.episodes ? ` of ${anime.episodes}` : ''}`
                  : undefined
              }
            >
              <LayersIcon />
              {count}
            </span>
          )}
          {anime.duration ? (
            <span className="rounded-md bg-white/5 px-2 py-1 text-base-300 ring-1 ring-white/10">
              {anime.duration} min
            </span>
          ) : null}
        </div>

        {upcoming && (
          <p className="flex items-center gap-1.5 text-[11px] font-medium text-emerald-300">
            <ClockIcon />
            {upcoming}
          </p>
        )}

        {synopsis && (
          <p
            className="line-clamp-4 border-l-2 pl-3 text-[12.5px] leading-relaxed text-base-300"
            style={{ borderColor: hue }}
          >
            {synopsis}
          </p>
        )}

        {rows.length > 0 && (
          <dl className="grid grid-cols-[4.25rem_1fr] gap-x-2 gap-y-1 text-[11px]">
            {rows.map((r) => (
              <div key={r.label} className="contents">
                <dt className="font-semibold tracking-wider text-base-500 uppercase">{r.label}</dt>
                <dd className="truncate font-medium" style={{ color: hue }}>
                  {r.value}
                </dd>
              </div>
            ))}
          </dl>
        )}

        {!!anime.progress && anime.progress > 0 && (
          <div>
            <p className="mb-1 text-[11px] text-base-400">
              Watched {anime.progress}
              {total ? ` of ${total}${anime.episodes ? '' : ' out'}` : ''}
            </p>
            <ProgressBar value={total ? (anime.progress / total) * 100 : 0} />
          </div>
        )}

        <div className="flex items-stretch gap-1.5 pt-1">
          <div className="flex overflow-hidden rounded-lg ring-1 ring-white/10">
            {anime.malId ? (
              <ExternalChip href={`https://myanimelist.net/anime/${anime.malId}`} label="MAL" />
            ) : null}
            {anime.id > 0 && <ExternalChip href={`https://anilist.co/anime/${anime.id}`} label="AL" />}
          </div>
          <Link
            to={`/anime/${anime.id}`}
            className="flex-1 rounded-lg bg-white/5 px-2 py-2 text-center text-xs font-semibold text-base-100 ring-1 ring-white/10 transition-colors hover:bg-white/10"
          >
            Details
          </Link>
          {anime.play && (
            <Link
              to={anime.play.to}
              title={anime.play.label}
              aria-label={anime.play.label}
              className="flex items-center gap-1.5 rounded-lg px-3 text-xs font-bold text-white shadow-lg transition-[transform,filter] hover:scale-[1.03] hover:brightness-110"
              style={{ background: 'var(--color-accent-500)', boxShadow: `0 6px 18px -6px ${glow}` }}
            >
              <PlayGlyph />
              {anime.play.label}
            </Link>
          )}
        </div>
      </div>
    </div>
  )
}

function ExternalChip({ href, label }: { href: string; label: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="grid place-items-center px-2.5 text-[10px] font-extrabold tracking-wider text-base-300 transition-colors not-last:border-r not-last:border-white/10 hover:bg-white/10 hover:text-white"
    >
      {label}
    </a>
  )
}

function StarIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-3" aria-hidden>
      <path d="m12 3 2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9L12 3Z" fill="currentColor" />
    </svg>
  )
}

function LayersIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-3" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
      <path d="m12 3 9 5-9 5-9-5 9-5Z" strokeLinejoin="round" />
      <path d="m3 13 9 5 9-5" strokeLinejoin="round" />
    </svg>
  )
}

function ClockIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-3" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" strokeLinecap="round" />
    </svg>
  )
}

function PlayGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-3.5" aria-hidden>
      <path d="M8 5.5v13l11-6.5-11-6.5Z" fill="currentColor" />
    </svg>
  )
}
