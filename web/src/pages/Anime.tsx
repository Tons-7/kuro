import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, statusLabel, type Bookmark, type DiscoverItem } from '../lib/api'
import { clockTime, cx, relativeTime, tint } from '../lib/format'
import {
  useEpisodes,
  useFranchise,
  useNow,
  usePrefs,
  useRecommendations,
  useSetPref,
  useSetStatus,
  type Season,
} from '../lib/queries'
import { CharacterRail } from '../components/CharacterRail'
import { ShowExtra } from '../components/ShowExtra'
import { EpisodeList } from '../components/EpisodeList'
import { PlayIcon, PosterCard, toCard } from '../components/PosterCard'
import { Rail, RailItem } from '../components/Rail'
import { StatusMenu } from '../components/StatusMenu'
import { TrailerOverlay } from '../components/TrailerOverlay'
import { Badge, ErrorState, Skeleton, useDismiss } from '../components/ui'

/** AniList and Jikan describe an anime differently; only the overlap is used. */
interface AnimeDetail {
  id: number
  source: 'anilist' | 'myanimelist' | 'corpus'
  title: string
  partial?: boolean
  listStatus?: string
  progress?: number
  episodeCount?: number
  /** Finished rewatches, what the trackers show as "rewatched N times". */
  repeat?: number
  /** 0-100, the trackers' raw scale; 0 is unrated. */
  score?: number
  bookmark?: Bookmark
  /** An episode left part way, which beats the tracker's count for "continue". */
  resume?: { episode: number; position: number; duration: number }
  /** Which trackers hold this title at all. The catalogues do not fully overlap. */
  sync?: { anilist: boolean; mal: boolean }
  malId?: number
  anime: {
    trailer?: { id?: string; site?: string } | null
    title?: { romaji?: string; english?: string; native?: string }
    description?: string
    coverImage?: { extraLarge?: string; color?: string }
    bannerImage?: string
    episodes?: number
    format?: string
    status?: string
    genres?: string[]
    averageScore?: number
    season?: string
    seasonYear?: number
    // Jikan shape
    romaji?: string
    cover?: string
    Description?: string
  }
}

/**
 * The synopsis, clamped but openable. The toggle appears only when there is
 * more to read — a button that expands nothing is worse than none.
 */
