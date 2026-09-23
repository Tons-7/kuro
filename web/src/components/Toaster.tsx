import { useEffect, useState } from 'react'
import { cx } from '../lib/format'

interface Toast {
  id: number
  message: string
  tone: 'error' | 'info'
}

// Outside React so a mutation cache, which is not a component, can raise one.
let listeners: Array<(t: Toast) => void> = []
let next = 1

export function toast(message: string, tone: Toast['tone'] = 'error') {
  const t = { id: next++, message, tone }
  for (const l of listeners) l(t)
}

/**
 * Where an action's failure is said when its own screen has no place for it:
 * most buttons used to fail with nothing on screen at all.
 */
export function Toaster() {
  const [toasts, setToasts] = useState<Toast[]>([])

  useEffect(() => {
    const add = (t: Toast) => {
      // The same failure twice in a row is one message.
      setToasts((prev) => (prev.some((p) => p.message === t.message) ? prev : [...prev.slice(-2), t]))
      window.setTimeout(() => setToasts((prev) => prev.filter((p) => p.id !== t.id)), 6000)
    }
    listeners.push(add)
    return () => {
      listeners = listeners.filter((l) => l !== add)
    }
  }, [])

  return (
    <div
      aria-live="polite"
      className="pointer-events-none fixed right-4 bottom-4 z-[90] flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-2"
    >
      {toasts.map((t) => (
        <div
          key={t.id}
          role={t.tone === 'error' ? 'alert' : 'status'}
          className={cx(
            'pointer-events-auto flex animate-rise items-start gap-3 rounded-xl bg-base-850/95 px-4 py-3 text-sm shadow-lift ring-1 backdrop-blur-xl',
            t.tone === 'error' ? 'ring-recap/40' : 'ring-white/10',
          )}
        >
          <span
            className={cx('mt-1.5 size-2 shrink-0 rounded-full', t.tone === 'error' ? 'bg-recap' : 'bg-accent-400')}
          />
          <p className="min-w-0 flex-1 text-base-100">{t.message}</p>
          <button
            onClick={() => setToasts((prev) => prev.filter((p) => p.id !== t.id))}
            aria-label="Dismiss"
            className="-mr-1 rounded px-1 text-base-500 hover:text-white"
          >
            ✕
          </button>
        </div>
      ))}
    </div>
  )
}
