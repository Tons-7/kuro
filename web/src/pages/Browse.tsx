import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, query, type DiscoverItem } from '../lib/api'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useFilters } from '../lib/queries'
import { FilterMenu } from '../components/FilterMenu'
import { StudioFilter } from '../components/StudioFilter'
import { PosterCard, PosterGrid, toCard } from '../components/PosterCard'
import { Button, Empty, ErrorState, PageHeader, SearchInput, Skeleton, useDebounced } from '../components/ui'

interface Vocabulary {
  genres: string[]
  formats: string[]
  statuses: string[]
  seasons: string[]
  sorts: string[]
  years: number[]
  current: { season: string; year: number }
}

const SORT_LABELS: Record<string, string> = {
  popular: 'Most popular',
  trending: 'Trending',
  score: 'Highest rated',
  favourites: 'Most favourited',
  newest: 'Newest',
  oldest: 'Oldest',
  title: 'Title A–Z',
  episodes: 'Most episodes',
}

export function Browse() {
  const [params, setParams] = useSearchParams()
  const [text, setText] = useState(params.get('q') ?? '')
  const debounced = useDebounced(text, 350)

  const filters = useFilters()
  const vocab = filters.data as unknown as Vocabulary | undefined

  // The typed query drives the URL so a search can be shared or reloaded.
  useEffect(() => {
    const next = new URLSearchParams(params)
    if (debounced) next.set('q', debounced)
    else next.delete('q')
    // A new search starts at its own first page, not page four of the last one.
    if (next.get('q') !== params.get('q')) next.delete('page')
    if (next.toString() !== params.toString()) setParams(next, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced])

  // A search from the header rewrites the URL without remounting this page, so
  // the field has to follow it or it keeps showing the previous term.
  const fromURL = params.get('q') ?? ''
  useEffect(() => {
    // Not our own debounced write, which would land mid-word and truncate it.
    if (fromURL !== debounced) setText(fromURL)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fromURL])

  const active = useMemo(() => Object.fromEntries(params.entries()), [params])
  const page = Number(active.page ?? 1)

  const results = useQuery({
    queryKey: ['browse', active],
    // A superseded filter's request is dropped, not left spending the AniList budget.
    queryFn: ({ signal }) =>
      api.get<{ items: DiscoverItem[]; total: number; hasMore: boolean }>(
        `/api/browse${query({ ...active, perPage: 42 })}`,
        signal,
      ),
  })

  // Surprise me, within the genre/format/year filters chosen.
  const navigate = useNavigate()
  const random = useMutation({
    meta: { inline: true },
    mutationFn: () =>
      api.get<{ id: number }>(
        `/api/random${query({
          genres: active.genres,
          format: csv(active.formats)[0],
          year: active.year,
        })}`,
      ),
    onSuccess: (r) => navigate(`/anime/${r.id}`),
  })

  const set = (key: string, value?: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    // Changing a filter must return to the first page or the results look empty.
    next.delete('page')
    setParams(next)
  }

  const goToPage = (n: number) => {
    const next = new URLSearchParams(params)
    next.set('page', String(n))
    setParams(next)
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  // One chip per value in force, so a single one can go without reopening its menu.
  const pretty = (v: string) => v.replace(/_/g, ' ').toLowerCase().replace(/^\w/, (c) => c.toUpperCase())
  const without = (key: string, value: string) => () =>
    set(key, csv(active[key]).filter((x) => x !== value).join(','))
  const chips: Array<{ key: string; value: string; label: string; remove: () => void }> = [
    ...csv(active.genres).map((g) => ({ key: 'genres', value: g, label: g, remove: without('genres', g) })),
    ...csv(active.formats).map((f) => ({ key: 'formats', value: f, label: f.replace('_', ' '), remove: without('formats', f) })),
    ...csv(active.statuses).map((s) => ({ key: 'statuses', value: s, label: pretty(s), remove: without('statuses', s) })),
    ...(active.studio
      ? [{
          key: 'studio',
          value: active.studio,
          label: `Studio: ${active.studioName ?? active.studio}`,
          remove: () => {
            const next = new URLSearchParams(params)
            next.delete('studio')
            next.delete('studioName')
            next.delete('page')
            setParams(next)
          },
        }]
      : []),
    ...(active.year ? [{ key: 'year', value: active.year, label: active.year, remove: () => set('year') }] : []),
    ...(active.season ? [{ key: 'season', value: active.season, label: pretty(active.season), remove: () => set('season') }] : []),
  ]

  return (
    <div className="space-y-5">
      <PageHeader
        title="Browse"
        subtitle="Every anime there is, narrowed your way."
        actions={
          <Button onClick={() => random.mutate()} disabled={random.isPending} title="A random anime matching the genre, format and year chosen">
            <ShuffleIcon />
            {random.isPending ? 'Picking…' : 'Surprise me'}
          </Button>
        }
      />

      {/* One panel: the search, what narrows it, and what is narrowing it now. */}
      <div className="surface space-y-3 p-3 sm:p-4">
      <SearchInput
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="Search 34,000 anime by any title…"
        aria-label="Search"
        inputClassName="rounded-xl bg-base-950/60 py-3 pl-10 text-base"
      />

      {/* On a phone one swipeable line, like the tabs, not three ragged rows. */}
      <div className="no-scrollbar flex items-center gap-2 max-sm:-mx-3 max-sm:overflow-x-auto max-sm:px-3 sm:flex-wrap">
        {/* Genre, format and status take several values at once — the API has
            always accepted a list; only this could not send one. */}
        <FilterMenu
          label="Genre"
          values={csv(active.genres)}
          options={vocab?.genres ?? []}
          onChange={(v) => set('genres', v.join(','))}
        />
        <FilterMenu
          label="Format"
          values={csv(active.formats)}
          options={vocab?.formats ?? []}
          onChange={(v) => set('formats', v.join(','))}
        />
        <FilterMenu
          label="Status"
          values={csv(active.statuses)}
          options={vocab?.statuses ?? []}
          onChange={(v) => set('statuses', v.join(','))}
        />
        <StudioFilter
          value={
            active.studio ? { id: Number(active.studio), name: active.studioName ?? 'Studio' } : undefined
          }
          onChange={(s) => {
            const next = new URLSearchParams(params)
            if (s) {
              next.set('studio', String(s.id))
              next.set('studioName', s.name)
            } else {
              next.delete('studio')
              next.delete('studioName')
            }
            next.delete('page')
            setParams(next)
          }}
        />
        <FilterMenu
          label="Year"
          multiple={false}
          values={csv(active.year)}
          options={(vocab?.years ?? []).map(String)}
          onChange={(v) => set('year', v[0])}
        />
        <FilterMenu
          label="Season"
          multiple={false}
          values={csv(active.season)}
          options={vocab?.seasons ?? []}
          onChange={(v) => set('season', v[0])}
        />
        {/* Ordering is not narrowing: set apart on the right. */}
        <div className="ml-auto flex shrink-0 items-center gap-2">
          <span className="text-xs font-medium text-base-500 max-sm:hidden">Sort</span>
          <FilterMenu
            label="Most popular"
            multiple={false}
            values={csv(active.sort)}
            options={vocab?.sorts ?? []}
            labels={SORT_LABELS}
            onChange={(v) => set('sort', v[0])}
          />
        </div>
      </div>

      {/* What is narrowing the results now, each removable on its own. */}
      <div className="flex min-h-7 flex-wrap items-center gap-1.5 border-t border-white/[0.05] pt-3">
        {chips.length === 0 ? (
          <span className="text-xs text-base-500">No filters — showing everything.</span>
        ) : (
          chips.map((c) => (
            <button
              key={`${c.key}-${c.value}`}
              onClick={c.remove}
              className="group flex items-center gap-1.5 rounded-full bg-accent-500/15 py-1 pr-2 pl-3 text-xs font-medium text-accent-200 ring-1 ring-accent-500/30 transition-colors hover:bg-accent-500/25"
              aria-label={`Remove ${c.label}`}
            >
              {c.label}
              <span className="text-accent-300/70 group-hover:text-white">✕</span>
            </button>
          ))
        )}
        {chips.length > 1 && (
          <button
            onClick={() => {
              const next = new URLSearchParams()
              if (active.q) next.set('q', active.q)
              if (active.sort) next.set('sort', active.sort)
              setParams(next)
            }}
            className="px-2 py-1 text-xs font-medium text-base-400 transition-colors hover:text-white"
          >
            Clear all
          </button>
        )}
        {results.data && results.data.total > 0 && (
          <span className="ml-auto text-xs text-base-500 tabular-nums">
            {/* AniList stops counting at 5,000. */}
            {results.data.total.toLocaleString()}
            {results.data.total >= 5000 ? '+' : ''} results
          </span>
        )}
      </div>
      </div>
      {random.isError && (
        <p className="text-xs text-recap">{(random.error as Error).message}</p>
      )}
      {active.studio && (active.q || active.year || active.season) && (
        <p className="text-xs text-base-500">
          A studio's list is its whole catalogue: search, year and season don't narrow it.
        </p>
      )}

      {results.isError && !results.data ? (
        <ErrorState error={results.error} retry={() => results.refetch()} />
      ) : results.isPending ? (
        <PosterGrid>
          {Array.from({ length: 21 }, (_, i) => (
            <div key={i}>
              <Skeleton className="aspect-[2/3] w-full" />
              <Skeleton className="mt-2 h-4 w-3/4" />
            </div>
          ))}
        </PosterGrid>
      ) : results.data.items.length === 0 ? (
        <Empty title="Nothing matched" hint="Try removing a filter." />
      ) : (
        <>
          <PosterGrid>
            {results.data.items.map((anime) => (
              <PosterCard key={anime.id} anime={toCard(anime)} />
            ))}
          </PosterGrid>

          <div className="flex items-center justify-center gap-3 pt-4">
            <PageButton disabled={page <= 1} onClick={() => goToPage(page - 1)} label="Previous" />
            <span className="text-sm text-base-400 tabular-nums">
              Page <span className="font-semibold text-white">{page}</span>
              {results.data.total > 0 && ` of ${Math.ceil(results.data.total / 42).toLocaleString()}`}
            </span>
            <PageButton
              disabled={!results.data.hasMore}
              onClick={() => goToPage(page + 1)}
              label="Next"
            />
          </div>
        </>
      )}
    </div>
  )
}

function ShuffleIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path
        d="M4 7h3.5c1.4 0 2.6.7 3.4 1.8l2.2 3.4c.8 1.1 2 1.8 3.4 1.8H20M4 17h3.5c1.4 0 2.6-.7 3.4-1.8l2.2-3.4c.8-1.1 2-1.8 3.4-1.8H20M17 4l3 3-3 3M17 14l3 3-3 3"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function csv(value?: string): string[] {
  return value ? value.split(',').filter(Boolean) : []
}

function PageButton({
  disabled,
  onClick,
  label,
}: {
  disabled: boolean
  onClick: () => void
  label: string
}) {
  return (
    <Button disabled={disabled} onClick={onClick}>
      {label}
    </Button>
  )
}
