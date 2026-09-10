// Northern-hemisphere quarters, as AniList and the server use.
const SEASONS = ['WINTER', 'SPRING', 'SUMMER', 'FALL']

export interface SeasonYear {
  season: string
  year: number
}

export function currentSeason(now = new Date()): SeasonYear {
  return { season: SEASONS[Math.floor(now.getMonth() / 3)], year: now.getFullYear() }
}

export function nextSeason(now = new Date()): SeasonYear {
  const q = Math.floor(now.getMonth() / 3)
  return {
    season: SEASONS[(q + 1) % 4],
    year: q === 3 ? now.getFullYear() + 1 : now.getFullYear(),
  }
}

export function browseSeason({ season, year }: SeasonYear): string {
  return `/browse?season=${season}&year=${year}&sort=popular`
}