function Synopsis({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  const [clamped, setClamped] = useState(false)
  const body = useRef<HTMLParagraphElement>(null)

  useEffect(() => {
    const el = body.current
    if (!el) return
    const check = () => setClamped(el.scrollHeight > el.clientHeight + 4)
    check()
    window.addEventListener('resize', check)
    return () => window.removeEventListener('resize', check)
  }, [text])

  return (
    <div className="mt-3 max-w-3xl">
      <p
        ref={body}
        className={cx(
          'text-sm leading-relaxed text-base-300',
          !open && 'line-clamp-4',
        )}
      >
        {text}
      </p>
      {(clamped || open) && (
        <button
          onClick={() => setOpen((v) => !v)}
          className="mt-1 text-xs font-medium text-accent-400 transition-colors hover:text-accent-300"
        >
          {open ? 'Show less' : 'Show more'}
        </button>
      )}
    </div>
  )
}

export function Anime() {
  const { animeId } = useParams()
  const id = Number(animeId)

  const detail = useQuery({
    enabled: Number.isFinite(id) && id !== 0,
    queryKey: ['anime', id],
    queryFn: () => api.get<AnimeDetail>(`/api/anime/${id}`),
  })

  const episodes = useEpisodes(id)
  const franchise = useFranchise(id !== 0 ? id : undefined)
  const recommended = useRecommendations(id > 0 ? id : undefined)

  const qc = useQueryClient()

  // Episodes join a queue worked one at a time, so the button reports how many
  // of this show are still waiting rather than just "queued".
  const queue = useQuery({
    queryKey: ['download-queue'],
    queryFn: () => api.get<{ waiting: Record<string, number> }>('/api/download/queue'),
    // Only worth watching closely while something is actually queued.
    refetchInterval: (q) =>
      Object.keys(q.state.data?.waiting ?? {}).length ? 5000 : 30_000,
  })
  const waiting = queue.data?.waiting?.[String(id)] ?? 0

  const refreshQueue = () => qc.invalidateQueries({ queryKey: ['download-queue'] })

  const downloadAll = useMutation({
    mutationFn: () => api.post<{ queued: number }>('/api/download/all', { animeId: id }),
    onSuccess: refreshQueue,
  })

  const cancelAll = useMutation({
    mutationFn: () => api.del(`/api/download/all/${id}`),
    onSuccess: refreshQueue,
  })

  const list = episodes.data?.items ?? []
  const progress = detail.data?.progress ?? 0
  const resume = detail.data?.resume
  const now = useNow() / 1000
  const next = list.find((e) => e.number > progress)
  const total = detail.data?.episodeCount || detail.data?.anime.episodes || list.length
  // An episode stopped part way comes first: the tracker's count can already
  // be past it, and "Continue episode 3" pointed away from minute 18 of 2.
  const first = resume?.episode ?? next?.number ?? (Math.min(progress + 1, list.length || progress + 1) || 1)
  const firstLabel = list.find((e) => e.number === first)?.display ?? first
  const finished =
    !resume &&
    (detail.data?.listStatus === 'COMPLETED' || (progress > 0 && total > 0 && progress >= total && !next))
  const unaired = !resume && !finished && progress > 0 && !!next?.airDate && next.airDate > now
  const action = resume
    ? { kind: 'play' as const, label: `Continue episode ${firstLabel} · ${clockTime(resume.position)}` }
    : finished
      ? { kind: 'rewatch' as const }
      : unaired
        ? { kind: 'wait' as const, label: `Episode ${next!.display} airs ${relativeTime(next!.airDate!)}` }
        : { kind: 'play' as const, label: `${progress > 0 ? 'Continue' : 'Watch'} episode ${firstLabel}` }
  const ready = detail.isSuccess && episodes.isSuccess

  const navigate = useNavigate()
  const rewatch = useSetStatus()
  const startRewatch = () =>
    rewatch.mutate(
      { animeId: id, status: 'REPEATING' },
      { onSuccess: () => navigate(`/watch/${id}/1`) },
    )

  // Finding a release is the slow half of play, so prefetch while the page is
  // read (server ignores it if already held). Above the early returns, since a
  // hook can't be conditional.
  useEffect(() => {
    if (!ready || !Number.isFinite(id) || !id || !first || action.kind !== 'play') return
    void api.post('/api/prepare', { animeId: id, episode: first }).catch(() => {})
  }, [ready, id, first, action.kind])

  if (!Number.isFinite(id) || id === 0) {
    return <ErrorState error={new Error('Unknown anime')} />
  }
  if (detail.isError) {
    return <ErrorState error={detail.error} retry={() => detail.refetch()} />
  }

  const media = detail.data?.anime
  const cover = media?.coverImage?.extraLarge ?? media?.cover
  const banner = media?.bannerImage
  const colour = media?.coverImage?.color
  const description = (media?.description ?? media?.Description ?? '').replace(/<[^>]+>/g, '')

  return (
    <div className="space-y-8">
      <section
        className={cx(
          'tinted relative -mx-4 overflow-visible bg-base-950 sm:mx-0 sm:rounded-xl',
          // The banner is a fixed-height layer and the section doesn't clip, so
          // a short synopsis left it hanging over the heading below.
          banner && 'min-h-[26rem]',
        )}
        style={colour ? ({ '--tint': tint(colour, 0.22) } as React.CSSProperties) : undefined}
      >
        {/* Its own layer so the section stays overflow-visible (menus were
            clipped by the banner's corners); fixed height, not inset-0, so
            expanding the synopsis doesn't re-crop the image. */}
        {banner && (
          <div className="absolute inset-x-0 top-0 h-[26rem] overflow-hidden sm:rounded-t-xl">
            <img src={banner} alt="" className="size-full object-cover object-center" />
            <div className="absolute inset-0 bg-gradient-to-t from-base-950 via-base-950/85 to-base-950/40" />
          </div>
        )}

        <div className="relative flex flex-col gap-5 p-4 sm:flex-row sm:p-6">
          {detail.isPending ? (
            <Skeleton className="h-64 w-44 shrink-0" />
          ) : cover ? (
            <img
              src={cover}
              alt=""
              className="h-64 w-44 shrink-0 rounded-card object-cover shadow-lift"
            />
          ) : null}

          <div className="min-w-0 flex-1">
            {detail.isPending ? (
              <Skeleton className="h-8 w-2/3" />
            ) : (
              <h1 className="text-2xl leading-tight font-semibold text-white text-balance sm:text-3xl">
                {detail.data?.title}
              </h1>
            )}

            <div className="mt-2.5 flex flex-wrap items-center gap-2 text-sm text-base-300">
              {media?.averageScore ? (
                <span className="rounded-md bg-accent-500/15 px-2 py-0.5 font-medium text-accent-300">
                  ★ {media.averageScore}
                </span>
              ) : null}
              {media?.format && <span>{media.format.replace('_', ' ')}</span>}
              {media?.episodes ? <span>· {media.episodes} episodes</span> : null}
              {media?.seasonYear ? <span>· {media.seasonYear}</span> : null}
              {detail.data?.source === 'corpus' && (
                <Badge>Limited info — MyAnimeList unreachable</Badge>
              )}
            </div>

            {media?.genres && media.genres.length > 0 && (
              <div className="mt-2.5 flex flex-wrap gap-1.5">
                {media.genres.slice(0, 6).map((genre) => (
                  <Link
                    key={genre}
                    to={`/browse?genres=${encodeURIComponent(genre)}`}
                    className="rounded-md bg-base-850/80 px-2 py-0.5 text-xs text-base-300 backdrop-blur-sm transition-colors hover:bg-base-750 hover:text-white"
                  >
                    {genre}
                  </Link>
                ))}
              </div>
            )}

            {description && <Synopsis text={description} />}

            <div className="mt-5 flex flex-wrap items-center gap-2">
              {action.kind === 'play' ? (
                <Link
                  to={`/watch/${id}/${first}`}
                  className="flex items-center gap-2 rounded-md bg-white px-4 py-2 text-sm font-semibold text-base-950 transition-transform hover:scale-[1.02] active:scale-95"
                  style={colour ? { boxShadow: `0 0 24px ${tint(colour, 0.35)}` } : undefined}
                >
                  <PlayIcon />
                  {action.label}
                </Link>
              ) : action.kind === 'rewatch' ? (
                <button
                  onClick={startRewatch}
                  disabled={rewatch.isPending}
                  title="Marks the show as rewatching, which counts progress from the start again"
                  className="flex items-center gap-2 rounded-md bg-white px-4 py-2 text-sm font-semibold text-base-950 transition-transform hover:scale-[1.02] active:scale-95 disabled:opacity-60"
                  style={colour ? { boxShadow: `0 0 24px ${tint(colour, 0.35)}` } : undefined}
                >
                  <RewatchIcon />
                  {rewatch.isPending ? 'Starting…' : 'Rewatch from episode 1'}
                </button>
              ) : (
                <span
                  title="You are caught up. The next episode has not aired yet."
                  className="flex items-center gap-2 rounded-md bg-base-800/90 px-4 py-2 text-sm font-medium text-base-200 backdrop-blur-sm"
                >
                  <ClockIcon />
                  {action.label}
                </span>
              )}

              <StatusMenu animeId={id} current={detail.data?.listStatus} />
              {detail.isSuccess && <ScoreSelect animeId={id} score={detail.data.score ?? 0} />}
              {detail.isSuccess && (
                <FavouriteButton animeId={id} bookmark={detail.data.bookmark} />
              )}

              {(detail.data?.repeat ?? 0) > 0 && (
                <span
                  title="Completed rewatches, as counted on your list"
                  className="rounded-md bg-base-800/90 px-3 py-1.5 text-sm text-base-300 backdrop-blur-sm"
                >
                  Rewatched ×{detail.data!.repeat}
                </span>
              )}

              {waiting > 0 ? (
                <button
                  onClick={() => cancelAll.mutate()}
                  disabled={cancelAll.isPending}
                  title="Removes what is still waiting; the episode downloading now is left alone"
                  className="flex items-center gap-2 rounded-md bg-base-800/90 px-4 py-2 text-sm font-medium text-base-100 backdrop-blur-sm transition-colors hover:bg-base-700 disabled:opacity-50"
                >
                  <span className="size-2 animate-pulse rounded-full bg-accent-400" />
                  {waiting} queued · stop
                </button>
              ) : (
                <button
                  onClick={() => downloadAll.mutate()}
                  disabled={downloadAll.isPending}
                  className="rounded-md bg-base-800/90 px-4 py-2 text-sm font-medium text-base-100 backdrop-blur-sm transition-colors hover:bg-base-700 disabled:opacity-50"
                >
                  {downloadAll.isPending ? 'Queuing…' : 'Download all'}
                </button>
              )}
            </div>

            {downloadAll.isError && (
              <p className="mt-2 text-xs text-recap">{(downloadAll.error as Error).message}</p>
            )}

            <AutoDownload animeId={id} />
            <SyncNote sync={detail.data?.sync} />
            {detail.isSuccess && (
              <ExternalLinks
                anilist={detail.data.source === 'anilist' ? id : undefined}
                mal={detail.data.malId}
                trailer={
                  media?.trailer?.site?.toLowerCase() === 'youtube' ? media.trailer.id : undefined
                }
              />
            )}
            {detail.isSuccess && <Note animeId={id} bookmark={detail.data.bookmark} />}
          </div>
        </div>
      </section>

      {(franchise.data?.seasons?.length ?? 0) > 1 && (
        <section>
          <h2 className="mb-3 section-title">
            Seasons &amp; related
          </h2>
          <div className="no-scrollbar flex gap-2 overflow-x-auto pb-1">
            {franchise.data!.seasons.map((season) => (
              <SeasonChip key={season.id} season={season} current={season.id === id} />
            ))}
          </div>
        </section>
      )}

      <CharacterRail animeId={id} />

      <ShowExtra animeId={id} />

      <section>
        <div className="mb-3 flex items-baseline justify-between">
          <h2 className="section-title">Episodes</h2>
          {list.length > 0 && <span className="text-xs text-base-500">{list.length} total</span>}
        </div>

        {episodes.isPending ? (
          <Skeleton className="h-80 w-full" />
        ) : list.length === 0 ? (
          <div className="surface p-6 text-center">
            <p className="text-sm text-base-400">No episode list for this show yet.</p>
            <Link
              to={`/watch/${id}/1`}
              className="mt-3 inline-block rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
            >
              Try episode 1 anyway
            </Link>
          </div>
        ) : (
          <EpisodeList
            animeId={id}
            episodes={list}
            progress={progress}
            cover={banner ?? cover}
            upNext={action.kind === 'play' ? first : undefined}
            selectable
          />
        )}
      </section>

      {(recommended.data?.items?.length ?? 0) > 0 && (
        <Rail title={recommended.data?.source === 'genre' ? 'Similar genres' : 'More like this'}>
          {recommended.data!.items.map((anime: DiscoverItem) => (
            <RailItem key={anime.id}>
              <PosterCard anime={toCard(anime)} />
            </RailItem>
          ))}
        </Rail>
      )}
    </div>
  )
}

function RewatchIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path
        d="M4 12a8 8 0 0 1 13.7-5.6L20 8.5M20 4v4.5h-4.5M20 12a8 8 0 0 1-13.7 5.6L4 15.5M4 20v-4.5h4.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function ClockIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4 text-base-400" aria-hidden>
      <circle cx="12" cy="12" r="8.5" fill="none" stroke="currentColor" strokeWidth="1.8" />
      <path d="M12 7.5V12l3 2" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
    </svg>
  )
}

