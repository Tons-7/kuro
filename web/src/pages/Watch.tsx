import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  api,
  ApiError,
  beacon,
  query,
  type PlaySession,
  type SkipRange,
  SOUND_PREFS,
  type SoundOptions,
  type StreamInfo,
  type VoiceLevel,
} from '../lib/api'
import { bytes, cx } from '../lib/format'
import { refreshDownloads, useDownloads, useEpisodes, usePrefs, useSetPref } from '../lib/queries'
import { Anime4KDialog } from '../components/Anime4KDialog'
import { CharacterRail } from '../components/CharacterRail'
import { EpisodeList, isUnaired } from '../components/EpisodeList'
import { ReleasePicker } from '../components/ReleasePicker'
import { StatusMenu } from '../components/StatusMenu'
import { Badge, Button, buttonClass, ErrorState, LinkButton, Segmented, Skeleton, Spinner, useDocumentTitle } from '../components/ui'

type AudioChoice = 'sub' | 'dub' | 'either'
import { Player } from '../player/Player'
import {
  autoTier,
  deviceUpscale,
  lowerTier,
  setAutoTier,
  setDeviceUpscale,
  upscaleHere,
  type AutoTier,
} from '../player/anime4k'

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

  // Write where the shown value came from, or a per-show override keeps winning and the switch looks stuck.
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

  // Prefs first, or the browser starts a stream mpv takes over; a raw is opted into per attempt only.
  const [rawFor, setRawFor] = useState('')
  const allowRaw = rawFor === epKey
  const setAllowRaw = (on: boolean) => setRawFor(on ? epKey : '')

  // Sub or dub is saved per show, and held here too so the switch doesn't wait on the save.
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

  // mpv opens the file itself and reports over its own IPC socket, so there is nothing here to transcode for.
  const source = playingInMpv ? undefined : play.data?.streamUrl
  // Read at open time, not part of the key: changing it mid-episode restarts the pass in place.
  const soundOptions = useMemo<SoundOptions>(
    () => ({
      voice: (effective['playback.voice'] || 'off') as VoiceLevel,
      night: flag('playback.night'),
      headphones: flag('playback.headphones'),
      mono: flag('playback.mono'),
      boost: (effective['playback.boost'] || 'off') as VoiceLevel,
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- effective and flag follow prefs.data
    [prefs.data],
  )
  const soundAtOpen = useRef(soundOptions)
  soundAtOpen.current = soundOptions
  // Stable, or the player's key handler is torn down and re-added on every progress tick.
  const setSoundMutate = setPref.mutate
  const soundProp = useMemo(
    () => ({
      options: soundOptions,
      onChange: (next: SoundOptions) => {
        for (const key of Object.keys(SOUND_PREFS) as (keyof SoundOptions)[]) {
          if (next[key] !== soundOptions[key]) setSoundMutate({ key: SOUND_PREFS[key], value: String(next[key]) })
        }
      },
    }),
    [soundOptions, setSoundMutate],
  )
  const stream = useQuery({
    enabled: !!source,
    queryKey: ['stream', id, ep, source, wantAudio],
    // Aborted when superseded: an abandoned open would otherwise pin and hold its episode when it lands.
    queryFn: ({ signal }) =>
      api.post<StreamInfo>(
        `/api/stream/open${query({ id, episode: ep, source, audio: wantAudio, ...soundAtOpen.current })}`,
        undefined,
        signal,
      ),
    retry: 1,
    staleTime: Infinity,
    // Never carried across a mount: leaving the page closes the session, so a
    // cached answer would describe a playlist that is no longer there.
    gcTime: 0,
  })

  // From pressing play to the stream opening: the search, then the release's first data, then its start.
  const startingNote = useStartingNote(id, ep, play.isPending || (!!source && stream.isPending))

  // AniSkip needs the stream's real length, remembered since an audio switch briefly clears it.
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
    onSuccess: () => refreshDownloads(qc),
  })
  // The page no longer remounts, so "Queued" would follow you to the next one.
  const resetDownload = download.reset
  useEffect(() => resetDownload(), [id, ep, resetDownload])

  // An episode already on disk isn't offered for download.
  const downloads = useDownloads()
  // A release bigger than the cache budget is removed once nothing plays it; said now, with the way out.
  const cache = useQuery({
    enabled: play.data?.source === 'torrent',
    queryKey: ['cache'],
    queryFn: () => api.get<{ budget: number }>('/api/cache'),
    staleTime: 60_000,
  })
  const playing = (downloads.data?.items ?? []).find((d) => !!play.data?.infoHash && d.infoHash === play.data.infoHash)
  const overBudget = !!playing && !playing.kept && !!cache.data?.budget && playing.totalBytes > cache.data.budget
  const keep = useMutation({
    meta: { inline: true },
    mutationFn: () => api.post(`/api/downloads/${playing!.infoHash}/keep`),
    onSuccess: () => refreshDownloads(qc),
  })
  // A season pack lists every episode it serves.
  const held = (downloads.data?.items ?? []).some(
    (d) => d.animeId === id && d.episodes.includes(String(ep)) && d.percent >= 100,
  )

  const detail = useQuery({
    enabled: id !== 0,
    queryKey: ['anime', id],
    queryFn: () =>
      api.get<{ title: string; listStatus?: string; coverImage?: { large?: string; extraLarge?: string }; cover?: string }>(
        `/api/anime/${id}`,
      ),
    staleTime: 5 * 60_000,
  })

  const list = episodes.data?.items ?? []
  const current = list.find((e) => e.number === ep)
  // The next episode worth playing, as store.NextEpisode picks it (filler rule, nothing unaired).
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
    (position: number, duration: number, played: number, final: boolean) => {
      const body = { animeId: id, episode: ep, position, duration, played }
      // sendBeacon survives the tab closing, which a fetch does not.
      if (!final) return beacon('/api/progress', body)
      // The last report of an episode (its end, Next, leaving) refreshes the ticks once the server has it.
      void api
        .post<{ watched: boolean }>('/api/progress', body)
        .then((r) => {
          if (!r.watched) return
          void qc.invalidateQueries({ queryKey: ['episodes', id] })
          void qc.invalidateQueries({ queryKey: ['anime', id] })
        })
        .catch(() => {})
    },
    [id, ep, qc],
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

  // Which episode ended, not just that one did: a plain flag survives navigation and skips an episode.
  const [endedKey, setEndedKey] = useState<string | null>(null)
  const ended = endedKey === `${id}-${ep}`
  const onEnded = useCallback(() => setEndedKey(`${id}-${ep}`), [id, ep])
  const clearEnded = useCallback(() => setEndedKey(null), [])

  // A reaped session: /api/play revives the torrent and the reopen rebuilds it; throws so the player shows it.
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

  // The saved default is shared with mpv on the host; each browser decides whether it applies here.
  const [deviceChoice, setDeviceChoice] = useState(deviceUpscale)
  const [settled, setSettled] = useState(autoTier)
  const [steppedTo, setSteppedTo] = useState<AutoTier | null>(null)
  const here = upscaleHere(deviceChoice, flag('playback.anime4k'), settled)
  // Under Auto a GPU that falls behind drops a tier, and the device remembers where it settled.
  const stepDown = () => {
    const next = lowerTier(here.tier)
    setAutoTier(next)
    setSettled(next)
    setSteppedTo(next)
  }
  const upscaling = {
    enabled: upscale?.enabled ?? here.enabled,
    mode: upscale?.mode ?? effective['playback.anime4k_mode'] ?? 'A',
    tier: here.tier,
    // Not for a tier picked by hand, nor an episode switched on by hand.
    onSlow: here.auto && !upscale ? stepDown : undefined,
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
            {/* No overflow-hidden: clipping a composited video blacks it out. */}
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
                <Searching note={startingNote} />
              ) : playingInMpv ? (
                <MpvPanel title={play.data?.title ?? showTitle} animeId={id} episode={ep} player={play.data?.player ?? 'mpv'} />
              ) : (
                <Player
                  stream={stream.data}
                  starting={startingNote}
                  startAt={play.data?.startAt ?? 0}
                  skips={skips.data?.ranges ?? []}
                  autoSkip={autoSkip}
                  autoPlay={flag('playback.autoplay')}
                  subLanguages={subLanguages}
                  upscale={upscaling}
                  sound={soundProp}
                  title={showTitle}
                  subtitle={episodeLabel}
                  artwork={detail.data?.coverImage?.large ?? detail.data?.coverImage?.extraLarge ?? detail.data?.cover}
                  onProgress={reportProgress}
                  onEnded={onEnded}
                  onNext={hasNext ? goNext : undefined}
                  // Back into the finished episode: the countdown must not take the viewer away mid-rewatch.
                  onResume={clearEnded}
                  onSessionLost={recoverSession}
                  // Inside the player, or fullscreen hides it; failures too, unless the player can recover.
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
                        needsTranscoder={
                          stream.error instanceof ApiError &&
                          (stream.error.body as { code?: string } | undefined)?.code === 'no-transcoder'
                        }
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

              {overBudget && (
                <div
                  role="status"
                  className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2.5 text-sm text-amber-100"
                >
                  <p>
                    This release is {bytes(playing.totalBytes)}, bigger than the {bytes(cache.data!.budget)} cache: it is
                    removed once you stop watching unless you keep it.
                  </p>
                  <Button variant="primary" onClick={() => keep.mutate()} disabled={keep.isPending}>
                    Keep this episode
                  </Button>
                  {keep.isError && <p className="w-full text-xs text-recap">{(keep.error as Error).message}</p>}
                </div>
              )}

              {/* Until settings load every switch reads off, and a click would write wrong. */}
              {!prefs.data ? (
                <Skeleton className="h-12 w-full rounded-lg" />
              ) : (
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
                  {/* Switching resolves another release, as most carry one language. */}
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
                  {/* A toggle flipped here becomes the new default. */}
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
              )}

              {steppedTo && (
                <p className="text-xs text-base-400">
                  {steppedTo === 'off'
                    ? 'Anime4K turned itself off: this device was falling behind.'
                    : `Anime4K dropped to ${steppedTo}: this device was falling behind.`}
                </p>
              )}

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

              {/* The show's cast, not this episode's (MAL has none), as the heading says. */}
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
        device={deviceChoice}
        onDevice={(choice) => {
          setDeviceUpscale(choice)
          setDeviceChoice(choice)
          // Choosing Auto again starts it from the top.
          if (choice === 'auto') {
            setAutoTier(null)
            setSettled(null)
          }
          setSteppedTo(null)
          // The device's default just changed; this episode follows it.
          setUpscale(null)
        }}
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

/** What the episode is, under the player, beyond a release filename. */
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

/** What a starting episode is waiting on, as the server reports it. */
interface PlayStage {
  step?: 'search' | 'files' | 'data' | 'start'
  release?: string
  peers?: number
  bytes?: number
}

/** Starting is search, file list, first data, file start; the wait says which step and for how long. */
function useStartingNote(animeId: number, episode: number, waiting: boolean): { title: string; detail: string } {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    if (!waiting) return
    setSeconds(0)
    const id = window.setInterval(() => setSeconds((s) => s + 1), 1000)
    return () => window.clearInterval(id)
  }, [waiting, animeId, episode])

  const stage = useQuery({
    enabled: waiting,
    queryKey: ['play-stage', animeId, episode],
    queryFn: () => api.get<PlayStage>(`/api/play/stage${query({ id: animeId, episode })}`),
    refetchInterval: 1000,
    gcTime: 0,
  }).data

  const clock = seconds >= 20 ? ` · ${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}` : ''
  switch (stage?.step) {
    case 'files':
      return { title: 'Getting the file list…', detail: `${stage.release ?? 'From the people sharing it'}${clock}` }
    case 'data': {
      const swarm = stage.peers
        ? `${stage.peers} ${stage.peers === 1 ? 'peer' : 'peers'}, ${bytes(stage.bytes ?? 0)} so far`
        : 'no peers connected yet'
      return { title: 'Waiting for the first data…', detail: `${stage.release ? `${stage.release} · ` : ''}${swarm}${clock}` }
    }
    case 'start':
      return {
        title: 'Reading the start of the file…',
        detail: `A large release on a slow connection can take a few minutes${clock}`,
      }
    default:
      return {
        title: 'Searching for a release…',
        detail: `${seconds < 6 ? 'Checking the release sites under each of the show’s titles' : 'Still looking; searches are rate limited'}${clock}`,
      }
  }
}

