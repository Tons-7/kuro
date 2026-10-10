import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useDismiss, useModalFocus } from './ui'
import { plainKey, SHORTCUTS, SHOW_SHORTCUTS, usePortalHome } from './keys'

/** Every keyboard shortcut, behind "?": keys nobody is told about are keys nobody uses. */
export function Shortcuts() {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const backdrop = useDismiss<HTMLDivElement>(close)
  const panel = useRef<HTMLDivElement>(null)
  useModalFocus(panel, open)
  const home = usePortalHome()

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (plainKey(e) === '?') setOpen((v) => !v)
    }
    const show = () => setOpen(true)
    window.addEventListener('keydown', onKey)
    window.addEventListener(SHOW_SHORTCUTS, show)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener(SHOW_SHORTCUTS, show)
    }
  }, [])

  if (!open) return null

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" />
      <div ref={backdrop} className="relative w-full max-w-md">
        <div
          ref={panel}
          role="dialog"
          aria-modal="true"
          aria-label="Keyboard shortcuts"
          className="animate-fade-in max-h-[85vh] overflow-y-auto rounded-xl border border-base-800 bg-base-900 p-4 shadow-2xl scrollbar-thin"
        >
          <div className="mb-3 flex items-center justify-between gap-4">
            <h2 className="text-base font-semibold text-base-100">Keyboard shortcuts</h2>
            <button
              onClick={close}
              aria-label="Close"
              className="grid size-7 place-items-center rounded-md text-base-400 transition-colors hover:bg-base-800 hover:text-white"
            >
              ✕
            </button>
          </div>

          {SHORTCUTS.map((group) => (
            <section key={group.where} className="mt-3 first:mt-0">
              <h3 className="mb-1 text-[11px] font-semibold tracking-wider text-base-400 uppercase">{group.where}</h3>
              <dl className="divide-y divide-base-800">
                {group.items.map((s) => (
                  <div key={s.does} className="flex items-center justify-between gap-4 py-1.5">
                    <dt className="text-sm text-base-200">{s.does}</dt>
                    <dd className="flex shrink-0 gap-1">
                      {s.keys.map((k) => (
                        <kbd
                          key={k}
                          className="min-w-7 rounded-md bg-base-800 px-1.5 py-0.5 text-center font-sans text-xs text-base-100 ring-1 ring-white/10"
                        >
                          {k}
                        </kbd>
                      ))}
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      </div>
    </div>,
    home,
  )
}