// Per show, not per account: overrides the global default either way, so one
// series can auto-download without turning it on for everything.
function AutoDownload({ animeId }: { animeId: number }) {
  const prefs = usePrefs(animeId)
  const setPref = useSetPref()

  const on = prefs.data?.effective['autodownload.enabled'] === 'true'
  if (!prefs.isSuccess) return null

  return (
    <button
      onClick={() =>
        setPref.mutate({ animeId, key: 'autodownload.enabled', value: String(!on) })
      }
      disabled={setPref.isPending}
      className="mt-3 flex items-center gap-2 text-xs text-base-400 transition-colors hover:text-base-200"
    >
      <span
        className={cx(
          'relative h-4 w-7 rounded-full transition-colors',
          on ? 'bg-accent-500' : 'bg-base-700',
        )}
      >
        <span
          className={cx(
            'absolute top-0.5 size-3 rounded-full bg-white transition-all',
            on ? 'left-[0.875rem]' : 'left-0.5',
          )}
        />
      </span>
      {on ? 'Auto-downloading new episodes' : 'Auto-download new episodes'}
    </button>
  )
}

// Where the show lives elsewhere, and the trailer without leaving the page.
function ExternalLinks({ anilist, mal, trailer }: { anilist?: number; mal?: number; trailer?: string }) {
  const [showing, setShowing] = useState(false)
  if (!anilist && !mal && !trailer) return null
  const link = 'rounded-md bg-base-850/80 px-2.5 py-1 text-xs text-base-300 transition-colors hover:bg-base-750 hover:text-white'

  return (
    <div className="mt-3 flex flex-wrap items-center gap-1.5">
      {trailer && (
        <button onClick={() => setShowing(true)} className={link}>
          ▶ Trailer
        </button>
      )}
      {anilist && (
        <a href={`https://anilist.co/anime/${anilist}`} target="_blank" rel="noreferrer" className={link}>
          AniList ↗
        </a>
      )}
      {mal && (
        <a href={`https://myanimelist.net/anime/${mal}`} target="_blank" rel="noreferrer" className={link}>
          MyAnimeList ↗
        </a>
      )}
      {showing && trailer && <TrailerOverlay videoId={trailer} onClose={() => setShowing(false)} />}
    </div>
  )
}

