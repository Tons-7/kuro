import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, query, type DiscoverItem } from '../lib/api'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useFilters } from '../lib/queries'
import { FilterMenu } from '../components/FilterMenu'
import { PosterCard, PosterGrid, toCard } from '../components/PosterCard'
import { Empty, ErrorState, SearchInput, Skeleton, useDebounced } from '../components/ui'

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
    queryFn: () =>
      api.get<{ items: DiscoverItem[]; total: number; hasMore: boolean }>(
        `/api/browse${query({ ...active, perPage: 42 })}`,
      ),
  })

  // Surprise me, within the genre/format/year filters chosen.
  const navigate = useNavigate()
  const random = useMutation({
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

  return (
    <div className="space-y-5">
      <SearchInput
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="Search 34,000 anime…"
        aria-label="Search"
        inputClassName="py-2.5 pl-10 text-base"
      />

      <div className="flex flex-wrap items-center gap-2">
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
        <FilterMenu
          label="Sort"
          multiple={false}
          values={csv(active.sort)}
          options={vocab?.sorts ?? []}
          labels={SORT_LABELS}
          onChange={(v) => set('sort', v[0])}
        />

        {Object.keys(active).some((k) => k !== 'page') && (
          <button
            onClick={() => setParams(new URLSearchParams())}
            className="rounded-lg px-2.5 py-1.5 text-sm text-base-400 transition-colors hover:text-white"
          >
            Clear all
          </button>
        )}

        <button
          onClick={() => random.mutate()}
          disabled={random.isPending}
          title="A random anime matching the genre, format and year chosen above"
          className="ml-auto flex items-center gap-1.5 rounded-lg bg-base-850 px-3 py-1.5 text-sm text-base-200 transition-colors hover:bg-base-750 disabled:opacity-50"
        >
          <ShuffleIcon />
          {random.isPending ? 'Picking…' : 'Random'}
        </button>
      </div>
      {random.isError && (
        <p className="text-xs text-recap">{(random.error as Error).message}</p>
      )}

      {results.isError ? (
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

          <div className="flex items-center justify-center gap-2 pt-2">
            <PageButton
              disabled={page <= 1}
              onClick={() => set('page', String(page - 1))}
              label="Previous"
            />
            <span className="text-sm text-base-400">Page {page}</span>
            <PageButton
              disabled={!results.data.hasMore}
              onClick={() => set('page', String(page + 1))}
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
    <button
      disabled={disabled}
      onClick={onClick}
      className="rounded-md bg-base-850 px-3 py-1.5 text-sm text-base-200 transition-colors hover:bg-base-750 disabled:cursor-not-allowed disabled:opacity-40"
    >
      {label}
    </button>
  )
}
