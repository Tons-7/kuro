import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'

interface Theme {
  title: string
  artist?: string
  episodes?: string
}

interface Credit {
  name: string
  role: string
  image?: string
}

interface Extra {
  openings?: Theme[]
  endings?: Theme[]
  staff?: Credit[]
  trailer?: string
}

/** A show's themes, staff and trailer, fetched apart from the details as two more requests. */
export function useAnimeExtra(animeId: number) {
  return useQuery({
    enabled: animeId > 0,
    queryKey: ['anime-extra', animeId],
    queryFn: () => api.get<Extra>(`/api/anime/${animeId}/extra`),
    staleTime: 12 * 60 * 60_000,
  })
}

/** Theme songs and credits; the trailer is the hero's Trailer button. */
export function ShowExtra({ animeId }: { animeId: number }) {
  const { data } = useAnimeExtra(animeId)

  const openings = data?.openings ?? []
  const endings = data?.endings ?? []
  const staff = data?.staff ?? []
  if (openings.length === 0 && endings.length === 0 && staff.length === 0) return null

  return (
    <section className="space-y-3">
      <h2 className="section-title">Music & staff</h2>

      <div className="grid gap-2 lg:grid-cols-2">
        {(openings.length > 0 || endings.length > 0) && (
          <div className="rounded-card bg-base-800 p-4 shadow-card ring-1 ring-white/5">
            <ThemeList label="Opening" themes={openings} />
            {endings.length > 0 && <div className="mt-3" />}
            <ThemeList label="Ending" themes={endings} />
          </div>
        )}

        {staff.length > 0 && (
          <div className="rounded-card bg-base-800 p-4 shadow-card ring-1 ring-white/5">
            <ul className="space-y-1.5">
              {staff.slice(0, 8).map((c) => (
                <li key={`${c.role}-${c.name}`} className="flex items-baseline gap-3 text-sm">
                  {/* A fixed column, so names line up whatever the role's length. */}
                  {/* Wraps on a phone, where the column is too narrow for a whole role. */}
                  <span className="w-24 shrink-0 text-xs leading-tight text-base-500 sm:w-36 sm:truncate" title={c.role}>
                    {c.role}
                  </span>
                  <span className="min-w-0 truncate text-base-200">{c.name}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </section>
  )
}

function ThemeList({ label, themes }: { label: string; themes: Theme[] }) {
  const [all, setAll] = useState(false)
  if (themes.length === 0) return null

  // Conan has sixty-two openings. The recent ones are the ones you are watching,
  // so the list runs from the end and the rest are behind a count.
  const cap = 4
  const hidden = Math.max(0, themes.length - cap)
  const shown = all ? themes : themes.slice(-cap)

  return (
    <div>
      <ul className="space-y-1.5">
        {shown.map((t, i) => (
          <li key={`${t.title}-${i}`} className="flex items-baseline gap-3 text-sm">
            <span className="w-20 shrink-0 text-xs text-base-500 sm:w-36">
              {label}
              {themes.length > 1 ? ` ${(all ? 0 : hidden) + i + 1}` : ''}
            </span>
            <span className="min-w-0">
              <span className="text-base-100">{t.title}</span>
              {t.artist && <span className="text-base-400"> · {t.artist}</span>}
              {t.episodes && <span className="text-base-600"> · eps {t.episodes}</span>}
            </span>
          </li>
        ))}
      </ul>

      {hidden > 0 && (
        <button
          onClick={() => setAll((v) => !v)}
          className="mt-1.5 text-xs text-accent-400 transition-colors hover:text-accent-300"
        >
          {all ? 'Show fewer' : `Show all ${themes.length}`}
        </button>
      )}
    </div>
  )
}