// Show rating, 1-10 over the trackers' 0-100 scale.
function ScoreSelect({ animeId, score }: { animeId: number; score: number }) {
  const qc = useQueryClient()
  const set = useMutation({
    mutationFn: (value: number) => api.post('/api/score', { animeId, score: value }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['anime', animeId] })
      void qc.invalidateQueries({ queryKey: ['library'] })
    },
  })
  const tens = Math.round(score / 10)

  // Not a native select: the OS paints those white on white here.
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)
  const trigger = useRef<HTMLButtonElement>(null)
  const box = trigger.current?.getBoundingClientRect()

  return (
    <div className="relative" ref={ref}>
      <button
        ref={trigger}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Your score"
        onClick={() => setOpen((v) => !v)}
        className={cx(
          'flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium backdrop-blur-sm transition-colors',
          tens > 0
            ? 'bg-accent-500/20 text-accent-400 hover:bg-accent-500/30'
            : 'bg-base-800/90 text-base-200 hover:bg-base-700',
        )}
      >
        <span>★</span>
        <span>{tens > 0 ? `${tens}/10` : 'Rate'}</span>
      </button>

      {open && box &&
        createPortal(
          <div
            role="menu"
            data-portal-menu
            style={{
              top: Math.min(box.bottom + 6, window.innerHeight - 340),
              left: Math.max(8, Math.min(box.left, window.innerWidth - 120)),
            }}
            className="fixed z-50 w-28 animate-rise overflow-hidden rounded-lg border border-base-700 bg-base-850 py-1 shadow-xl shadow-black/50"
          >
            {Array.from({ length: 11 }, (_, i) => 10 - i).map((n) => (
              <button
                key={n}
                role="menuitemradio"
                aria-checked={n === tens}
                disabled={set.isPending}
                onClick={() => {
                  set.mutate(n * 10)
                  close()
                }}
                className={cx(
                  'flex w-full items-center justify-between px-3 py-1.5 text-left text-sm tabular-nums transition-colors',
                  n === tens ? 'text-accent-400' : 'text-base-200 hover:bg-base-750',
                )}
              >
                {n === 0 ? 'Unrated' : n}
                {n === tens && n > 0 && <span>★</span>}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </div>
  )
}

function useSetBookmark(animeId: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (b: Partial<Bookmark>) => api.post('/api/bookmarks', { animeId, ...b }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['anime', animeId] })
      void qc.invalidateQueries({ queryKey: ['bookmarks'] })
    },
  })
}

