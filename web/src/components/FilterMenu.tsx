import { useCallback, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { cx } from '../lib/format'
import { useDismiss } from './ui'

/**
 * A multi-value filter; a native select holds only one and ignores the design.
 * A portal so a scrolling filter bar can't clip it; the trigger shows the count.
 */
export function FilterMenu({
  label,
  values,
  options,
  labels,
  multiple = true,
  onChange,
}: {
  label: string
  values: string[]
  options: string[]
  labels?: Record<string, string>
  multiple?: boolean
  onChange: (values: string[]) => void
}) {
  const [open, setOpen] = useState(false)
  const [term, setTerm] = useState('')
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)
  const trigger = useRef<HTMLButtonElement>(null)

  const pretty = (option: string) => labels?.[option] ?? option.replace(/_/g, ' ').toLowerCase()
  const box = trigger.current?.getBoundingClientRect()

  // A long list in one narrow column has to be scrolled to be read. Two
  // columns puts twenty genres on screen at once, which is the whole list.
  const wide = options.length > 12
  const shown = term
    ? options.filter((o) => pretty(o).toLowerCase().includes(term.toLowerCase()))
    : options

  const toggle = (option: string) => {
    if (!multiple) {
      onChange(values[0] === option ? [] : [option])
      setOpen(false)
      return
    }
    onChange(
      values.includes(option) ? values.filter((v) => v !== option) : [...values, option],
    )
  }

  return (
    <div className="relative" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className={cx(
          'flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-sm transition-colors',
          values.length > 0
            ? 'border-accent-500/50 bg-accent-500/10 text-accent-300'
            : 'border-base-800 bg-base-900 text-base-300 hover:border-base-700 hover:text-base-100',
        )}
      >
        <span className="capitalize">
          {values.length === 1 ? pretty(values[0]) : label}
        </span>
        {values.length > 1 && (
          <span className="rounded bg-accent-500/25 px-1 text-[11px] tabular-nums">
            {values.length}
          </span>
        )}
        <svg viewBox="0 0 24 24" className="size-3.5 opacity-60" aria-hidden>
          <path d="m6 9 6 6 6-6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </button>

      {open && box &&
        createPortal(
          <div
            role="listbox"
            data-portal-menu
            style={{
              top: Math.min(box.bottom + 6, window.innerHeight - 360),
              left: Math.max(
                8,
                Math.min(box.left, window.innerWidth - (wide ? 460 : 248)),
              ),
            }}
            className={cx(
              'fixed z-50 animate-rise rounded-xl border border-base-750 bg-base-850 p-1.5 shadow-panel',
              wide ? 'w-[27rem]' : 'w-60',
            )}
          >
            {wide && (
              <input
                autoFocus
                value={term}
                onChange={(e) => setTerm(e.target.value)}
                placeholder={`Filter ${label.toLowerCase()}…`}
                className="mb-1.5 w-full rounded-lg bg-base-900 px-2.5 py-1.5 text-sm text-base-100 placeholder:text-base-500 focus:outline-none"
              />
            )}

            {values.length > 0 && (
              <button
                onClick={() => onChange([])}
                className="mb-1 w-full rounded-lg px-2.5 py-1.5 text-left text-xs text-base-400 transition-colors hover:bg-base-800 hover:text-white"
              >
                Clear {label.toLowerCase()}
              </button>
            )}

            <div
              className={cx(
                'max-h-80 overflow-y-auto scrollbar-thin',
                wide && 'grid grid-cols-2 gap-x-1',
              )}
            >
            {shown.map((option) => {
              const on = values.includes(option)
              return (
                <button
                  key={option}
                  role="option"
                  aria-selected={on}
                  onClick={() => toggle(option)}
                  className={cx(
                    'flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-sm capitalize transition-colors',
                    on ? 'bg-accent-500/15 text-accent-200' : 'text-base-200 hover:bg-base-800',
                  )}
                >
                  {multiple && (
                    <span
                      className={cx(
                        'grid size-4 shrink-0 place-items-center rounded border transition-colors',
                        on ? 'border-accent-400 bg-accent-500 text-white' : 'border-base-600',
                      )}
                    >
                      {on && (
                        <svg viewBox="0 0 24 24" className="size-3" aria-hidden>
                          <path
                            d="m5 13 4 4L19 7"
                            fill="none"
                            stroke="currentColor"
                            strokeWidth="3"
                            strokeLinecap="round"
                            strokeLinejoin="round"
                          />
                        </svg>
                      )}
                    </span>
                  )}
                  {pretty(option)}
                </button>
              )
            })}
            {shown.length === 0 && (
              <p className="col-span-2 px-2.5 py-3 text-center text-xs text-base-500">
                Nothing matches “{term}”.
              </p>
            )}
            </div>
          </div>,
          document.body,
        )}
    </div>
  )
}
