import { useCallback, useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useModalFocus } from './ui'

/** The YouTube id inside any of the URL shapes AniList and MAL hand out. */
export function youtubeID(url?: string | null): string | undefined {
  if (!url) return undefined
  const m = url.match(/(?:youtu\.be\/|[?&]v=|\/embed\/)([\w-]{6,})/)
  return m?.[1]
}

// The trailer over the page it came from, not a new tab: Escape or the X
// brings the page straight back.
export function TrailerOverlay({ videoId, onClose }: { videoId: string; onClose: () => void }) {
  const close = useCallback(() => onClose(), [onClose])
  const dialog = useRef<HTMLDivElement>(null)
  useModalFocus(dialog)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [close])

  return createPortal(
    <div
      ref={dialog}
      role="dialog"
      aria-modal="true"
      aria-label="Trailer"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/85 p-4 backdrop-blur-sm"
      onClick={close}
    >
      {/* On the screen's corner, not above the video: on a landscape phone the
          video fills the height and anything above it is off-screen. Escape
          stops reaching the page once the player has focus, so this has to be
          always reachable. */}
      <button
        onClick={close}
        aria-label="Close trailer"
        className="fixed top-3 right-3 z-10 grid size-10 place-items-center rounded-full bg-base-900/90 text-lg text-base-100 ring-1 ring-white/15 hover:bg-base-800 hover:text-white"
      >
        ✕
      </button>
      {/* Width capped by the height too, so the whole 16:9 frame fits. */}
      <div
        className="relative w-full max-w-4xl"
        style={{ maxWidth: 'min(56rem, calc((100dvh - 2rem) * 16 / 9))' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="aspect-video overflow-hidden rounded-xl bg-black shadow-2xl">
          <iframe
            title="Trailer"
            src={`https://www.youtube-nocookie.com/embed/${videoId}?autoplay=1&rel=0`}
            allow="autoplay; encrypted-media; fullscreen; picture-in-picture"
            allowFullScreen
            className="size-full"
          />
        </div>
      </div>
    </div>,
    document.body,
  )
}
