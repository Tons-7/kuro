import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api } from '../lib/api'
import { bytes } from '../lib/format'
import type { SetupState } from '../lib/queries'

/** Every folder kuro uses and its config.toml key. */
export function WhereThingsGo({ setup }: { setup: SetupState }) {
  const [picking, setPicking] = useState(false)
  const libraryPaths = setup.libraryPaths ?? []
  return (
    <>
      {/* The path is what you read: its own line, full width. */}
      <dl className="divide-y divide-white/5">
        <Folder
          label="History and settings"
          path={setup.dataDir}
          setting="data_dir"
          aside={
            <button
              onClick={() => setPicking((v) => !v)}
              className="text-accent-400 transition-colors hover:text-accent-300"
            >
              Change…
            </button>
          }
        >
          {picking && <DataDirPicker current={setup.dataDir} />}
        </Folder>
        <Folder
          label="Episode cache"
          path={setup.cacheDir}
          setting="cache_dir"
          aside={`up to ${bytes(setup.cacheBudget)}`}
        />
        <Folder label="Programs" path={setup.binDir} setting="bin_dir" />
        <div className="py-2.5">
          <dt className="flex items-baseline justify-between gap-4 text-sm text-base-200">
            Your own files
            <span className="text-xs text-base-500">
              {libraryPaths.length > 0
                ? `${libraryPaths.length} folder${libraryPaths.length === 1 ? '' : 's'}`
                : 'none yet'}
            </span>
          </dt>
        </div>
      </dl>
      <p className="mt-2 text-xs text-base-500">
        Each folder is a setting in <code className="text-base-400">{setup.configPath}</code>; edit it
        and restart.
      </p>
    </>
  )
}

function Folder({
  label,
  path,
  setting,
  aside,
  children,
}: {
  label: string
  path: string
  setting: string
  aside?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div className="py-2.5">
      <dt className="flex items-baseline justify-between gap-4 text-sm text-base-200">
        <span>
          {label} <code className="text-[11px] text-base-500">{setting}</code>
        </span>
        <span className="shrink-0 text-xs text-base-500">{aside}</span>
      </dt>
      <dd className="mt-0.5">
        <p className="font-mono text-xs break-all text-base-400">{path}</p>
        {children}
      </dd>
    </div>
  )
}

// Where history and settings live. Written to config.toml and the database
// copied over, so it takes effect on the next start with nothing lost.
function DataDirPicker({ current }: { current: string }) {
  const [path, setPath] = useState('')
  const move = useMutation({
    mutationFn: (p: string) =>
      api.post<{ dataDir: string; copied: boolean }>('/api/setup/data-dir', { path: p }),
  })

  if (move.isSuccess) {
    return (
      <p className="mt-2 text-xs text-accent-400">
        Saved: {move.data.dataDir}. Restart kuro to use it
        {move.data.copied ? ' — your history has been copied there.' : '.'}
      </p>
    )
  }
  return (
    <div className="mt-2 space-y-2 text-xs">
      <p className="text-base-400">
        Empty means the default ({current}). A relative path is beside kuro.exe:{' '}
        <button onClick={() => setPath('data')} className="text-accent-400 hover:underline">
          data
        </button>{' '}
        keeps everything in kuro's own folder.
      </p>
      <div className="flex gap-2">
        <input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="D:\kuro\data"
          className="min-w-0 flex-1 rounded-md border border-base-800 bg-base-950 px-2 py-1.5 text-sm text-base-100 outline-none focus:border-accent-500"
        />
        <button
          onClick={() => move.mutate(path.trim())}
          disabled={move.isPending}
          className="rounded-md bg-accent-500 px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-600 disabled:opacity-50"
        >
          Save
        </button>
      </div>
      {move.isError && <p className="text-recap">{(move.error as Error).message}</p>}
    </div>
  )
}