function Searching({ note }: { note: { title: string; detail: string } }) {
  return (
    <div className="grid aspect-video place-items-center">
      <div className="max-w-md px-4 text-center">
        <Spinner className="mx-auto size-8" />
        <p className="mt-3 text-sm text-base-300">{note.title}</p>
        <p className="mt-1 text-xs break-words text-base-500 tabular-nums">{note.detail}</p>
      </div>
    </div>
  )
}

// Usually "nobody has subbed this yet", said plainly with the ways out.
function NoRelease({
  message,
  animeId,
  rawAvailable,
  rawTitle,
  inPlayer,
  needsTranscoder,
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
  // Retrying can't help; offer Setup instead.
  needsTranscoder?: boolean
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
              className={cx(buttonClass(), 'mt-2')}
            >
              Watch the raw anyway
            </button>
          </div>
        )}

        {needsTranscoder ? (
          <div className="mt-4 flex flex-wrap items-center justify-center gap-2">
            <LinkButton to="/setup" variant="primary">Install the transcoder</LinkButton>
            <LinkButton to="/settings?tab=Playback" variant="ghost">Player settings</LinkButton>
          </div>
        ) : (
        <div className="mt-4 flex flex-wrap items-center justify-center gap-2">
          <Button onClick={onRetry}>Try again</Button>
          <Button onClick={onPick}>Choose a release</Button>
          <LinkButton to={`/anime/${animeId}`}>Other episodes</LinkButton>
          <LinkButton to="/settings?tab=Quality" variant="ghost">Quality settings</LinkButton>
        </div>
        )}
      </div>
    </div>
  )
}

/** mpv plays in its own window, so this page only offers Stop and says which player is in charge. */
function MpvPanel({ title, animeId, episode, player }: { title: string; animeId: number; episode: number; player: string }) {
  // The episode too, so the server also stops fetching what nobody is watching now.
  const stop = useMutation({ mutationFn: () => api.post('/api/stop', { animeId, episode }) })

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
            className={buttonClass()}
          >
            {stop.isSuccess ? 'Stopped' : 'Stop'}
          </button>
          <Link
            to="/settings?tab=Playback"
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

// Over the credits at the end: counts down with auto-next on, else offers the button.
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
