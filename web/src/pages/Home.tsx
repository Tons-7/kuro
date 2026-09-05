import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type LibraryItem } from '../lib/api'
import { clockTime } from '../lib/format'
import { useAired, useDiscover, useHome, useNow } from '../lib/queries'
import { Hero } from '../components/Hero'
import { HoverInfo } from '../components/HoverInfo'
import { PosterCard, toCard } from '../components/PosterCard'
import { Rail, RailItem } from '../components/Rail'
import { ReleasedCard } from '../components/ReleasedCard'
import { Popularity, ScheduleWidget } from '../components/Sidebar'
import { ProgressBar, Skeleton } from '../components/ui'

export function Home() {
  const trending = useDiscover('trending', 20)
  const seasonal = useDiscover('season', 20)
  const continuing = useHome()
  const schedule = useAired(48)

  // Everything already aired, newest first: the "recently released" strip.
  const now = useNow() / 1000
  const released = (schedule.data?.items ?? [])
    .filter((item) => item.airingAt <= now)
    .sort((a, b) => b.airingAt - a.airingAt)
    .slice(0, 20)

  return (
    <div className="space-y-8">
      <Hero items={trending.data?.items} loading={trending.isPending} />

      <div className="grid gap-8 xl:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="min-w-0 space-y-8">
          {continuing.isPending ? (
            <RailSkeleton title="Continue watching" />
          ) : (
            continuing.data && continuing.data.items.length > 0 && (
              <Rail title="Continue watching" more={{ to: '/library' }}>
                {continuing.data.items.map((item) => (
                  <RailItem key={item.id}>
                    <ContinueCard item={item} />
                  </RailItem>
                ))}
              </Rail>
            )
          )}

          {schedule.isPending ? (
            <RailSkeleton title="Recently released" />
          ) : (
            released.length > 0 && (
              <Rail title="Recently released" more={{ to: '/recent' }}>
                {released.map((item) => (
                  <RailItem key={`${item.animeId}-${item.episode}`}>
                    <ReleasedCard item={item} />
                  </RailItem>
                ))}
              </Rail>
            )
          )}

          {seasonal.isPending ? (
            <RailSkeleton title="This season" />
          ) : (
            <Rail title="This season" more={{ to: '/browse?sort=popular' }}>
              {(seasonal.data?.items ?? []).map((anime) => (
                <RailItem key={anime.id}>
                  <PosterCard anime={toCard(anime)} />
                </RailItem>
              ))}
            </Rail>
          )}

          {trending.isPending ? (
            <RailSkeleton title="Trending now" />
          ) : (
            <Rail title="Trending now" more={{ to: '/browse?sort=trending' }}>
              {(trending.data?.items ?? []).map((anime) => (
                <RailItem key={anime.id}>
                  <PosterCard anime={toCard(anime)} />
                </RailItem>
              ))}
            </Rail>
          )}
        </div>

        <aside className="space-y-4">
          <Popularity />
          <ScheduleWidget />
        </aside>
      </div>
    </div>
  )
}

function ContinueCard({ item }: { item: LibraryItem }) {
  const resume = item.resume
  const episode = resume?.episode ?? item.progress + 1
  const percent =
    resume && resume.duration ? Math.min(100, (resume.position / resume.duration) * 100) : 0

  const qc = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const dismiss = useMutation({
    mutationFn: () => api.post('/api/continue/dismiss', { animeId: item.id, episode }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['continue'] }),
  })

  return (
    <div className="group/continue relative">
      {/* Removing only takes it off this row; progress and the list tag are
          untouched, which is the difference from marking it watched. */}
      <div className="absolute top-1.5 right-1.5 z-10 opacity-0 transition-opacity group-hover/continue:opacity-100 focus-within:opacity-100 max-sm:opacity-100">
        {confirming ? (
          <div className="flex items-center gap-1 rounded-md bg-base-950/90 p-1 ring-1 ring-base-700 backdrop-blur-sm">
            <button
              onClick={() => dismiss.mutate()}
              disabled={dismiss.isPending}
              className="rounded px-2 py-0.5 text-[11px] font-medium text-recap hover:bg-base-800"
            >
              Remove
            </button>
            <button
              onClick={() => setConfirming(false)}
              className="rounded px-2 py-0.5 text-[11px] text-base-400 hover:bg-base-800"
            >
              Keep
            </button>
          </div>
        ) : (
          <button
            onClick={() => setConfirming(true)}
            aria-label="Remove from continue watching"
            title="Remove from continue watching"
            className="grid size-7 place-items-center rounded-md bg-base-950/80 text-base-300 ring-1 ring-white/10 backdrop-blur-sm transition-colors hover:bg-base-800 hover:text-white"
          >
            <svg viewBox="0 0 24 24" className="size-3.5" aria-hidden>
              <path
                d="m6 6 12 12M18 6 6 18"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.2"
                strokeLinecap="round"
              />
            </svg>
          </button>
        )}
      </div>

      <HoverInfo
        anime={{
          id: item.id,
          title: item.title,
          episodes: item.episodes,
          progress: item.progress,
          play: { to: `/watch/${item.id}/${episode}`, label: `Resume ep ${episode}` },
        }}
      >
        <Link to={`/watch/${item.id}/${episode}`} className="block">
          <div className="relative aspect-video overflow-hidden rounded-card bg-base-850 ring-1 ring-base-800">
            {item.cover && (
              <img
                src={item.cover}
                alt=""
                loading="lazy"
                className="size-full object-cover object-center transition-transform duration-300 group-hover/continue:scale-105"
              />
            )}
            <div className="absolute inset-0 bg-gradient-to-t from-base-950/90 to-transparent" />

            <div className="absolute inset-x-0 bottom-0 p-2">
              <p className="mb-1 text-[11px] text-base-300">
                Episode {episode}
                {resume && resume.position > 0 && ` · ${clockTime(resume.position)}`}
              </p>
              {percent > 0 && <ProgressBar value={percent} />}
            </div>
          </div>
        </Link>
      </HoverInfo>

      {/* The art plays the episode, the title opens the show. Without this the
          only route to a series from the home page is to search for it. */}
      <Link to={`/anime/${item.id}`} className="mt-2 block">
        <p className="line-clamp-2 text-sm leading-snug text-base-200 transition-colors hover:text-white">
          {item.title}
        </p>
      </Link>
    </div>
  )
}

function RailSkeleton({ title }: { title: string }) {
  return (
    <section>
      <h2 className="mb-3 section-title">{title}</h2>
      <div className="flex gap-3 overflow-hidden">
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="w-[38vw] shrink-0 sm:w-40 md:w-44 lg:w-[11.5rem]">
            <Skeleton className="aspect-[2/3] w-full" />
            <Skeleton className="mt-2 h-4 w-3/4" />
          </div>
        ))}
      </div>
    </section>
  )
}
