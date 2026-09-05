import { useEffect, useState } from 'react'
import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryOptions,
} from '@tanstack/react-query'
import {
  api,
  query,
  type DiscoverItem,
  type Episode,
  type LibraryItem,
  type ListStatus,
  type Page,
  type ScheduleDay,
  type ScheduleItem,
} from './api'

// The browser reports minutes to subtract from local time to reach UTC, which
// is the opposite sign to what the schedule endpoint wants.
export const tzOffset = () => -new Date().getTimezoneOffset()

export function useHome() {
  return useQuery({
    queryKey: ['continue'],
    queryFn: () => api.get<Page<LibraryItem>>('/api/continue?perPage=20'),
    // Progress moves while an episode plays, including from mpv or a phone.
    refetchInterval: 2 * 60_000,
  })
}

export function useDiscover(sort: string, perPage = 24) {
  return useQuery({
    queryKey: ['discover', sort, perPage],
    queryFn: () => api.get<{ items: DiscoverItem[] }>(`/api/discover${query({ sort, perPage })}`),
    // Trending moves slowly; refetching on every tab focus wastes the AniList
    // budget, which is 30 requests a minute for everything.
    staleTime: 10 * 60_000,
  })
}

export function useSchedule(days = 7, mine = false) {
  return useQuery({
    queryKey: ['schedule', days, mine],
    queryFn: () =>
      api.get<{ days: ScheduleDay[]; items: ScheduleItem[] }>(
        `/api/schedule${query({ tz: tzOffset(), days, mine: mine || undefined })}`,
      ),
    staleTime: 5 * 60_000,
    // Which episode is next changes with the clock, not with any user action.
    refetchInterval: 5 * 60_000,
  })
}

// Re-renders on a timer so relative times ("5h ago", "in 2h") advance on their
// own; without it the page is correct but motionless.
export function useNow(every = 30_000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), every)
    return () => window.clearInterval(id)
  }, [every])
  return now
}

export function useAired(hours = 48) {
  const start = Math.floor(Date.now() / 3_600_000) * 3600 - hours * 3600
  return useQuery({
    queryKey: ['aired', hours, start],
    queryFn: () =>
      api.get<{ items: ScheduleItem[] }>(`/api/schedule${query({ start, days: 3 })}`),
    staleTime: 5 * 60_000,
    // New episodes appear on their own; leaving the page open should not mean
    // looking at a list from hours ago.
    refetchInterval: 5 * 60_000,
  })
}

export interface Season {
  id: number
  ordinal: number
  romaji: string
  english?: string | null
  cover?: string | null
  episodes?: number | null
  year?: number | null
  format?: string | null
  status?: string | null
  /** What the user tagged it, as opposed to whether it has finished airing. */
  listStatus?: string | null
  progress: number
  onList: boolean
}

export function useFranchise(animeId?: number) {
  return useQuery({
    enabled: !!animeId,
    queryKey: ['franchise', animeId],
    queryFn: () =>
      api.get<{ rootId: number; seasons: Season[] }>(`/api/franchise?id=${animeId}`),
    staleTime: 30 * 60_000,
  })
}

export function useLibrary(
  f: { status: string; q?: string; sort?: string },
  page = 1,
  enabled = true,
) {
  return useQuery({
    enabled,
    queryKey: ['library', f.status, f.q ?? '', f.sort ?? '', page],
    queryFn: () =>
      api.get<Page<LibraryItem>>(`/api/library${query({ status: f.status, q: f.q, sort: f.sort, page })}`),
  })
}

export interface LibraryCounts {
  total: number
  statuses: Record<string, number>
  favourites: number
}

export function useLibraryCounts() {
  return useQuery({
    queryKey: ['library', 'counts'],
    queryFn: () => api.get<LibraryCounts>('/api/library/counts'),
    staleTime: 30_000,
  })
}

