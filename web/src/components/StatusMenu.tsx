import { useCallback, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { LIST_STATUSES, type ListStatus } from '../lib/api'
import { useSetStatus } from '../lib/queries'
import { cx } from '../lib/format'
import { useDismiss } from './ui'

/**
 * The bookmark control. On the sites this is modelled on the bookmark button
 * is the list tag: pressing it offers watching, completed, on hold and the
 * rest, rather than being a separate flag that syncs nowhere.
 */
export function StatusMenu({
  animeId,
  current,
  compact,
  onDone,
}: {
  animeId: number
  current?: string | null
  compact?: boolean
  onDone?: () => void
}) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)
  const trigger = useRef<HTMLButtonElement>(null)
  const setStatus = useSetStatus()

  const active = LIST_STATUSES.find((s) => s.value === current)

  // A portal, because this sits inside cards and a banner that clip their
  // contents: anchored normally the list was cut off after two rows.
  const box = trigger.current?.getBoundingClientRect()
  const menu = box && {
    top: Math.min(box.bottom + 6, window.innerHeight - 240),
    left: Math.max(8, Math.min(box.right - 176, window.innerWidth - 184)),
  }

  return (
    <div className="relative" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={active ? `Listed as ${active.label}` : 'Add to list'}
        onClick={(e) => {
          e.preventDefault()
          e.stopPropagation()
          setOpen((v) => !v)
        }}
        className={cx(
          'flex items-center gap-1.5 rounded-md font-medium transition-colors',
          compact ? 'size-8 justify-center' : 'px-3 py-1.5 text-sm',
          active
            ? 'bg-accent-500/20 text-accent-400 hover:bg-accent-500/30'
            : 'bg-base-800/90 text-base-200 hover:bg-base-700',
        )}
      >
        <BookmarkIcon filled={!!active} />
        {!compact && <span>{active?.label ?? 'Add to list'}</span>}
      </button>

      {open && menu && createPortal(
        <div
          role="menu"
          data-portal-menu
          style={{ top: menu.top, left: menu.left }}
          className="fixed z-50 w-44 origin-top-right animate-rise overflow-hidden rounded-lg border border-base-700 bg-base-850 py-1 shadow-xl shadow-black/50"
        >
          {LIST_STATUSES.map((status) => (
            <button
              key={status.value}
              role="menuitem"
              disabled={setStatus.isPending}
              onClick={(e) => {
                e.preventDefault()
                e.stopPropagation()
                setStatus.mutate(
                  { animeId, status: status.value as ListStatus },
                  { onSettled: () => { setOpen(false); onDone?.() } },
                )
              }}
              className={cx(
                'flex w-full items-center justify-between px-3 py-1.5 text-left text-sm transition-colors',
                status.value === current
                  ? 'text-accent-400'
                  : 'text-base-200 hover:bg-base-750',
              )}
            >
              {status.label}
              {status.value === current && <CheckIcon />}
            </button>
          ))}

        </div>,
        document.body,
      )}
    </div>
  )
}

function BookmarkIcon({ filled }: { filled: boolean }) {
  return (
    <svg viewBox="0 0 24 24" className="size-4 shrink-0" aria-hidden>
      <path
        d="M6 4.5A1.5 1.5 0 0 1 7.5 3h9A1.5 1.5 0 0 1 18 4.5v16l-6-4-6 4v-16Z"
        fill={filled ? 'currentColor' : 'none'}
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function CheckIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path
        d="m5 13 4 4L19 7"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
