import { useCallback, useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  api,
  ApiError,
  beacon,
  query,
  type PlaySession,
  type SkipRange,
  type StreamInfo,
} from '../lib/api'
import { cx } from '../lib/format'
import { useEpisodes, usePrefs, useSetPref } from '../lib/queries'
import { Anime4KDialog } from '../components/Anime4KDialog'
import { CharacterRail } from '../components/CharacterRail'
import { EpisodeList, isUnaired } from '../components/EpisodeList'
import { ReleasePicker } from '../components/ReleasePicker'
import { StatusMenu } from '../components/StatusMenu'
import { Badge, Button, ErrorState, Segmented, Spinner, useDocumentTitle } from '../components/ui'

type AudioChoice = 'sub' | 'dub' | 'either'
import { Player } from '../player/Player'

export function Watch() {
  const { animeId, episode } = useParams()
  const id = Number(animeId)
  const ep = Number(episode)
  const navigate = useNavigate()
  const qc = useQueryClient()

  const prefs = usePrefs(id)
  const setPref = useSetPref()
  const episodes = useEpisodes(id)

  const effective = prefs.data?.effective ?? {}
  const flag = (key: string) => effective[key] === 'true'
  const subLanguagesRaw = effective['subtitle.languages']
  const subLanguages = useMemo(() => {
    try {
      const parsed = JSON.parse(subLanguagesRaw || '["en"]')
      return Array.isArray(parsed) && parsed.length > 0 ? (parsed as string[]) : ['en']
    } catch {
      return ['en']
    }
  }, [subLanguagesRaw])
  const external = effective['playback.player'] !== 'browser' && !!effective['playback.player']

  // Write where the shown value came from, or a per-show override keeps
  // winning and the switch looks stuck.
  const overrides = prefs.data?.overrides ?? {}
  const setFlag = (key: string) => (v: boolean) =>
    setPref.mutate({
      key,
      value: String(v),
      animeId: key in overrides ? id : undefined,
    })

  // Per-episode choices carry their episode: reset in an effect, they reached
  // the next episode's play a render late, hand-picked release and all.
  const epKey = `${id}-${ep}`

  // Waiting for preferences before resolving a release stops the browser
  // player starting a stream that mpv is about to take over.
  // Opting into a raw is per attempt, never remembered: it is a "this episode
  // is not subbed yet" decision, not a preference.
  const [rawFor, setRawFor] = useState('')
  const allowRaw = rawFor === epKey
  const setAllowRaw = (on: boolean) => setRawFor(on ? epKey : '')

  // Sub or dub for this show: saved as its own preference, so the next episode
  // keeps it without rewriting the setting for every other show. Held here
  // too, so the switch is instant rather than waiting on the save.
  const [audioPick, setAudioPick] = useState<{ key: number; audio: AudioChoice } | null>(null)
  const audio = audioPick?.key === id ? audioPick.audio : null
  const setAudio = (a: AudioChoice | null) => {
    setAudioPick(a ? { key: id, audio: a } : null)
    if (a) setPref.mutate({ key: 'audio.prefer', value: a, animeId: id })
  }
  const wantAudio: AudioChoice =
    audio ?? ((effective['audio.prefer'] as AudioChoice | undefined) ?? 'sub')

  // A release chosen by hand, for this episode only.
  const [hashPick, setHashPick] = useState<{ key: string; hash: string } | null>(null)
  const infoHash = hashPick?.key === epKey ? hashPick.hash : undefined
  const setInfoHash = (hash?: string) => setHashPick(hash ? { key: epKey, hash } : null)
  const [picking, setPicking] = useState(false)

  const play = useQuery({
    enabled: id !== 0 && ep > 0 && prefs.isSuccess,
    queryKey: ['play', id, ep, external, allowRaw, wantAudio, infoHash],
    // Audio only when chosen here, so a held release in the other one is replaced.
    queryFn: ({ signal }) =>
      api.post<PlaySession>('/api/play', {
        animeId: id, episode: ep, external, allowRaw, audio: audio ?? undefined, infoHash,
      }, signal),
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
  })

  const playingInMpv = !!play.data && play.data.player !== 'browser'

  // mpv opens the file itself and reports over its own IPC socket, so there is
  // nothing here to transcode for.
  const source = playingInMpv ? undefined : play.data?.streamUrl
  const stream = useQuery({
    enabled: !!source,
    queryKey: ['stream', id, ep, source, wantAudio],
    queryFn: () =>
      api.post<StreamInfo>(
        `/api/stream/open${query({ id, episode: ep, source, audio: wantAudio })}`,
      ),
    retry: 1,
    staleTime: Infinity,
    // Never carried across a mount: leaving the page closes the session, so a
    // cached answer would describe a playlist that is no longer there.
    gcTime: 0,
  })

  // AniSkip matches on the episode's real length, known only once the stream is
  // open. Remembered, since an audio switch briefly clears it and the query
  // would fall back to the empty answer from before the stream existed.
  const [lengthFor, setLengthFor] = useState<{ key: string; seconds: number } | null>(null)
  const episodeLength = lengthFor?.key === epKey ? lengthFor.seconds : 0
  useEffect(() => {
    const seconds = Math.round(stream.data?.duration ?? 0)
    if (seconds > 0) setLengthFor({ key: epKey, seconds })
  }, [stream.data?.duration, epKey])

  const skips = useQuery({
    enabled: id !== 0 && ep > 0 && episodeLength > 0,
    queryKey: ['skips', id, ep, episodeLength],
    queryFn: () =>
      api.get<{ ranges: SkipRange[]; autoSkipOp: boolean; autoSkipEd: boolean }>(
        `/api/episode/skips${query({ anime: id, episode: ep, duration: episodeLength })}`,
      ),
    staleTime: 60 * 60_000,
  })

  const download = useMutation({
    meta: { inline: true },
    mutationFn: () => api.post('/api/download', { animeId: id, episode: ep }),
  })
  // The page no longer remounts, so "Queued" would follow you to the next one.
  const resetDownload = download.reset
  useEffect(() => resetDownload(), [id, ep, resetDownload])

  // Offering to download an episode that is already on disk queues a job whose
  // only outcome is to notice that and stop.
  const downloads = useQuery({
    queryKey: ['downloads'],
    queryFn: () =>
      api.get<{ items: Array<{ animeId?: number; episodes: string[]; percent: number }> }>(
        '/api/downloads',
      ),
    staleTime: 5_000,
    refetchInterval: (q) =>
      (q.state.data?.items ?? []).some((d) => d.animeId === id && d.percent < 100) ? 5_000 : false,
  })
  // A season pack lists every episode it serves.
  const held = (downloads.data?.items ?? []).some(
    (d) => d.animeId === id && d.episodes.includes(String(ep)) && d.percent >= 100,
  )

  const detail = useQuery({
    enabled: id !== 0,
    queryKey: ['anime', id],
    queryFn: () => api.get<{ title: string; listStatus?: string }>(`/api/anime/${id}`),
    staleTime: 5 * 60_000,
  })

  const list = episodes.data?.items ?? []
  const current = list.find((e) => e.number === ep)
  // The next episode worth playing: with the filler rule on, the next that is
  // neither filler nor a recap. Mirrors store.NextEpisode, which also refuses
  // one still to broadcast: auto-next into it lands on a failed search.
  const skipFiller = flag('playback.skip_filler')
  const next = list.find(
    (e) =>
      e.number > ep &&
      !isUnaired(e) &&
      !(skipFiller && (e.filler === 'filler' || e.recap)),
  )
  const hasNext = !!next

  // The release filename is what the server resolved; it is not what anyone
  // calls the episode. Fall back to it only when nothing better is known.
  const showTitle = detail.data?.title ?? play.data?.title ?? ''
  const episodeLabel = [`Episode ${current?.display ?? ep}`, current?.titleEn]
    .filter(Boolean)
    .join(' · ')
  useDocumentTitle(showTitle ? `${showTitle} · Ep ${current?.display ?? ep}` : undefined)

  const reportProgress = useCallback(
    (position: number, duration: number, played: number) => {
      // sendBeacon survives the tab closing, which a fetch does not.
      beacon('/api/progress', { animeId: id, episode: ep, position, duration, played })
    },
    [id, ep],
  )

  const nextNumber = next?.number
  const goNext = useCallback(() => {
    if (nextNumber) navigate(`/watch/${id}/${nextNumber}`)
  }, [nextNumber, id, navigate])

  // Swapping in the searching panel would destroy the element holding
  // fullscreen, so after the first episode the player waits with its own spinner.
  const [streamed, setStreamed] = useState(false)
  useEffect(() => {
    if (stream.data) setStreamed(true)
  }, [stream.data])

  // The player has already reported the end as a position; the up-next card
  // decides what happens after, counting down only when auto-next is on.
  // Which episode ended, not just that one did: a plain flag survives the
  // navigation and the finished countdown fires again, skipping an episode.
  const [endedKey, setEndedKey] = useState<string | null>(null)
  const ended = endedKey === `${id}-${ep}`
  const onEnded = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ['episodes', id] })
    void qc.invalidateQueries({ queryKey: ['anime', id] })
    setEndedKey(`${id}-${ep}`)
  }, [id, ep, qc])
  const clearEnded = useCallback(() => setEndedKey(null), [])

  // The idle reaper took the session while the tab was backgrounded. Replaying
  // /api/play revives a suspended torrent, then reopening the stream rebuilds
  // the transcode session under the same playlist URL — no refresh needed.
  // Throws when the reopen fails, so the player shows it instead of rebuilding
  // against a session that is not there.
  const recoverSession = useCallback(async () => {
    await qc.invalidateQueries({ queryKey: ['play', id, ep] }, { throwOnError: true })
    await qc.invalidateQueries({ queryKey: ['stream', id, ep] }, { throwOnError: true })
  }, [qc, id, ep])

  // Releasing the transcode session frees an ffmpeg process; leaving it to the
  // idle reaper keeps a CPU busy for a minute after leaving the page.
  useEffect(() => {
    const streamId = stream.data?.id
    return () => {
      if (streamId) void api.del(`/api/stream/${streamId}`).catch(() => {})
    }
  }, [stream.data?.id])

  const autoSkip = useMemo(
    () => ({ op: flag('playback.autoskip_op'), ed: flag('playback.autoskip_ed') }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [effective['playback.autoskip_op'], effective['playback.autoskip_ed']],
  )

  // Per episode, unlike the toggles beside it: the saved default is untouched.
  const [upscale, setUpscale] = useState<{ enabled: boolean; mode: string } | null>(null)
  const [tuning, setTuning] = useState(false)
  useEffect(() => setUpscale(null), [id, ep])

  const upscaling = upscale ?? {
    enabled: flag('playback.anime4k'),
    mode: effective['playback.anime4k_mode'] ?? 'A',
  }

  if (!id || !ep) return <ErrorState error={new Error('Unknown episode')} />

  const failure =
    play.error instanceof ApiError
      ? (play.error.body as { rawAvailable?: boolean; rawTitle?: string } | undefined)
      : undefined

  return (
    <div className="bg-base-950">
      <div className="mx-auto w-full max-w-[1600px] px-0 sm:px-6 sm:py-4">
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
          <div className="min-w-0">
            {/* No overflow-hidden: clipping a hardware-composited video turned
                the picture black. The player rounds its own corners. */}
            <div className="relative bg-black sm:rounded-xl">
              {play.isError && !streamed ? (
                <NoRelease
                  message={(play.error as Error).message}
                  animeId={id}
                  rawAvailable={!allowRaw && !!failure?.rawAvailable}
                  rawTitle={failure?.rawTitle}
                  onRetry={() => play.refetch()}
                  onPlayRaw={() => setAllowRaw(true)}
                  onPick={() => setPicking(true)}
                />
              ) : play.isPending && !streamed ? (
                <Searching />
              ) : playingInMpv ? (
                <MpvPanel title={play.data?.title ?? showTitle} episode={ep} player={play.data?.player ?? 'mpv'} />
              ) : (
                <Player
                  stream={stream.data}
                  startAt={play.data?.startAt ?? 0}
                  skips={skips.data?.ranges ?? []}
                  autoSkip={autoSkip}
                  autoPlay={flag('playback.autoplay')}
                  subLanguages={subLanguages}
                  upscale={upscaling}
                  title={showTitle}
                  subtitle={episodeLabel}
                  onProgress={reportProgress}
                  onEnded={onEnded}
                  onNext={hasNext ? goNext : undefined}
                  // Back into the finished episode: the countdown must not
                  // take the viewer away mid-rewatch.
                  onResume={clearEnded}
                  onSessionLost={recoverSession}
                  // Inside the player, or fullscreen hides it while it counts.
                  // The failure too, or swapping the player out drops fullscreen —
                  // not over a stream still there, whose recovery is the player's.
                  overlay={
                    play.isError && !stream.data ? (
                      <NoRelease
                        inPlayer
                        message={(play.error as Error).message}
                        animeId={id}
                        rawAvailable={!allowRaw && !!failure?.rawAvailable}
                        rawTitle={failure?.rawTitle}
                        onRetry={() => play.refetch()}
                        onPlayRaw={() => setAllowRaw(true)}
                        onPick={() => setPicking(true)}
                      />
                    ) : stream.isError && !stream.data ? (
                      // The release was found; opening it is what failed.
                      <NoRelease
                        inPlayer
                        message={(stream.error as Error).message}
                        animeId={id}
                        onRetry={() => stream.refetch()}
                        onPick={() => setPicking(true)}
                      />
                    ) : ended && next ? (
                      <UpNext
                        episode={next.display || next.number}
                        title={next.titleEn}
                        auto={flag('playback.autonext')}
                        onPlay={goNext}
                        onCancel={() => setEndedKey(null)}
                      />
                    ) : null
                  }
                />
              )}
            </div>

            <div className="space-y-3 p-3 sm:px-0 sm:pt-4">
              <div className="flex flex-wrap items-start gap-x-4 gap-y-3">
                {/* What is playing, and the way back to its page. */}
                <div className="mr-auto min-w-0">
                  <Link
                    to={`/anime/${id}`}
                    className="group inline-flex items-center gap-1.5 font-display text-xl font-bold tracking-tight text-white transition-colors hover:text-accent-200 sm:text-2xl"
                  >
                    <span className="line-clamp-1">{showTitle || 'Series'}</span>
                    <span className="text-base-500 transition-transform group-hover:translate-x-0.5">›</span>
                  </Link>
                  <p className="mt-0.5 line-clamp-1 text-sm text-base-400">{episodeLabel}</p>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  <StatusMenu animeId={id} current={detail.data?.listStatus} />

                  <Button onClick={() => setPicking(true)}>
                    {infoHash ? 'Release chosen' : 'Choose release'}
                  </Button>

                  <Button
                    onClick={() => download.mutate()}
                    disabled={held || download.isPending || download.isSuccess}
                  >
                    {held ? 'Downloaded' : download.isSuccess ? 'Queued' : 'Download'}
                  </Button>

                  {hasNext && (
                    <Button variant="primary" onClick={goNext}>
                      Next episode ›
                    </Button>
                  )}
                </div>
              </div>

              <div className="flex flex-wrap items-center gap-2 rounded-lg bg-base-900/60 p-2.5 shadow-card">
                {play.data?.source && (
                  <Badge tone={play.data.source === 'local' ? 'accent' : 'neutral'}>
                    {play.data.source === 'local' ? 'Local file' : 'Torrent'}
                  </Badge>
                )}

                {/* Three scopes here; each group says which. */}
                <span className="text-[10px] font-semibold tracking-wider text-base-500 uppercase">
                  This show
                </span>
                {/* Switching resolves a different release, so it reloads rather
                    than swapping a track — most releases carry one language. */}
                <Segmented
                  size="sm"
                  value={wantAudio}
                  onChange={setAudio}
                  options={[
                    { value: 'sub', label: 'Sub' },
                    { value: 'dub', label: 'Dub' },
                    { value: 'either', label: 'Either' },
                  ]}
                />

                <Toggle
                  label="Skip filler"
                  on={skipFiller}
                  onChange={(v) =>
                    setPref.mutate({ key: 'playback.skip_filler', value: String(v), animeId: id })
                  }
                />

                <span className="ml-1 border-l border-base-800 pl-3 text-[10px] font-semibold tracking-wider text-base-500 uppercase">
                  Everywhere
                </span>
                {/* Flipping a toggle here becomes the new default, which is
                    what the setting means from then on. */}
                <Toggle
                  label="Auto play"
                  on={flag('playback.autoplay')}
                  onChange={setFlag('playback.autoplay')}
                />
                <Toggle
                  label="Auto next"
                  on={flag('playback.autonext')}
                  onChange={setFlag('playback.autonext')}
                />
                <Toggle
                  label="Skip opening"
                  on={flag('playback.autoskip_op')}
                  onChange={setFlag('playback.autoskip_op')}
                />
                <Toggle
                  label="Skip ending"
                  on={flag('playback.autoskip_ed')}
                  onChange={setFlag('playback.autoskip_ed')}
                />

                {!playingInMpv && (
                  <button
                    onClick={() => setTuning(true)}
                    className={cx(
                      'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                      upscaling.enabled
                        ? 'bg-accent-500/15 text-accent-300 hover:bg-accent-500/25'
                        : 'bg-base-800 text-base-300 hover:bg-base-700',
                    )}
                  >
                    Anime4K{upscaling.enabled ? ` · ${upscaling.mode}` : ''}
                  </button>
                )}
              </div>

              {download.isError && (
                <p className="text-xs text-recap">{(download.error as Error).message}</p>
              )}

              <EpisodeAbout
                title={showTitle}
                label={episodeLabel}
                overview={current?.overview}
                still={current?.still}
                file={play.data?.title}
              />

              {/* The cast is the show's, not this episode's — MyAnimeList has no
                  per-episode cast — so the heading says so. */}
              <CharacterRail animeId={id} limit={8} title="Cast" />
            </div>
          </div>

          <aside className="px-3 pb-8 sm:px-0">
            <h2 className="mb-2 section-title">
              Episodes
            </h2>
            {list.length > 0 ? (
              <EpisodeList animeId={id} episodes={list} current={ep} compact />
            ) : (
              <p className="rounded-lg border border-base-850 p-4 text-sm text-base-500">
                No episode list available.
              </p>
            )}
          </aside>
        </div>
      </div>

      <Anime4KDialog
        open={tuning}
        enabled={upscaling.enabled}
        mode={upscaling.mode}
        onClose={() => setTuning(false)}
        onChange={setUpscale}
      />

      {picking && (
        <ReleasePicker
          animeId={id}
          episode={ep}
          audio={wantAudio}
          current={play.data?.infoHash}
          onClose={() => setPicking(false)}
          onPick={(hash) => {
            setPicking(false)
            setInfoHash(hash)
          }}
        />
      )}
    </div>
  )
}

