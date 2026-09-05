import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { bytes, cx } from '../lib/format'
import { useSetup } from '../lib/queries'
import { ComponentState } from '../components/ComponentState'
import { Badge, ErrorState, Skeleton } from '../components/ui'

/**
 * What kuro needs before it can play anything, and what each piece is for.
 * The binary is 22 MB; these together are over half a gigabyte.
 */
export function SetupPage() {
  const qc = useQueryClient()
  const { data, isPending, isError, error, refetch } = useSetup({
    // While anything is downloading, often enough for the bar to move.
    refetchInterval: (q) => {
      const setup = q.state.data
      if (!setup) return 5000
      const busy = (setup.progress ?? []).some(
        (p) => p.stage !== 'done' && p.stage !== 'failed',
      )
      return busy ? 1000 : setup.ready ? false : 5000
    },
  })

  const install = useMutation({
    mutationFn: (name: string) => api.post(`/api/setup/install/${name}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['setup'] }),
  })

  if (isError) return <ErrorState error={error} retry={() => refetch()} />
  if (isPending) return <Skeleton className="h-96 w-full" />

  // Defended, not trusted: this is where a first run lands, and an empty list
  // arriving as null took the whole app down to a blank screen.
  const components = data.components ?? []
  const libraryPaths = data.libraryPaths ?? []
  const missing = components.filter((c) => !c.present)
  // Only ones kuro can actually download drive the bulk button; a manual one
  // shows its package-manager command on its own row.
  const fetchable = missing.filter((c) => !c.manual)
  const progressFor = (name: string) => (data.progress ?? []).find((p) => p.component === name)

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-white">Setting up</h1>
        <p className="mt-1 text-sm text-base-400">
          kuro shells out to a few external programs. None are bundled: together
          they are far larger than the app itself.
        </p>
      </div>

      <section className="surface overflow-hidden">
        <ul className="divide-y divide-white/[0.05]">
          {components.map((c) => (
            <li key={c.name} className="flex items-start gap-3 p-4">
              <span
                className={cx(
                  'mt-0.5 grid size-5 shrink-0 place-items-center rounded-full text-[11px]',
                  c.present ? 'bg-accent-500/20 text-accent-400' : 'bg-base-800 text-base-500',
                )}
              >
                {c.present ? '✓' : '·'}
              </span>

              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium text-base-100">{c.label}</span>
                  <span className="text-xs text-base-500">{c.size}</span>
                  {c.required && !c.present && <Badge tone="recap">Required</Badge>}
                  {!c.required && <Badge>Optional</Badge>}
                  {c.version && <Badge tone="accent">{c.version}</Badge>}
                </div>
                <p className="mt-1 text-sm text-base-400">{c.purpose}</p>
                {c.needs && !components.find((x) => x.name === c.needs)?.present && (
                  <p className="mt-1 text-xs text-base-500">Needs {c.needs} to be useful.</p>
                )}
                <ComponentState
                  progress={progressFor(c.name)}
                  present={c.present}
                  manual={c.manual}
                  version={c.version}
                  latest={c.latest}
                  onInstall={() => install.mutate(c.name)}
                  pending={install.isPending && install.variables === c.name}
                />
              </div>
            </li>
          ))}
        </ul>
      </section>

      <section className="surface p-4">
        <h2 className="text-sm font-semibold text-base-100">Release sources</h2>
        {data.indexers > 0 ? (
          <p className="mt-1 text-sm text-base-400">
            {data.indexers} site{data.indexers === 1 ? '' : 's'} configured in{' '}
            <code className="text-base-300">config.toml</code>.
          </p>
        ) : (
          <>
            <p className="mt-1 text-sm text-base-400">
              kuro ships with no torrent sites. Add one block per site to{' '}
              <code className="text-base-300">config.toml</code> beside the binary and
              restart. <code className="text-base-300">type</code> is the feed format
              (<code className="text-base-300">nyaa</code> or{' '}
              <code className="text-base-300">tokyotosho</code>); the first site listed
              decides the record kept for a torrent several carry.
            </p>
            <pre className="mt-3 rounded-md bg-base-950 p-3 text-xs text-base-300">
              {'[[indexer]]\ntype = "nyaa"\nurl = "https://…"\n\n[[indexer]]\ntype = "tokyotosho"\nurl = "https://…"'}
            </pre>
          </>
        )}
      </section>

      {missing.length > 0 ? (
        <section className="surface p-4">
          <h2 className="text-sm font-semibold text-base-100">Fetch what is missing</h2>
          <p className="mt-1 text-sm text-base-400">
            Downloaded from the same places a build uses, into{' '}
            <code className="text-base-300">{data.binDir}</code>.
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <button
              onClick={() => fetchable.forEach((c) => install.mutate(c.name))}
              disabled={fetchable.length === 0}
              className="rounded-md bg-accent-500 px-4 py-2 text-sm font-medium text-white hover:bg-accent-600 disabled:opacity-50"
            >
              Install {fetchable.length === 1 ? fetchable[0].label.toLowerCase() : 'everything missing'}
            </button>
            {/* Always a way out: most of these are optional, an install can
                fail, and own-files viewers need none of them. */}
            <Link to="/" className="text-sm text-base-400 transition-colors hover:text-base-100">
              Skip for now
            </Link>
          </div>
        </section>
      ) : (
        <section className="rounded-xl border border-accent-500/30 bg-accent-500/5 p-4">
          <p className="text-sm text-base-100">Everything is installed.</p>
          <Link
            to="/"
            className="mt-3 inline-block rounded-md bg-accent-500 px-4 py-2 text-sm font-medium text-white hover:bg-accent-600"
          >
            Start watching
          </Link>
        </section>
      )}

      <section className="surface p-4 text-sm">
        <h2 className="mb-2 font-semibold text-base-100">Where things go</h2>
        <dl className="space-y-1.5 text-base-400">
          <div className="flex justify-between gap-4">
            <dt>Episode cache</dt>
            <dd className="truncate text-base-300" title={data.cacheDir}>
              {data.cacheDir} · up to {bytes(data.cacheBudget)}
            </dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt>Your own files</dt>
            <dd className="text-base-300">
              {libraryPaths.length > 0
                ? `${libraryPaths.length} folder${libraryPaths.length === 1 ? '' : 's'}`
                : 'none yet'}
            </dd>
          </div>
        </dl>
        <p className="mt-3 text-xs text-base-500">
          Watched episodes stay cached so a rewatch is instant; the oldest are
          removed once the budget is reached. Both are adjustable in{' '}
          <Link to="/settings" className="text-accent-400 hover:underline">
            settings
          </Link>
          .
        </p>
      </section>
    </div>
  )
}
