import { useCallback, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useQuery } from '@tanstack/react-query'
import { api, query } from '../lib/api'
import { cx } from '../lib/format'
import { useDebounced, useDismiss } from './ui'

interface Studio {
  id: number
  name: string
}

/** Browse by the studio that made it; there are too many to list, so it searches. */
export function StudioFilter({
  value,
  onChange,
}: {
  value?: Studio
  onChange: (studio?: Studio) => void
}) {
  const [open, setOpen] = useState(false)
  const [term, setTerm] = useState('')
  const search = useDebounced(term.trim(), 250)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)
  const trigger = useRef<HTMLButtonElement>(null)
  const box = trigger.current?.getBoundingClientRect()

  const found = useQuery({
    enabled: open && search.length >= 2,
    queryKey: ['studios', search],
    queryFn: ({ signal }) =>
      api.get<{ studios: Studio[] }>(`/api/studios${query({ q: search })}`, signal),
    staleTime: 60 * 60_000,
  })

  // A pill like the other filters; the chip row below is where it is removed.
  return (
    <div className="relative" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className={cx(
          'flex items-center gap-1.5 rounded-full px-3.5 py-1.5 text-sm font-medium ring-1 transition-colors',
          value
            ? 'bg-accent-500/15 text-accent-200 ring-accent-500/40'
            : 'bg-base-850 text-base-200 ring-white/[0.07] hover:bg-base-800 hover:text-white',
        )}
      >
        {value ? value.name : 'Studio'}
        <svg viewBox="0 0 24 24" className="size-3 opacity-70" aria-hidden>
          <path d="m6 9 6 6 6-6" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
      </button>
      {open &&
        box &&
        createPortal(
          <div
            data-portal-menu
            style={{ top: box.bottom + 6, left: Math.max(8, Math.min(box.left, window.innerWidth - 264)) }}
            className="fixed z-50 w-64 animate-rise rounded-xl border border-base-750 bg-base-850 p-2 shadow-panel"
          >
            <input
              autoFocus
              value={term}
              onChange={(e) => setTerm(e.target.value)}
              placeholder="Search studios…"
              className="w-full rounded-md border border-base-750 bg-base-950 px-2 py-1.5 text-sm text-base-100 outline-none focus:border-accent-500"
            />
            <ul role="listbox" className="mt-1.5 max-h-64 overflow-y-auto scrollbar-thin">
              {search.length < 2 ? (
                <li className="px-2 py-1.5 text-xs text-base-500">Type a name, e.g. MAPPA</li>
              ) : found.isPending ? (
                <li className="px-2 py-1.5 text-xs text-base-500">Searching…</li>
              ) : (found.data?.studios ?? []).length === 0 ? (
                <li className="px-2 py-1.5 text-xs text-base-500">No studio by that name</li>
              ) : (
                found.data!.studios.map((s) => (
                  <li key={s.id}>
                    <button
                      role="option"
                      aria-selected={false}
                      onClick={() => {
                        onChange(s)
                        setOpen(false)
                        setTerm('')
                      }}
                      className={cx('w-full rounded-lg px-2.5 py-1.5 text-left text-sm text-base-200 hover:bg-base-800')}
                    >
                      {s.name}
                    </button>
                  </li>
                ))
              )}
            </ul>
          </div>,
          document.body,
        )}
    </div>
  )
}