/**
 * What the episode is, under the player. Without it the page below the video
 * is empty and the only description of what is playing is a release filename.
 */
function EpisodeAbout({
  title,
  label,
  overview,
  still,
  file,
}: {
  title: string
  label: string
  overview?: string
  still?: string
  file?: string
}) {
  if (!title && !overview) return null

  return (
    <section className="flex gap-4 rounded-lg bg-base-900/60 p-4 shadow-card">
      {still && (
        <img
          src={still}
          alt=""
          loading="lazy"
          className="hidden h-24 w-40 shrink-0 rounded-md object-cover sm:block"
        />
      )}
      <div className="min-w-0">
        <h2 className="truncate text-base font-semibold text-base-100">{title}</h2>
        <p className="mt-0.5 text-sm text-accent-400">{label}</p>
        {overview ? (
          <p className="mt-2 line-clamp-3 text-sm leading-relaxed text-base-300">{overview}</p>
        ) : (
          <p className="mt-2 text-sm text-base-500">No synopsis for this episode yet.</p>
        )}
        {/* The release is worth being able to check, but it is not the title. */}
        {file && <p className="mt-2 truncate text-xs text-base-600">{file}</p>}
      </div>
    </section>
  )
}

/**
 * Resolving a release means several throttled indexer searches taking ~ten
 * seconds; a bare spinner that long reads as a hang, so it says what it's doing.
 */
