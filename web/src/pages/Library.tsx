import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { LIST_STATUSES, type LibraryItem } from '../lib/api'
import { useFavourites, useLibrary, useLibraryCounts } from '../lib/queries'
import { PosterCard, PosterGrid, toCard } from '../components/PosterCard'
import {
  Empty,
  ErrorState,
  LinkButton,
  PageHeader,
  Pager,
  RefreshFailed,
  SearchInput,
  Segmented,
  Select,
  Skeleton,
  useClampPage,
  useDebounced,
} from '../components/ui'

const FAVOURITES = 'favourites'

// Progress is measured against the announced total, else what has aired.
function total(item: LibraryItem): number | null {
  return item.episodes ?? (item.nextEpisode ? item.nextEpisode - 1 : null)
}

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
    // Only a changed search resets the page; mounting (Back from a show) must not.
    if ((params.get('q') ?? '') !== q) next.delete('page')
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
  const setPage = (n: number, replace = false) => {
    const next = new URLSearchParams(params)
    next.set('page', String(n))
    setParams(next, { replace })
  }
  // A status changed from the last card of a later page empties it.
  useClampPage(data?.items.length === 0, page, data?.total, data?.perPage ?? 60, (n) => setPage(n, true))

  return (
    <div className="space-y-5">
      <PageHeader
        title="My library"
        meta={data ? `${data.total} anime` : undefined}
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

      {isError && data && <RefreshFailed retry={() => refetch()} />}
      {isError && !data ? (
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
          // Favourites first: the shelf ignores the search box, which it also hides.
          title={q && !favourites ? 'No match in your list' : 'Nothing here yet'}
          hint={
            favourites
              ? 'Press ♡ on a show to keep it here.'
              : q
                ? 'Try another spelling, or search all of AniList.'
                : status
                  ? 'Tag a show with this status and it will appear here.'
                  : 'Connect AniList in settings to import your list, or browse and tag shows yourself.'
          }
          action={
            favourites ? (
              <LinkButton to="/browse">Browse anime</LinkButton>
            ) : q ? (
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
                  // No announced total still gets a bar and count, against what has aired.
                  percent: total(item) ? (item.progress / total(item)!) * 100 : 0,
                  badge:
                    item.progress > 0 && total(item)
                      ? `${item.progress}/${total(item)}`
                      : undefined,
                  myScore: item.score,
                }}
              />
            ))}
          </PosterGrid>

          <Pager page={page} hasMore={data.hasMore} onPage={setPage} />
        </>
      )}
    </div>
  )
}
