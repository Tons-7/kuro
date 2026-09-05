import { useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, query, type ReleaseCandidate } from '../lib/api'
import { bytes, cx } from '../lib/format'
import { Badge, Skeleton, useDismiss } from './ui'

/** Every release the finder saw, best first; ineligible ones greyed with the reason. */
export function ReleasePicker({
  animeId,
  episode,
  current,
  onPick,
  onClose,
}: {
  animeId: number
  episode: number
  /** Info hash of what is playing now, to mark the row. */
  current?: string
  onPick: (infoHash: string) => void
  onClose: () => void
}) {
  const close = useCallback(() => onClose(), [onClose])
  const panel = useDismiss<HTMLDivElement>(close)

  const sources = useQuery({
    queryKey: ['sources', animeId, episode],
    queryFn: () =>
      api.get<{ results: ReleaseCandidate[]; queries: string[] }>(
        `/api/episode/sources${query({ id: animeId, episode })}`,
      ),
    staleTime: 5 * 60_000,
  })

  const results = sources.data?.results ?? []

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label="Choose a release"
        className="animate-fade-in relative flex max-h-[85vh] w-full max-w-2xl flex-col rounded-xl border border-base-800 bg-base-900 shadow-2xl"
      >
        <div className="flex items-start justify-between gap-4 border-b border-base-850 p-4">
          <div>
            <h2 className="text-base font-semibold text-white">Choose a release</h2>
            <p className="mt-0.5 text-xs text-base-500">
              Episode {episode} · best first. Picking one plays it now; the choice is not remembered.
            </p>
          </div>
          <button
            onClick={close}
            aria-label="Close"
            className="rounded-md px-2 py-1 text-base-400 hover:bg-base-800 hover:text-base-100"
          >
            ✕
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto p-2 scrollbar-thin">
          {sources.isPending ? (
            <div className="space-y-2 p-2">
              {Array.from({ length: 5 }, (_, i) => (
                <Skeleton key={i} className="h-16 w-full" />
              ))}
            </div>
          ) : sources.isError ? (
            <p className="p-4 text-sm text-recap">{(sources.error as Error).message}</p>
          ) : results.length === 0 ? (
            <p className="p-6 text-center text-sm text-base-500">
              No release found under any of the show's titles.
            </p>
          ) : (
            <ul className="space-y-1">
              {results.map((r) => (
                <Row
                  key={r.Torrent.infoHash}
                  candidate={r}
                  active={r.Torrent.infoHash === current}
                  onPick={() => onPick(r.Torrent.infoHash)}
                />
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}

function Row({
  candidate: r,
  active,
  onPick,
}: {
  candidate: ReleaseCandidate
  active: boolean
  onPick: () => void
}) {
  const rel = r.Release
  const specs = [
    rel.Resolution,
    rel.Source,
    rel.Codec && `${rel.Codec}${rel.BitDepth === 10 ? ' 10-bit' : ''}`,
    rel.DualAudio && 'dual audio',
    rel.Batch && 'batch',
  ].filter(Boolean)

  return (
    <li>
      <button
        onClick={onPick}
        title={r.reasons?.join('\n')}
        className={cx(
          'block w-full rounded-lg border px-3 py-2 text-left transition-colors',
          active
            ? 'border-accent-500/50 bg-accent-500/10'
            : 'border-base-850 hover:bg-base-850',
          r.blocked && 'opacity-60',
        )}
      >
        <p className="line-clamp-2 text-sm text-base-100">{r.Torrent.title}</p>
        <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-base-400">
          {rel.Group && <span className="text-base-200">{rel.Group}</span>}
          {specs.map((s) => (
            <span key={String(s)}>{s}</span>
          ))}
          <span>{bytes(r.Torrent.size)}</span>
          {r.Torrent.seedersKnown === false ? (
            <span>peers unknown</span>
          ) : (
            <span className={cx(r.Torrent.seeders === 0 && 'text-recap')}>
              {r.Torrent.seeders} seeders
            </span>
          )}
          <span className="ml-auto tabular-nums text-base-500">{Math.round(r.score)}</span>
          {active && <Badge tone="accent">Playing</Badge>}
          {r.SeaDexBest && <Badge tone="accent">SeaDex</Badge>}
          {!r.playable && <Badge tone="filler">Software transcode</Badge>}
          {r.blocked && <Badge tone="recap">{r.blocked}</Badge>}
        </div>
      </button>
    </li>
  )
}
