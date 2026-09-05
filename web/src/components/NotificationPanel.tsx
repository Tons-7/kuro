import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { cx, relativeTime } from '../lib/format'
import { useNotifications, type Notification } from '../lib/queries'
import { useDismiss } from './ui'

/**
 * New episodes read as a glance, not a destination. The panel does the whole
 * job in place, linking out only when there is more.
 */
export function NotificationPanel() {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)
  const trigger = useRef<HTMLButtonElement>(null)

  const qc = useQueryClient()
  const { data } = useNotifications()
  const unread = data?.unread ?? 0
  const items = (data?.items ?? []).slice(0, 8)
  useDesktopNotifications(data?.items)

  const refresh = () => qc.invalidateQueries({ queryKey: ['notifications'] })
  const markRead = useMutation({
    mutationFn: (id?: number) => api.post(`/api/notifications/read${id ? `?id=${id}` : ''}`),
    onSuccess: refresh,
  })
  const remove = useMutation({
    mutationFn: (id?: number) => api.del(`/api/notifications${id ? `?id=${id}` : ''}`),
    onSuccess: refresh,
  })

  const box = trigger.current?.getBoundingClientRect()

  return (
    <div className="relative shrink-0" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={unread ? `${unread} unread notifications` : 'Notifications'}
        onClick={() => setOpen((v) => !v)}
        className="relative grid size-9 place-items-center rounded-md text-base-300 transition-colors hover:bg-base-800 hover:text-white"
      >
        <BellIcon />
        {unread > 0 && (
          <span className="absolute top-1.5 right-1.5 size-2 rounded-full bg-accent-500 ring-2 ring-base-950" />
        )}
      </button>

      {open && box &&
        createPortal(
          <div
            data-portal-menu
            style={{
              top: box.bottom + 8,
              right: Math.max(8, window.innerWidth - box.right),
            }}
            className="fixed z-50 w-80 animate-rise overflow-hidden rounded-xl border border-base-750 bg-base-850 shadow-panel"
          >
            <div className="flex items-center justify-between gap-2 border-b border-base-750 px-3 py-2">
              <p className="text-sm font-medium text-base-100">
                New episodes
                {unread > 0 && (
                  <span className="ml-1.5 rounded bg-accent-500/25 px-1.5 text-[11px] text-accent-200">
                    {unread}
                  </span>
                )}
              </p>
              <div className="flex items-center gap-1">
                {unread > 0 && (
                  <button
                    onClick={() => markRead.mutate(undefined)}
                    className="rounded px-1.5 py-0.5 text-[11px] text-base-400 transition-colors hover:bg-base-800 hover:text-white"
                  >
                    Mark all read
                  </button>
                )}
                {items.length > 0 && (
                  <button
                    onClick={() => remove.mutate(undefined)}
                    className="rounded px-1.5 py-0.5 text-[11px] text-base-400 transition-colors hover:bg-base-800 hover:text-recap"
                  >
                    Clear
                  </button>
                )}
              </div>
            </div>

            {items.length === 0 ? (
              <p className="px-3 py-6 text-center text-xs text-base-500">
                Nothing new. Follow a show and its episodes turn up here.
              </p>
            ) : (
              <ul className="max-h-80 divide-y divide-base-800 overflow-y-auto scrollbar-thin">
                {items.map((item) => (
                  <Row
                    key={item.id}
                    item={item}
                    onOpen={() => {
                      markRead.mutate(item.id)
                      setOpen(false)
                    }}
                    onRemove={() => remove.mutate(item.id)}
                  />
                ))}
              </ul>
            )}

            <Link
              to="/notifications"
              onClick={close}
              className="block border-t border-base-750 px-3 py-2 text-center text-xs text-base-400 transition-colors hover:bg-base-800 hover:text-white"
            >
              See all
            </Link>
          </div>,
          document.body,
        )}
    </div>
  )
}

function Row({
  item,
  onOpen,
  onRemove,
}: {
  item: Notification
  onOpen: () => void
  onRemove: () => void
}) {
  return (
    <li className="group/row relative">
      <Link
        to={notificationTarget(item)}
        onClick={onOpen}
        className={cx(
          'block px-3 py-2 pr-8 transition-colors hover:bg-base-800',
          !item.read && 'bg-accent-500/[0.06]',
        )}
      >
        <div className="flex items-start gap-2">
          {!item.read && <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-accent-500" />}
          <div className="min-w-0">
            <p className="line-clamp-1 text-xs font-medium text-base-100">{item.title}</p>
            <p className="mt-0.5 line-clamp-1 text-[11px] text-base-500">
              {item.body} · {relativeTime(item.createdAt)}
            </p>
          </div>
        </div>
      </Link>

      <button
        onClick={onRemove}
        aria-label="Remove notification"
        className="absolute top-2 right-2 rounded p-1 text-base-600 opacity-0 transition-opacity group-hover/row:opacity-100 hover:text-recap focus-visible:opacity-100"
      >
        <svg viewBox="0 0 24 24" className="size-3" aria-hidden>
          <path d="m6 6 12 12M18 6 6 18" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
      </button>
    </li>
  )
}

export const DESKTOP_NOTIFY_KEY = 'kuro.notify.desktop'
const SEEN_KEY = 'kuro.notify.seen'

export function desktopNotifyWanted(): boolean {
  try {
    return localStorage.getItem(DESKTOP_NOTIFY_KEY) === '1'
  } catch {
    return false
  }
}

// Raises a system notification for each unread item newer than the last one
// this browser announced, when the setting is on and permission was granted.
// The first poll only records where the list stands, so enabling it does not
// replay the backlog.
function useDesktopNotifications(items?: Notification[]) {
  useEffect(() => {
    if (!items || typeof Notification === 'undefined') return
    const newest = items.reduce((m, n) => Math.max(m, n.id), 0)
    let seen = 0
    try {
      seen = Number(localStorage.getItem(SEEN_KEY) ?? '')
    } catch {
      // Unreadable storage: treat as first sight.
    }
    const first = !Number.isFinite(seen) || seen === 0
    if (!first && desktopNotifyWanted() && Notification.permission === 'granted') {
      for (const n of items.filter((n) => n.id > seen && !n.read).reverse()) {
        const note = new Notification(n.title, { body: n.body, tag: `kuro-${n.id}` })
        note.onclick = () => {
          window.focus()
          window.location.assign(notificationTarget(n))
        }
      }
    }
    if (newest > seen || first) {
      try {
        localStorage.setItem(SEEN_KEY, String(newest))
      } catch {
        // Nothing to do; the next poll announces again at worst.
      }
    }
  }, [items])
}

export function notificationTarget(n: Notification): string {
  if (n.kind === 'update') return '/settings?tab=About'
  return n.animeId ? `/watch/${n.animeId}/${n.episode || 1}` : '#'
}

function BellIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path
        d="M6 9a6 6 0 1 1 12 0c0 3.5 1 5 1.5 5.5H4.5C5 14 6 12.5 6 9Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
      <path d="M10 18a2 2 0 0 0 4 0" fill="none" stroke="currentColor" strokeWidth="1.6" />
    </svg>
  )
}