// Local only: a favourite is a shelf in kuro, not a list status on a tracker.
function FavouriteButton({ animeId, bookmark }: { animeId: number; bookmark?: Bookmark }) {
  const set = useSetBookmark(animeId)
  const on = !!bookmark?.favourite

  return (
    <button
      onClick={() => set.mutate({ ...bookmark, favourite: !on })}
      disabled={set.isPending}
      aria-pressed={on}
      title={on ? 'Remove from favourites' : 'Add to favourites'}
      className={cx(
        'rounded-md px-3 py-1.5 text-sm backdrop-blur-sm transition-colors',
        on ? 'bg-accent-500/20 text-accent-300' : 'bg-base-800/90 text-base-200 hover:bg-base-700',
      )}
    >
      {on ? '♥ Favourite' : '♡ Favourite'}
    </button>
  )
}

// A private note, saved when the field loses focus.
function Note({ animeId, bookmark }: { animeId: number; bookmark?: Bookmark }) {
  const set = useSetBookmark(animeId)
  const [text, setText] = useState(bookmark?.note ?? '')
  const [open, setOpen] = useState(!!bookmark?.note)
  useEffect(() => setText(bookmark?.note ?? ''), [bookmark?.note])

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="mt-2 text-xs text-base-500 transition-colors hover:text-base-200"
      >
        Add a note
      </button>
    )
  }
  return (
    <textarea
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => {
        if (text !== (bookmark?.note ?? '')) set.mutate({ ...bookmark, note: text })
      }}
      placeholder="Your note — only you see it"
      rows={2}
      className="mt-3 w-full max-w-xl rounded-md border border-base-800 bg-base-950/60 px-3 py-2 text-sm text-base-100 placeholder:text-base-600 focus:border-accent-500 focus:outline-none"
    />
  )
}

