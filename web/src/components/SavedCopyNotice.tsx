import { useState, useSyncExternalStore } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { catalogueSource } from '../lib/api'
import { relativeTime } from '../lib/format'

/** Shown while AniList can't be reached and pages come from what kuro saved. */
export function SavedCopyNotice() {
  const savedAt = useSyncExternalStore(catalogueSource.subscribe, catalogueSource.savedAt)
  const qc = useQueryClient()
  const [retrying, setRetrying] = useState(false)
  if (savedAt == null) return null

  return (
    <div
      role="status"
      className="mx-4 mb-2 flex items-center gap-3 rounded-xl bg-amber-500/10 px-3 py-2 text-xs text-amber-200 ring-1 ring-amber-400/25 sm:mx-6"
    >
      <span className="size-1.5 shrink-0 rounded-full bg-amber-400" />
      <p className="min-w-0 flex-1">
        AniList isn't answering, so this is kuro's saved copy
        <span className="text-amber-200/70"> from {relativeTime(savedAt)}</span>. It updates by itself once AniList is back.
      </p>
      <button
        type="button"
        disabled={retrying}
        onClick={async () => {
          setRetrying(true)
          await qc.refetchQueries({ type: 'active' }).catch(() => {})
          setRetrying(false)
        }}
        className="shrink-0 rounded-md px-2 py-1 font-medium text-amber-100 transition-colors hover:bg-amber-400/15 disabled:opacity-50"
      >
        {retrying ? 'Checking…' : 'Try now'}
      </button>
    </div>
  )
}
