import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { usePrefs } from '../lib/queries'
import { Skeleton } from './ui'

export interface Voice {
  name: string
  image?: string
  language: string
}

export interface Character {
  id: number
  name: string
  image?: string
  role?: string
  favorites: number
  voices?: Voice[]
}

export function useCharacters(animeId: number | undefined) {
  return useQuery({
    enabled: !!animeId,
    queryKey: ['characters', animeId],
    queryFn: () => api.get<{ items: Character[] }>(`/api/anime/${animeId}/characters`),
    // A cast does not change once a show has aired.
    staleTime: 12 * 60 * 60_000,
  })
}

/**
 * Who is in the show and who played them; MyAnimeList records one cast per
 * series. Laid out in rows so names aren't cut off by a single sideways strip.
 */
export function CharacterRail({
  animeId,
  limit = 12,
  title = 'Characters',
}: {
  animeId: number
  limit?: number
  title?: string
}) {
  const { data, isPending } = useCharacters(animeId)
  const prefs = usePrefs()
  const [expanded, setExpanded] = useState(false)

  // Follow whichever performance is being watched, falling back to the original
  // cast — most shows were never dubbed at all.
  const dubbed = prefs.data?.effective['audio.prefer'] === 'dub'
  const voiceOf = (c: Character) => {
    const voices = c.voices ?? []
    if (dubbed) return voices.find((v) => v.language === 'English') ?? voices[0]
    return voices[0]
  }

  // A count rather than a number of rows: the grid fits as many columns as the
  // width allows, so how many make a row is not something this can know.
  const cast = data?.items ?? []
  const shown = expanded ? cast.slice(0, 48) : cast.slice(0, limit)

  if (isPending) {
    return (
      <section className="space-y-3">
        <h2 className="section-title">{title}</h2>
        <div className="grid gap-x-3 gap-y-2.5 [grid-template-columns:repeat(auto-fill,minmax(17rem,1fr))]">
          {Array.from({ length: limit }, (_, i) => (
            <Skeleton key={i} className="h-[72px] rounded-card" />
          ))}
        </div>
      </section>
    )
  }
  if (cast.length === 0) return null

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <h2 className="section-title">{title}</h2>
        {cast.length > shown.length && (
          <button
            onClick={() => setExpanded(true)}
            className="text-sm text-accent-400 transition-colors hover:text-accent-300"
          >
            Show more
          </button>
        )}
        {expanded && (
          <button
            onClick={() => setExpanded(false)}
            className="text-sm text-accent-400 transition-colors hover:text-accent-300"
          >
            Show less
          </button>
        )}
      </div>

      <ul className="grid gap-x-3 gap-y-2.5 [grid-template-columns:repeat(auto-fill,minmax(17rem,1fr))]">
        {shown.map((c) => {
          const voice = voiceOf(c)
          return (
            <li
              key={c.id}
              className="flex items-stretch overflow-hidden rounded-card bg-base-800 shadow-card ring-1 ring-white/5 transition-colors hover:bg-base-750"
            >
              {c.image ? (
                <img src={c.image} alt="" loading="lazy" className="h-[72px] w-14 shrink-0 object-cover" />
              ) : (
                <div className="h-[72px] w-14 shrink-0 bg-base-850" />
              )}

              {/* Stacked, not two columns: long Japanese names truncated both.
                  min-w-0 lets the text ellipse instead of pushing portraits out. */}
              <div className="min-w-0 flex-1 px-2.5 py-2">
                <p className="truncate text-sm font-medium text-base-100" title={c.name}>
                  {c.name}
                </p>
                <p className="text-xs text-base-500">{c.role}</p>
                {voice && (
                  <p
                    className="mt-1 truncate text-xs text-base-300"
                    title={`${voice.name} · ${voice.language}`}
                  >
                    {voice.name}
                    <span className="text-base-600"> · {voice.language}</span>
                  </p>
                )}
              </div>

              {voice?.image ? (
                <img src={voice.image} alt="" loading="lazy" className="h-[72px] w-14 shrink-0 object-cover" />
              ) : (
                voice && <div className="h-[72px] w-14 shrink-0 bg-base-850" />
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}
