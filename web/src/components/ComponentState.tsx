import { bytes } from '../lib/format'
import type { SetupProgress } from '../lib/queries'
import { ProgressBar } from './ui'

/** One external program's install state: a command to run by hand, a download under way, or the button. */
export function ComponentState({
  progress,
  present,
  manual,
  version,
  latest,
  onInstall,
  pending,
}: {
  progress?: SetupProgress
  present: boolean
  manual?: string
  version?: string
  latest?: string
  onInstall: () => void
  pending: boolean
}) {
  const update = present && !!latest && !!version && latest !== version
  if (present && progress?.stage !== 'failed' && !update) return null

  // No clean prebuilt binary on this OS: guide to the package manager, which
  // puts it on PATH where kuro looks, rather than a download that would fail.
  if (manual) {
    return (
      <div className="mt-2">
        <p className="mb-1 text-xs text-base-400">Install it, then reload:</p>
        <code className="select-all rounded bg-base-800 px-2 py-1 text-xs text-base-100">{manual}</code>
      </div>
    )
  }

  if (progress && progress.stage !== 'done' && progress.stage !== 'failed') {
    const percent = progress.total > 0 ? (progress.bytes / progress.total) * 100 : 0
    return (
      <div className="mt-2">
        <p className="mb-1 text-xs text-base-400">
          {progress.stage === 'downloading'
            ? `Downloading ${bytes(progress.bytes)}${progress.total > 0 ? ` of ${bytes(progress.total)}` : ''}`
            : progress.stage === 'extracting'
              ? 'Extracting…'
              : 'Looking up the current version…'}
        </p>
        {percent > 0 && <ProgressBar value={percent} />}
      </div>
    )
  }

  return (
    <div className="mt-2 flex items-center gap-2">
      <button
        onClick={onInstall}
        disabled={pending}
        className="rounded-md bg-base-800 px-3 py-1 text-xs font-medium text-base-100 hover:bg-base-750 disabled:opacity-50"
      >
        {progress?.stage === 'failed' ? 'Try again' : update ? `Update to ${latest}` : 'Install'}
      </button>
      {progress?.error && (
        <span className="text-xs text-recap" title={progress.error}>
          {progress.error.slice(0, 80)}
        </span>
      )}
    </div>
  )
}
