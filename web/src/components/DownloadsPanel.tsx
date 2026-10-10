import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { copyText, downloadLabel, downloadProgress, downloadsReport, sortDownloads } from '../lib/downloads'
import { cx } from '../lib/format'
import { useDownloadQueue, useDownloads, type Download, type Queued } from '../lib/queries'
import { ProgressBar, useDismiss } from './ui'
import { plainKey, usePortalHome } from './keys'

/**
 * The downloads at a glance, over whatever is on screen: opening the page would stop the episode
 * playing. Looking only, so a stray key while watching removes nothing. D opens and closes it.
 */
export function DownloadsPanel() {
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)
  const trigger = useRef<HTMLButtonElement>(null)
  const home = usePortalHome()

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (plainKey(e) === 'd') setOpen((v) => !v)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const downloads = useDownloads()
  const queue = useDownloadQueue()
  const sections = sortDownloads(downloads.data?.items ?? [], queue.data?.items ?? [])
  const { active, held, finished, searching, waitingQueue, failed } = sections
  const busy = active.length + searching.length

  const copy = async () => {
    setCopied(await copyText(downloadsReport(sections), home))
    window.setTimeout(() => setCopied(false), 2000)
  }

  // Under its button, or the corner when fullscreen hides the button.
  const box = home === document.body ? trigger.current?.getBoundingClientRect() : undefined

  return (
    <div className="relative shrink-0" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={busy ? `Downloads, ${busy} in progress` : 'Downloads'}
        title="Downloads (D)"
        onClick={() => setOpen((v) => !v)}
        className="relative grid size-9 place-items-center rounded-md text-base-300 transition-colors hover:bg-base-800 hover:text-white"
      >
        <ArrowIcon />
        {busy > 0 && (
          <span className="absolute top-1.5 right-1.5 size-2 rounded-full bg-accent-500 ring-2 ring-base-950" />
        )}
      </button>

      {open &&
        createPortal(
          <div
            role="dialog"
            aria-label="Downloads"
            data-portal-menu
            style={{
              top: box ? box.bottom + 8 : 16,
              right: box ? Math.max(8, window.innerWidth - box.right) : 16,
            }}
            className="fixed z-50 w-96 max-w-[calc(100vw-1rem)] animate-rise overflow-hidden rounded-xl border border-base-750 bg-base-850 shadow-panel"
          >
            <div className="flex items-center justify-between gap-2 border-b border-base-750 px-3 py-2">
              <p className="text-sm font-medium text-base-100">Downloads</p>
              <button
                onClick={copy}
                title="Copy this list as text, to paste into a report"
                className="rounded px-1.5 py-0.5 text-[11px] text-base-400 transition-colors hover:bg-base-800 hover:text-white"
              >
                {copied ? 'Copied' : 'Copy details'}
              </button>
            </div>

            {busy + held.length + waitingQueue.length + failed.length === 0 ? (
              <p className="px-3 py-6 text-center text-xs text-base-500">Nothing is downloading.</p>
            ) : (
              <div className="max-h-[60vh] overflow-y-auto scrollbar-thin">
                <Section label="Downloading" count={busy}>
                  {searching.map((q) => (
                    <QueuedRow key={`q-${q.animeId}-${q.epKey}`} q={q} note="Finding a release…" />
                  ))}
                  {active.map((d) => (
                    <DownloadRow key={d.infoHash} d={d} />
                  ))}
                </Section>
                <Section label="Waiting" count={held.length + waitingQueue.length}>
                  {held.map((d) => (
                    <DownloadRow key={d.infoHash} d={d} />
                  ))}
                  {waitingQueue.map((q) => (
                    <QueuedRow key={`q-${q.animeId}-${q.epKey}`} q={q} />
                  ))}
                </Section>
                <Section label="Failed" count={failed.length}>
                  {failed.map((q) => (
                    <QueuedRow key={`q-${q.animeId}-${q.epKey}`} q={q} note={q.error} failed />
                  ))}
                </Section>
              </div>
            )}

            <Link
              to="/downloads"
              onClick={close}
              className="block border-t border-base-750 px-3 py-2 text-center text-xs text-base-400 transition-colors hover:bg-base-800 hover:text-white"
            >
              {finished.length > 0 ? `${finished.length} on disk · open Downloads` : 'Open Downloads'}
            </Link>
          </div>,
          home,
        )}
    </div>
  )
}

function Section({ label, count, children }: { label: string; count: number; children: React.ReactNode }) {
  if (count === 0) return null
  return (
    <section>
      <p className="flex justify-between bg-base-900/60 px-3 py-1 text-[11px] font-semibold tracking-wider text-base-400 uppercase">
        {label}
        <span className="font-normal text-base-600">{count}</span>
      </p>
      <ul className="divide-y divide-base-800">{children}</ul>
    </section>
  )
}

function DownloadRow({ d }: { d: Download }) {
  return (
    <li className="px-3 py-2">
      <p className="truncate text-xs font-medium text-base-100" title={d.name}>
        {downloadLabel(d)}
        {d.pinned && <span className="ml-1.5 font-normal text-accent-300">playing</span>}
      </p>
      <p className="mt-0.5 truncate text-[11px] text-base-500">{downloadProgress(d)}</p>
      <ProgressBar value={d.percent} className="mt-1.5" />
    </li>
  )
}

function QueuedRow({ q, note, failed }: { q: Queued; note?: string; failed?: boolean }) {
  return (
    <li className="px-3 py-2">
      <p className="truncate text-xs font-medium text-base-100">
        {q.title ?? `Anime ${q.animeId}`} episode {q.episode}
      </p>
      {note && <p className={cx('mt-0.5 truncate text-[11px]', failed ? 'text-recap' : 'text-base-500')}>{note}</p>}
    </li>
  )
}

function ArrowIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path
        d="M12 4v10m0 0 4-4m-4 4-4-4M5 18h14"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
