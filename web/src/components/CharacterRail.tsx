import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { usePrefs } from '../lib/queries'
import { Rail } from './Rail'
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

/** Who is in the show and who played them; MyAnimeList records one cast per series. */
export function CharacterRail({
  animeId,
  limit = 24,
  title = 'Cast',
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

  // A long cast still needs a cap — Conan lists two thousand — but the rail
  // scrolls, so the first pass can be generous.
  const cast = data?.items ?? []
  const shown = expanded ? cast.slice(0, 120) : cast.slice(0, limit)

  if (isPending) {
    return (
      <Rail title={title}>
        {Array.from({ length: limit }, (_, i) => (
          <Skeleton key={i} className="h-[174px] w-[116px] shrink-0 rounded-card" />
        ))}
      </Rail>
    )
  }
  if (cast.length === 0) return null

  return (
    <Rail
      title={title}
      action={
        cast.length > limit && (
          <button
            onClick={() => setExpanded(!expanded)}
            className="rounded-md px-2 py-1 text-sm text-base-400 transition-colors hover:bg-base-850 hover:text-white"
          >
            {expanded ? 'Show less' : cast.length > 120 ? 'Show more' : `Show all ${cast.length}`}
          </button>
        )
      }
    >
      {/* One portrait each, in the app's poster shape, with the actor's face as
          a small avatar rather than a second full-height photo. */}
      {shown.map((c) => {
        const voice = voiceOf(c)
        return (
          <div key={c.id} className="w-[116px] shrink-0 snap-start">
            <div className="relative overflow-hidden rounded-card shadow-card">
              {c.image ? (
                <img src={c.image} alt="" loading="lazy" className="h-[174px] w-[116px] object-cover" />
              ) : (
                <div className="h-[174px] w-[116px] bg-base-850" />
              )}
              {/* Only "Main" earns a badge: SUPPORTING on nine cards in ten
                  says nothing. */}
              {c.role?.toUpperCase() === 'MAIN' && (
                <span className="absolute top-1.5 left-1.5 rounded-full bg-accent-500/85 px-1.5 py-0.5 text-[10px] font-semibold tracking-wide text-white uppercase">
                  Main
                </span>
              )}
              {voice?.image && (
                <img
                  src={voice.image}
                  alt=""
                  loading="lazy"
                  title={`${voice.name} · ${voice.language}`}
                  className="absolute right-1.5 bottom-1.5 size-9 rounded-full object-cover ring-2 ring-base-950/80"
                />
              )}
            </div>
            <p className="mt-2 line-clamp-2 text-[13px] leading-snug font-medium text-base-100" title={c.name}>
              {c.name}
            </p>
            {voice && (
              <p
                className="line-clamp-2 text-xs leading-snug text-base-500"
                title={`${voice.name} · ${voice.language}`}
              >
                {voice.name}
              </p>
            )}
          </div>
        )
      })}
    </Rail>
  )
}
