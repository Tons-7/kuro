// A LAN client is handed a token in the URL on first visit; the server then
// sets a cookie, so it only has to be remembered for that first request.
const TOKEN_KEY = 'kuro.token'

function readToken(): string {
  const fromUrl = new URLSearchParams(window.location.search).get('token')
  if (fromUrl) {
    localStorage.setItem(TOKEN_KEY, fromUrl)
    return fromUrl
  }
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export class ApiError extends Error {
  readonly status: number
  readonly body?: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.status = status
    this.body = body
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = readToken()
  const headers = new Headers(init?.headers)
  if (token) headers.set('Authorization', `Bearer ${token}`)
  if (init?.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')

  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })

  if (!res.ok) {
    let body: unknown
    let message = `${res.status} ${res.statusText}`
    try {
      body = await res.json()
      const err = (body as { error?: string })?.error
      if (err) message = err
    } catch {
      // A non-JSON error body is still an error; the status carries it.
    }
    throw new ApiError(res.status, message, body)
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined }),
  del: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
  /** The file as sent; the server tells the formats apart by content. */
  upload: <T>(path: string, file: File) =>
    request<T>(path, {
      method: 'POST',
      body: file,
      headers: { 'Content-Type': file.type || 'application/octet-stream' },
    }),
}

/** The path with the access token in its query, for requests that cannot set a header. */
export function withToken(path: string): string {
  const token = readToken()
  if (!token) return path
  return `${path}${path.includes('?') ? '&' : '?'}token=${encodeURIComponent(token)}`
}

/** Fire-and-forget, for progress reports that must survive the page closing. */
export function beacon(path: string, body: unknown) {
  const url = withToken(path)
  const blob = new Blob([JSON.stringify(body)], { type: 'application/json' })
  if (!navigator.sendBeacon(url, blob)) {
    void api.post(path, body).catch(() => {})
  }
}

export function query(params: Record<string, string | number | boolean | undefined | null>) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    search.set(key, String(value))
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}

// ---------------------------------------------------------------- types

export interface Page<T> {
  items: T[]
  total: number
  page: number
  perPage: number
  hasMore: boolean
}

export interface Resume {
  epKey: string
  episode: number | null
  position: number
  duration: number | null
  watched: boolean
}

export interface LibraryItem {
  id: number
  title: string
  romaji: string
  english: string | null
  cover: string | null
  color: string | null
  episodes: number | null
  status: string | null
  progress: number
  score: number
  nextEpisode: number | null
  nextAiringAt: number | null
  resume?: Resume
}

export interface DiscoverItem {
  id: number
  title: string
  romaji: string
  english?: string
  /** Poster sized for a card. Use thumb in list rows and banner for the hero. */
  cover?: string
  thumb?: string
  banner?: string
  color?: string
  format?: string
  status?: string
  episodes?: number
  season?: string
  seasonYear?: number
  score?: number
  popularity?: number
  Genres?: string[]
  description?: string
  onList: boolean
  progress: number
}

export interface ScheduleItem {
  animeId: number
  episode: number
  airingAt: number
  title: string
  romaji: string
  english?: string
  cover?: string
  thumb?: string
  colour?: string
  format?: string
  onList: boolean
  progress: number
  behind: number
  watched: boolean
  jstWeekday: string
}

export interface ScheduleDay {
  date: string
  weekday: string
  today: boolean
  items: ScheduleItem[]
}

export interface Episode {
  animeId: number
  epKey: string
  /** As catalogued, which is what playback and release matching use. */
  number: number
  /** Position within this entry, so a later cour still reads as 1..N. */
  display: number
  absolute?: number
  titleEn?: string
  titleJa?: string
  overview?: string
  still?: string
  airDate?: number
  runtime?: number
  /** Numbered from the catalogue's episode count; nothing is known about it. */
  planned?: boolean
  /** "filler" | "mixed" | "manga-canon" | "anime-canon"; absent means unknown. */
  filler?: string
  recap: boolean
  watched: boolean
  position?: number
  duration?: number
  /** The position is worth going back to, by the same rule playback resumes on. */
  resumable?: boolean
  onDisk: boolean
}

/** Mixed episodes are part filler, so they carry the filler colour too. */
export function hasFiller(episode: Episode) {
  return episode.filler === 'filler' || episode.filler === 'mixed'
}

export interface PlaySession {
  animeId: number
  episode: number
  title: string
  source: 'local' | 'torrent'
  infoHash?: string
  torrentId?: number
  fileIndex: number
  streamUrl: string
  localPath?: string
  startAt: number
  player: string
}

/** One release the finder ranked for an episode; the manual picker's row. */
export interface ReleaseCandidate {
  Torrent: {
    title: string
    infoHash: string
    size: number
    seeders: number
    /** False from an indexer that publishes no peer counts. */
    seedersKnown: boolean
    leechers: number
    trusted?: boolean
    indexer?: string
  }
  Release: {
    Group?: string
    Resolution?: string
    Source?: string
    Codec?: string
    BitDepth?: number
    DualAudio?: boolean
    Batch?: boolean
    Episode?: number
    EpisodeEnd?: number
    Subtitles?: string[]
  }
  SeaDexBest?: boolean
  Confirmed?: boolean
  score: number
  reasons?: string[]
  playable: boolean
  autoPick: boolean
  blocked?: string
}

export interface Bookmark {
  favourite: boolean
  pinned: boolean
  hidden: boolean
  note: string
}

export interface SubtitleTrack {
  index: number
  title?: string
  language?: string
  url: string
  default?: boolean
}

export interface AudioTrack {
  index: number
  codec?: string
  channels?: number
  language?: string
  title?: string
  default?: boolean
}

/** One episode inside a downloaded pack. */
export interface DownloadFile {
  fileIndex: number
  epKey: string
  kept: boolean
  complete: boolean
}

/** Whether the swarm keeps up with the picture, in numbers. */
export interface StreamHealth {
  mbps: number
  needed: number
  peers: number
  complete: boolean
  slow: boolean
}

export interface StreamInfo {
  id: string
  playlist: string
  duration: number
  plan?: { videoCopy?: boolean; audioCopy?: boolean }
  video?: unknown
  audio?: AudioTrack[]
  /** Position in `audio` of the track being sent. */
  audioTrack?: number
  subtitles: SubtitleTrack[]
  fonts: string[]
  /** Present when the file has subtitles; fonts are extracted in the background. */
  fontsUrl?: string
}

export interface SkipRange {
  kind: string
  start: number
  end: number
}

export const LIST_STATUSES = [
  { value: 'CURRENT', label: 'Watching' },
  { value: 'COMPLETED', label: 'Completed' },
  { value: 'PAUSED', label: 'On hold' },
  { value: 'DROPPED', label: 'Dropped' },
  { value: 'PLANNING', label: 'Plan to watch' },
  { value: 'REPEATING', label: 'Rewatching' },
] as const

export type ListStatus = (typeof LIST_STATUSES)[number]['value']

export function statusLabel(value?: string | null) {
  return LIST_STATUSES.find((s) => s.value === value)?.label ?? ''
}
