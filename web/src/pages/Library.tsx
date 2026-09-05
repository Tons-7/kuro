import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { LIST_STATUSES } from '../lib/api'
import { useFavourites, useLibrary, useLibraryCounts } from '../lib/queries'
import { PosterCard, PosterGrid, toCard } from '../components/PosterCard'
import {
  Button,
  Empty,
  ErrorState,
  LinkButton,
  PageHeader,
  SearchInput,
  Segmented,
  Select,
  Skeleton,
  useDebounced,
} from '../components/ui'

const FAVOURITES = 'favourites'

export function Library() {
  const [params, setParams] = useSearchParams()
  const status = params.get('status') ?? ''
  const page = Number(params.get('page') ?? 1)

  // Favourites are kuro's own shelf, not a tracker status.
  const favourites = status === FAVOURITES
  const sort = params.get('sort') ?? 'updated'
  const [text, setText] = useState(params.get('q') ?? '')
  const q = useDebounced(text.trim(), 250)
  useEffect(() => {
    const next = new URLSearchParams(params)
    if (q) next.set('q', q)
    else next.delete('q')
    next.delete('page')
    if (next.toString() !== params.toString()) setParams(next, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q])

  const list = useLibrary({ status: favourites ? '' : status, q, sort }, page, !favourites)
  const favs = useFavourites(page, favourites)
  const counts = useLibraryCounts().data
  const { data, isPending, isError, error, refetch } = favourites ? favs : list

  const setStatus = (value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set('status', value)
    else next.delete('status')
    next.delete('page')
    setParams(next)
  }
  const setSort = (value: string) => {
    const next = new URLSearchParams(params)
    if (value && value !== 'updated') next.set('sort', value)
    else next.delete('sort')
    next.delete('page')
    setParams(next)
  }
  const setPage = (n: number) => {
    const next = new URLSearchParams(params)
    next.set('page', String(n))
    setParams(next)
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title="My library"
        meta={!isPending && !isError ? `${data.total} anime` : undefined}
        actions={
          !favourites && (
            <>
              <SearchInput
                value={text}
                onChange={(e) => setText(e.target.value)}
                placeholder="Filter your list…"
                aria-label="Filter library"
                className="w-64 max-w-full"
              />
              <Select value={sort} onChange={setSort} aria-label="Sort library">
                <option value="updated">Recently updated</option>
                <option value="title">Title A–Z</option>
                <option value="score">Your score</option>
                <option value="airing">Next to air</option>
                <option value="progress">Most watched</option>
              </Select>
            </>
          )
        }
      />

      <Segmented
        options={[
          { value: '', label: 'All', count: counts?.total },
          ...LIST_STATUSES.map((s) => ({
            value: s.value as string,
            label: s.label,
            count: counts?.statuses[s.value] ?? (counts ? 0 : undefined),
          })),
          { value: FAVOURITES, label: '♥ Favourites', count: counts?.favourites },
        ]}
        value={status}
        onChange={setStatus}
      />

      {isError ? (
        <ErrorState error={error} retry={() => refetch()} />
      ) : isPending ? (
        <PosterGrid>
          {Array.from({ length: 14 }, (_, i) => (
            <div key={i}>
              <Skeleton className="aspect-[2/3] w-full" />
              <Skeleton className="mt-2 h-4 w-3/4" />
            </div>
          ))}
        </PosterGrid>
      ) : data.items.length === 0 ? (
        <Empty
          title={q ? 'No match in your list' : 'Nothing here yet'}
          hint={
            q
              ? 'Try another spelling, or search all of AniList.'
              : favourites
                ? 'Press ♡ on a show to keep it here.'
                : status
                  ? 'Tag a show with this status and it will appear here.'
                  : 'Connect AniList in settings to import your list, or browse and tag shows yourself.'
          }
          action={
            q ? (
              <LinkButton to={`/browse?q=${encodeURIComponent(q)}`}>Search AniList</LinkButton>
            ) : status ? (
              <LinkButton to="/browse">Browse anime</LinkButton>
            ) : (
              <LinkButton to="/settings?tab=Trackers" variant="primary">
                Connect a tracker
              </LinkButton>
            )
          }
        />
      ) : (
        <>
          <PosterGrid>
            {data.items.map((item) => (
              <PosterCard
                key={item.id}
                anime={{
                  ...toCard(item),
                  percent: item.episodes ? (item.progress / item.episodes) * 100 : 0,
                  badge:
                    item.progress > 0 && item.episodes
                      ? `${item.progress}/${item.episodes}`
                      : undefined,
                  myScore: item.score,
                }}
              />
            ))}
          </PosterGrid>

          {(page > 1 || data.hasMore) && (
            <div className="flex items-center justify-center gap-2 pt-2">
              <Button disabled={page <= 1} onClick={() => setPage(page - 1)}>
                Previous
              </Button>
              <span className="text-sm text-base-400">Page {page}</span>
              <Button disabled={!data.hasMore} onClick={() => setPage(page + 1)}>
                Next
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