function SeasonChip({ season, current }: { season: Season; current: boolean }) {
  // What it was tagged first: marking a season completed does not set a
  // progress count, so inferring the label from progress left it blank.
  const tag = statusLabel(season.listStatus)
  const watched =
    season.progress > 0
      ? season.episodes && season.progress >= season.episodes
        ? 'Finished'
        : `${season.progress} watched`
      : ''

  return (
    <Link
      to={`/anime/${season.id}`}
      className={cx(
        'flex w-56 shrink-0 items-center gap-2.5 rounded-lg border p-2 transition-colors',
        current
          ? 'border-accent-500/50 bg-accent-500/10'
          : 'border-base-850 bg-base-900/40 hover:bg-base-850',
      )}
    >
      {season.cover ? (
        <img src={season.cover} alt="" loading="lazy" className="h-14 w-10 shrink-0 rounded object-cover" />
      ) : (
        <div className="h-14 w-10 shrink-0 rounded bg-base-850" />
      )}
      <div className="min-w-0">
        <p className="line-clamp-2 text-xs leading-snug text-base-100">
          {season.english || season.romaji || `Anime ${season.id}`}
        </p>
        <p className="mt-0.5 text-[11px] text-base-500">
          {season.year ?? '—'}
          {season.episodes ? ` · ${season.episodes} ep` : ''}
        </p>
        {(tag || watched) && (
          <p className="text-[11px] text-accent-400">{tag || watched}</p>
        )}
      </div>
    </Link>
  )
}

// AniList and MyAnimeList hold different catalogues, so a show can be missing
// from one. Saying so explains why progress reaches only one tracker.
function SyncNote({ sync }: { sync?: { anilist: boolean; mal: boolean } }) {
  if (!sync) return null

  const targets = [sync.anilist && 'AniList', sync.mal && 'MyAnimeList'].filter(Boolean)
  if (targets.length === 0) {
    return <p className="mt-3 text-xs text-base-500">Not on either tracker — progress stays in kuro.</p>
  }

  return (
    <p className="mt-3 text-xs text-base-500">
      Syncs to {targets.join(' and ')}
      {targets.length === 1 && ' only'}.
    </p>
  )
}