export function useFavourites(page = 1, enabled = true) {
  return useQuery({
    enabled,
    queryKey: ['bookmarks', page],
    queryFn: () => api.get<Page<LibraryItem>>(`/api/bookmarks${query({ page })}`),
  })
}

export function useEpisodes(animeId: number | undefined) {
  return useQuery({
    enabled: !!animeId,
    queryKey: ['episodes', animeId],
    queryFn: () =>
      api.get<{ items: Episode[]; count: number }>(`/api/episodes${query({ id: animeId })}`),
  })
}

export function useRecommendations(animeId: number | undefined) {
  return useQuery({
    enabled: !!animeId,
    queryKey: ['recommend', animeId],
    queryFn: () =>
      api.get<{ items: DiscoverItem[]; source: string }>(
        `/api/recommend${query({ anime: animeId, limit: 18 })}`,
      ),
    staleTime: 60 * 60_000,
  })
}

export function useFilters() {
  return useQuery({
    queryKey: ['filters'],
    queryFn: () => api.get<Record<string, unknown>>('/api/filters'),
    staleTime: 24 * 60 * 60_000,
  })
}

export function useSettings() {
  return useQuery({
    queryKey: ['settings'],
    queryFn: () => api.get<Record<string, string>>('/api/settings'),
  })
}

export function usePrefs(animeId?: number) {
  return useQuery({
    queryKey: ['prefs', animeId ?? 0],
    queryFn: () =>
      api.get<{
        effective: Record<string, string>
        defaults: Record<string, string>
        overrides?: Record<string, string>
      }>(`/api/prefs${query({ anime: animeId })}`),
  })
}

export function useSetPref() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { key: string; value: string; animeId?: number }) =>
      api.post('/api/prefs', v),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['prefs'] })
      void qc.invalidateQueries({ queryKey: ['settings'] })
    },
  })
}

export function useSetStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { animeId: number; status: ListStatus; score?: number }) =>
      api.post('/api/status', v),
    // The tag appears in several places at once, so everything showing list
    // state has to be refreshed rather than just the card that was clicked.
    onSuccess: () => {
      for (const key of ['library', 'continue', 'discover', 'browse', 'schedule', 'anime']) {
        void qc.invalidateQueries({ queryKey: [key] })
      }
    },
  })
}

export interface Notification {
  id: number
  kind: string
  animeId: number
  episode: number
  title: string
  body: string
  createdAt: number
  read: boolean
}

export function useNotifications() {
  return useQuery({
    queryKey: ['notifications'],
    queryFn: () =>
      api.get<{ items: Notification[]; unread: number }>('/api/notifications?perPage=30'),
    refetchInterval: 5 * 60_000,
  })
}

export interface SetupComponent {
  name: string
  label: string
  purpose: string
  size: string
  required?: boolean
  present: boolean
  version?: string
  /** Newest published version, once looked up. */
  latest?: string
  needs?: string
  /** A package-manager command, for what kuro cannot fetch on this OS. */
  manual?: string
}

export interface SetupProgress {
  component: string
  stage: 'resolving' | 'downloading' | 'extracting' | 'done' | 'failed'
  version?: string
  bytes: number
  total: number
  error?: string
}

export interface SetupState {
  components: SetupComponent[]
  ready: boolean
  /** Torrent sites in config.toml. None ship with kuro. */
  indexers: number
  binDir: string
  cacheDir: string
  cacheBudget: number
  libraryPaths: string[]
  progress: SetupProgress[]
}

/**
 * Shared so the setup page and the first-run nudge cannot disagree about the
 * shape of one cached answer: whichever asked first would otherwise decide what
 * the other received.
 */
export function useSetup(options?: {
  refetchInterval?: UseQueryOptions<SetupState>['refetchInterval']
}) {
  return useQuery({
    queryKey: ['setup'],
    queryFn: () => api.get<SetupState>('/api/setup'),
    staleTime: 30_000,
    ...options,
  })
}