function Searching() {
  const [seconds, setSeconds] = useState(0)

  useEffect(() => {
    const id = window.setInterval(() => setSeconds((s) => s + 1), 1000)
    return () => window.clearInterval(id)
  }, [])

  return (
    <div className="grid aspect-video place-items-center">
      <div className="text-center">
        <Spinner className="mx-auto size-8" />
        <p className="mt-3 text-sm text-base-300">Searching for a release…</p>
        <p className="mt-1 text-xs text-base-500">
          {seconds < 6
            ? 'Checking the indexer under each of the show’s titles'
            : 'Still looking — searches are rate limited'}
        </p>
      </div>
    </div>
  )
}

// A failure here is usually "nobody has subbed this yet", which is worth
// saying plainly along with the ways out of it.
function NoRelease({
  message,
  animeId,
  rawAvailable,
  rawTitle,
  inPlayer,
  onRetry,
  onPlayRaw,
  onPick,
}: {
  message: string
  animeId: number
  rawAvailable?: boolean
  rawTitle?: string
  // Covering a player rather than standing in for one.
  inPlayer?: boolean
  onRetry: () => void
  onPlayRaw?: () => void
  onPick: () => void
}) {
  return (
    <div
      className={cx(
        'grid place-items-center p-6 text-center',
        inPlayer ? 'absolute inset-0 z-30 bg-black/90' : 'aspect-video',
      )}
    >
      <div className="max-w-lg">
        <p className="text-sm text-base-200">{message}</p>

        {rawAvailable && (
          <div className="mt-4 rounded-lg border border-base-800 bg-base-900/60 p-3 text-left">
            <p className="text-xs text-base-400">
              A raw broadcast is seeding now. It has no subtitles.
            </p>
            {rawTitle && (
              <p className="mt-1 line-clamp-2 font-mono text-[11px] text-base-500">{rawTitle}</p>
            )}
            <button
              onClick={onPlayRaw}
              className="mt-2 rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
            >
              Watch the raw anyway
            </button>
          </div>
        )}

        <div className="mt-4 flex flex-wrap items-center justify-center gap-2">
          <button
            onClick={onRetry}
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
          >
            Try again
          </button>
          <button
            onClick={onPick}
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
          >
            Choose a release
          </button>
          <Link
            to={`/anime/${animeId}`}
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
          >
            Other episodes
          </Link>
          <Link
            to="/settings"
            className="rounded-md px-3 py-1.5 text-sm text-base-400 hover:text-base-100"
          >
            Quality settings
          </Link>
        </div>
      </div>
    </div>
  )
}

