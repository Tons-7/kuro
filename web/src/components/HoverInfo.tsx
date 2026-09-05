import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { cx, tint } from '../lib/format'
import { Badge, ProgressBar } from './ui'

export interface HoverAnime {
  id: number
  title: string
  romaji?: string | null
  english?: string | null
  format?: string | null
  status?: string | null
  episodes?: number | null
  seasonYear?: number | null
  score?: number | null
  genres?: string[] | null
  color?: string | null
  description?: string | null
  progress?: number
  /** Set when the card leads somewhere other than the series page. */
  play?: { to: string; label: string }
}

const PANEL_WIDTH = 300
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
        Math.max(GAP, rect.top + rect.height / 2 - 150),
        window.innerHeight - 320,
      )
      setBox({ top, left: Math.max(GAP, left) })
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
  const accent = tint(anime.color, 0.5)
  const meta = [
    anime.format?.replace('_', ' '),
    anime.seasonYear ? String(anime.seasonYear) : null,
    anime.episodes ? `${anime.episodes} ep` : null,
  ].filter(Boolean)

  return (
    <div
      onMouseEnter={onEnter}
      onMouseLeave={onLeave}
      style={{ top, left, width: PANEL_WIDTH, borderColor: accent }}
      className="fixed z-50 animate-rise rounded-xl border border-base-750 bg-base-900 p-3 shadow-2xl shadow-black/60"
    >
      <p className="line-clamp-2 text-sm font-semibold text-white">
        {anime.english ?? anime.title}
      </p>
      {anime.romaji && anime.romaji !== (anime.english ?? anime.title) && (
        <p className="mt-0.5 line-clamp-1 text-xs text-base-500">{anime.romaji}</p>
      )}

      <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-base-400">
        {meta.map((m) => (
          <span key={m}>{m}</span>
        ))}
        {anime.score ? <span className="text-accent-400">★ {anime.score}</span> : null}
        {anime.status === 'RELEASING' && <Badge tone="accent">Airing</Badge>}
      </div>

      {/* The synopsis is why anyone hovers: it answers "what is this" without
          leaving the row. AniList writes it with HTML in it. */}
      {anime.description && (
        <p className="mt-2 line-clamp-4 text-xs leading-relaxed text-base-400">
          {anime.description.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim()}
        </p>
      )}

      {anime.genres && anime.genres.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1">
          {anime.genres.slice(0, 4).map((g) => (
            <span key={g} className="rounded bg-base-850 px-1.5 py-0.5 text-[10px] text-base-300">
              {g}
            </span>
          ))}
        </div>
      )}

      {!!anime.progress && anime.progress > 0 && (
        <div className="mt-2.5">
          <p className="mb-1 text-[11px] text-base-400">
            Watched {anime.progress}
            {anime.episodes ? ` of ${anime.episodes}` : ''}
          </p>
          <ProgressBar value={anime.episodes ? (anime.progress / anime.episodes) * 100 : 0} />
        </div>
      )}

      {/* The whole point: a card that plays an episode still needs a way to
          reach the show it belongs to. */}
      <div className="mt-3 flex gap-1.5">
        {anime.play && (
          <Link
            to={anime.play.to}
            className="flex-1 rounded-md bg-accent-500/20 px-2 py-1.5 text-center text-xs font-medium text-accent-300 transition-colors hover:bg-accent-500/30"
          >
            {anime.play.label}
          </Link>
        )}
        <Link
          to={`/anime/${anime.id}`}
          className={cx(
            'rounded-md bg-base-800 px-2 py-1.5 text-center text-xs text-base-200 transition-colors hover:bg-base-750',
            anime.play ? 'flex-1' : 'w-full',
          )}
        >
          Series page
        </Link>
      </div>
    </div>
  )
}
