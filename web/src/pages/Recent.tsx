import { useState } from 'react'
import { useAired, useNow } from '../lib/queries'
import { ReleasedCard } from '../components/ReleasedCard'
import {
  Empty,
  ErrorState,
  FilterToggle,
  PageHeader,
  Segmented,
  Skeleton,
} from '../components/ui'

const WINDOWS = [
  { value: '24', label: 'Last day' },
  { value: '72', label: 'Last 3 days' },
  { value: '168', label: 'Last week' },
] as const

/**
 * What has just come out, newest first. Distinct from the schedule, which
 * answers when things air: this one is a list of episodes ready to watch now.
 */
export function RecentPage() {
  const [hours, setHours] = useState<string>('72')
  const [mine, setMine] = useState(false)

  const now = useNow() / 1000
  const { data, isPending, isError, error, refetch } = useAired(Number(hours))

  const items = (data?.items ?? [])
    .filter((item) => item.airingAt <= now && (!mine || item.onList))
    .sort((a, b) => b.airingAt - a.airingAt)

  return (
    <div className="space-y-5">
      <PageHeader
        title="Recently released"
        subtitle="Episodes that have aired and can be watched now."
        meta={!isPending && !isError ? `${items.length} episodes` : undefined}
        actions={
          <>
            <Segmented options={WINDOWS} value={hours} onChange={setHours} />
            <FilterToggle on={mine} onChange={setMine}>
              Only my list
            </FilterToggle>
          </>
        }
      />

      {isError ? (
        <ErrorState error={error} retry={() => refetch()} />
      ) : isPending ? (
        <div className="grid grid-cols-2 gap-x-3 gap-y-5 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-5">
          {Array.from({ length: 10 }, (_, i) => (
            <div key={i}>
              <Skeleton className="aspect-video w-full" />
              <Skeleton className="mt-2 h-4 w-3/4" />
            </div>
          ))}
        </div>
      ) : items.length === 0 ? (
        <Empty
          title="Nothing has aired in this window"
          hint={mine ? 'Nothing from your list, at least. Try widening it.' : 'Try a wider window.'}
        />
      ) : (
        <div className="grid grid-cols-2 gap-x-3 gap-y-5 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-5">
          {items.map((item) => (
            <ReleasedCard key={`${item.animeId}-${item.episode}`} item={item} tags />
          ))}
        </div>
      )}
    </div>
  )
}