/**
 * mpv plays in its own window and reports over IPC, so this page renders only a
 * way to stop it and a reminder of which player is in charge.
 */
function MpvPanel({ title, episode, player }: { title: string; episode: number; player: string }) {
  const stop = useMutation({ mutationFn: () => api.post('/api/stop') })

  return (
    <div className="grid aspect-video place-items-center bg-base-900 p-6 text-center">
      <div>
        <p className="text-sm font-medium text-base-100">Playing in {player === 'vlc' ? 'VLC' : 'mpv'}</p>
        <p className="mt-1 max-w-md truncate text-xs text-base-500">
          Episode {episode} · {title}
        </p>
        <div className="mt-4 flex items-center justify-center gap-2">
          <button
            onClick={() => stop.mutate()}
            disabled={stop.isPending}
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700 disabled:opacity-50"
          >
            {stop.isSuccess ? 'Stopped' : 'Stop'}
          </button>
          <Link
            to="/settings"
            className="rounded-md px-3 py-1.5 text-sm text-base-400 hover:text-base-100"
          >
            Use the browser instead
          </Link>
        </div>
      </div>
    </div>
  )
}

const UP_NEXT_SECONDS = 5

// Shown over the credits once an episode ends. With auto-next on it counts
// down and goes; otherwise it just offers the button.
function UpNext({
  episode,
  title,
  auto,
  onPlay,
  onCancel,
}: {
  episode: number
  title?: string
  auto: boolean
  onPlay: () => void
  onCancel: () => void
}) {
  const [left, setLeft] = useState(UP_NEXT_SECONDS)
  useEffect(() => {
    if (!auto) return
    if (left <= 0) {
      onPlay()
      return
    }
    const id = window.setTimeout(() => setLeft((n) => n - 1), 1000)
    return () => window.clearTimeout(id)
  }, [auto, left, onPlay])

  return (
    <div
      role="dialog"
      aria-label="Up next"
      className="absolute right-4 bottom-20 z-20 w-72 animate-rise rounded-xl border border-base-700 bg-base-950/90 p-3 shadow-xl shadow-black/60 backdrop-blur-sm"
    >
      <p className="text-[11px] font-medium tracking-wide text-base-400 uppercase">Up next</p>
      <p className="mt-0.5 truncate text-sm text-base-100">
        Episode {episode}
        {title ? ` · ${title}` : ''}
      </p>
      <div className="mt-2.5 flex items-center gap-2">
        <button
          onClick={onPlay}
          className="rounded-md bg-white px-3 py-1.5 text-sm font-semibold text-base-950 hover:bg-base-100"
        >
          {auto ? `Play now · ${left}` : 'Play next'}
        </button>
        <button
          onClick={onCancel}
          className="rounded-md px-3 py-1.5 text-sm text-base-300 hover:bg-base-800 hover:text-white"
        >
          {auto ? 'Cancel' : 'Dismiss'}
        </button>
      </div>
      {auto && (
        <div className="mt-2 h-0.5 overflow-hidden rounded-full bg-base-800">
          <div
            className="h-full bg-accent-500 transition-[width] duration-1000 ease-linear"
            style={{ width: `${((UP_NEXT_SECONDS - left) / UP_NEXT_SECONDS) * 100}%` }}
          />
        </div>
      )}
    </div>
  )
}

function Toggle({
  label,
  on,
  onChange,
}: {
  label: string
  on: boolean
  onChange: (value: boolean) => void
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      onClick={() => onChange(!on)}
      className={cx(
        'flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium transition-colors',
        on ? 'bg-accent-500/20 text-accent-400' : 'bg-base-850 text-base-400 hover:text-base-200',
      )}
    >
      <span
        className={cx(
          'relative h-3.5 w-6 rounded-full transition-colors',
          on ? 'bg-accent-500' : 'bg-base-700',
        )}
      >
        <span
          className={cx(
            'absolute top-0.5 size-2.5 rounded-full bg-white transition-all',
            on ? 'left-3' : 'left-0.5',
          )}
        />
      </span>
      {label}
    </button>
  )
}
