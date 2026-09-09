import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { cx } from '../lib/format'

/** How far the track can scroll, and where it currently sits. */
function useScrollState(track: React.RefObject<HTMLDivElement | null>) {
  const [state, setState] = useState({
    scrollable: false,
    atStart: true,
    atEnd: false,
    progress: 0,
    visible: 1,
  })

  const measure = useCallback(() => {
    const el = track.current
    if (!el) return
    const max = el.scrollWidth - el.clientWidth
    setState({
      // A pixel of slack: sub-pixel widths otherwise report a rail that fits as
      // scrollable, and every short row grew arrows.
      scrollable: max > 1,
      atStart: el.scrollLeft <= 1,
      atEnd: el.scrollLeft >= max - 1,
      progress: max > 1 ? el.scrollLeft / max : 0,
      visible: el.scrollWidth > 0 ? el.clientWidth / el.scrollWidth : 1,
    })
  }, [track])

  useEffect(() => {
    const el = track.current
    if (!el) return
    measure()
    el.addEventListener('scroll', measure, { passive: true })

    const resize = new ResizeObserver(measure)
    resize.observe(el)
    // Cards replacing skeletons change scrollWidth without touching the
    // track's own box, so a rail kept arrows it no longer needed.
    const swap = new MutationObserver(measure)
    swap.observe(el, { childList: true, subtree: true })

    return () => {
      el.removeEventListener('scroll', measure)
      resize.disconnect()
      swap.disconnect()
    }
  }, [measure, track])

  return { ...state, measure }
}

/**
 * Horizontal strip of cards. The arrows only appear on pointer devices; touch
 * users swipe, and a floating button over the art would just be in the way.
 */
export function Rail({
  title,
  more,
  action,
  children,
}: {
  title: string
  more?: { to: string; label?: string }
  /** A control beside the arrows, where "more" would be a link to a page. */
  action?: ReactNode
  children: ReactNode
}) {
  const track = useRef<HTMLDivElement>(null)
  const { scrollable, atStart, atEnd, progress, visible } = useScrollState(track)

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
          {action}
          {/* Only where there is something to scroll to: a row of six films
              that already fits grew arrows that did nothing. */}
          {scrollable && (
            <div className="hidden gap-1 md:flex">
              <ArrowButton direction={-1} disabled={atStart} onClick={() => scrollBy(-1)} />
              <ArrowButton direction={1} disabled={atEnd} onClick={() => scrollBy(1)} />
            </div>
          )}
        </div>
      </div>

      <div
        ref={track}
        className="no-scrollbar -mx-1 flex snap-x snap-mandatory gap-3 overflow-x-auto scroll-smooth px-1 pb-1"
      >
        {children}
      </div>

      {/* How far along the row you are, and that there is more of it. The thumb
          is the share on screen, so a long row reads as a short thumb. */}
      {scrollable && (
        <div className="mt-1.5 h-0.5 overflow-hidden rounded-full bg-base-850" aria-hidden>
          <div
            className="h-full rounded-full bg-base-700"
            style={{
              width: `${Math.max(visible * 100, 8)}%`,
              marginInlineStart: `${progress * (100 - Math.max(visible * 100, 8))}%`,
            }}
          />
        </div>
      )}
    </section>
  )
}

export function RailItem({ children }: { children: ReactNode }) {
  return (
    <div className="w-[38vw] shrink-0 snap-start sm:w-40 md:w-44 lg:w-[11.5rem]">{children}</div>
  )
}

function ArrowButton({
  direction,
  disabled,
  onClick,
}: {
  direction: 1 | -1
  disabled?: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={direction === 1 ? 'Scroll right' : 'Scroll left'}
      // Always there once a rail can scroll: appearing on hover left a hole
      // beside "See all" and made the row look broken until you moved onto it.
      className="grid size-8 place-items-center rounded-full bg-base-850/80 text-base-300 shadow-card backdrop-blur-sm transition-colors hover:bg-base-750 hover:text-white disabled:pointer-events-none disabled:opacity-30"
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
