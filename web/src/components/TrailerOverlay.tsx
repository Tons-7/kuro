import { useCallback, useEffect } from 'react'
import { createPortal } from 'react-dom'

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
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [close])

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Trailer"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/85 p-4 backdrop-blur-sm"
      onClick={close}
    >
      <div className="relative w-full max-w-4xl" onClick={(e) => e.stopPropagation()}>
        <button
          onClick={close}
          aria-label="Close trailer"
          className="absolute -top-10 right-0 rounded-md px-2 py-1 text-base-300 hover:bg-base-800 hover:text-white"
        >
          ✕
        </button>
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
