import { useRef, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { cx } from '../lib/format'

/**
 * Horizontal strip of cards. The arrows only appear on pointer devices; touch
 * users swipe, and a floating button over the art would just be in the way.
 */
export function Rail({
  title,
  more,
  children,
}: {
  title: string
  more?: { to: string; label?: string }
  children: ReactNode
}) {
  const track = useRef<HTMLDivElement>(null)

  const scrollBy = (direction: 1 | -1) => {
    const el = track.current
    if (!el) return
    el.scrollBy({ left: direction * Math.round(el.clientWidth * 0.8), behavior: 'smooth' })
  }

  return (
    <section className="group/rail">
      <div className="mb-3 flex items-center justify-between gap-4">
        {/* Big enough to read from across the row it labels. Uppercase grey at
            11px is legible only if you go looking for it. */}
        <h2 className="section-title">{title}</h2>
        <div className="flex items-center gap-2">
          {more && (
            <Link
              to={more.to}
              className="flex items-center gap-1 rounded-md px-2 py-1 text-sm text-base-400 transition-colors hover:bg-base-850 hover:text-white"
            >
              {more.label ?? 'See all'}
              <svg viewBox="0 0 24 24" className="size-3.5" aria-hidden>
                <path
                  d="m9 5 7 7-7 7"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            </Link>
          )}
          <div className="hidden gap-1 md:flex">
            <ArrowButton direction={-1} onClick={() => scrollBy(-1)} />
            <ArrowButton direction={1} onClick={() => scrollBy(1)} />
          </div>
        </div>
      </div>

      <div
        ref={track}
        className="no-scrollbar -mx-1 flex snap-x snap-mandatory gap-3 overflow-x-auto scroll-smooth px-1 pb-1"
      >
        {children}
      </div>
    </section>
  )
}

export function RailItem({ children }: { children: ReactNode }) {
  return (
    <div className="w-[38vw] shrink-0 snap-start sm:w-40 md:w-44 lg:w-[11.5rem]">{children}</div>
  )
}

function ArrowButton({ direction, onClick }: { direction: 1 | -1; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={direction === 1 ? 'Scroll right' : 'Scroll left'}
      className="grid size-8 place-items-center rounded-full bg-base-850/80 text-base-300 opacity-0 shadow-card backdrop-blur-sm transition-all hover:bg-base-750 hover:text-white group-hover/rail:opacity-100 focus-visible:opacity-100"
    >
      <svg viewBox="0 0 24 24" className={cx('size-4', direction === -1 && 'rotate-180')} aria-hidden>
        <path
          d="m9 5 7 7-7 7"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </button>
  )
}
