import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  api,
  type AudioTrack,
  type SkipRange,
  type StreamHealth,
  type StreamInfo,
  type SubtitleTrack,
  DEFAULT_SOUND,
  sameSound,
  soundFrom,
  type SoundOptions,
  VOICE_LEVELS,
  type VoiceLevel,
} from '../lib/api'
import { clockTime, cx, languageName } from '../lib/format'
import { refreshDownloads } from '../lib/queries'
import { SHOW_SHORTCUTS } from '../components/keys'
import { PlayIcon } from '../components/PosterCard'
import { buttonClass, Spinner, useDismiss } from '../components/ui'
import { useAnime4K, type UpscaleTier } from './anime4k'
import {
  skipKind,
  untilWatching,
  useAutoSkip,
  useDocumentPiP,
  useEmbeddedFonts,
  useFreshFrames,
  useHlsSource,
  useKeepAlive,
  useSeekRecovery,
  useStallWatchdog,
  useSubtitles,
  useThumbnails,
  type Sheet,
} from './hooks'

// An iPhone has no element fullscreen, only the video's own.
function toggleFullscreen(shell: HTMLElement | null, video: HTMLVideoElement | null) {
  if (document.fullscreenElement) {
    void document.exitFullscreen().catch(() => {})
  } else if (shell?.requestFullscreen) {
    void shell.requestFullscreen().catch(() => {})
  } else {
    ;(video as (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null)?.webkitEnterFullscreen?.()
  }
}

// A source swap loads the element paused with no pause event; what was playing resumes once it can.
function resumeAfterRebuild(video: HTMLVideoElement | null, wasPlaying: boolean) {
  if (!video || !wasPlaying) return
  video.addEventListener('canplay', () => void video.play().catch(() => {}), { once: true })
}

// The settings' ISO 639-1 codes, and what a file's track tags say instead.
const LANGUAGE_TAGS: Record<string, string[]> = {
  en: ['en', 'eng', 'english'],
  es: ['es', 'spa', 'spanish', 'es-419', 'es-es'],
  pt: ['pt', 'por', 'portuguese', 'pt-br'],
  fr: ['fr', 'fre', 'fra', 'french'],
  de: ['de', 'ger', 'deu', 'german'],
  it: ['it', 'ita', 'italian'],
  ru: ['ru', 'rus', 'russian'],
  ar: ['ar', 'ara', 'arabic'],
  zh: ['zh', 'chi', 'zho', 'chinese'],
  ko: ['ko', 'kor', 'korean'],
  id: ['id', 'ind', 'indonesian'],
  vi: ['vi', 'vie', 'vietnamese'],
  th: ['th', 'tha', 'thai'],
}
// A signs-and-songs track by default looks like broken subtitles, not the wrong track.
const SIGNS = /\b(signs?|songs?|s&s|forced)\b/i

/** The track to show: first preferred language, dialogue over signs; the file's default flag comes last. */
export function chooseTrack(tracks: SubtitleTrack[], languages: string[] = ['en']): SubtitleTrack {
  const dialogue = (t: SubtitleTrack) => !SIGNS.test(t.title ?? '')
  const speaks = (code: string) => (t: SubtitleTrack) =>
    (LANGUAGE_TAGS[code] ?? [code]).includes((t.language ?? '').toLowerCase())

  for (const code of languages) {
    const is = speaks(code)
    const found =
      tracks.find((t) => is(t) && dialogue(t) && t.default) ??
      tracks.find((t) => is(t) && dialogue(t)) ??
      tracks.find((t) => is(t))
    if (found) return found
  }
  return (
    tracks.find((t) => dialogue(t) && t.default) ??
    tracks.find((t) => dialogue(t)) ??
    tracks.find((t) => t.default) ??
    tracks[0]
  )
}

const SPEEDS = [0.75, 1, 1.25, 1.5, 2]

// Volume, mute, speed and subtitle size carry over; the element alone starts at full volume.
const PERSIST_KEY = 'kuro.player'
interface Persisted {
  volume?: number
  muted?: boolean
  rate?: number
  subScale?: number
}

const VOLUME_NOTICE = 'volume'
const SUB_SCALES = [0.75, 0.9, 1, 1.15, 1.3, 1.5]
const DELAY_STEP = 0.1

function delayLabel(d: number): string {
  return d === 0 ? '0.0 s' : `${d > 0 ? '+' : ''}${d.toFixed(1)} s`
}
function loadPersisted(): Persisted {
  try {
    return JSON.parse(localStorage.getItem(PERSIST_KEY) ?? '{}') as Persisted
  } catch {
    return {}
  }
}
function savePersisted(patch: Persisted) {
  try {
    localStorage.setItem(PERSIST_KEY, JSON.stringify({ ...loadPersisted(), ...patch }))
  } catch {
    // Storage may be unavailable; the defaults are fine.
  }
}

export interface PlayerProps {
  stream?: StreamInfo
  /** What the episode is waiting on before its stream opens. */
  starting?: { title: string; detail: string }
  startAt: number
  skips: SkipRange[]
  autoSkip: { op: boolean; ed: boolean }
  autoPlay: boolean
  /** Subtitle languages in preference order, from settings. */
  subLanguages?: string[]
  /** onSlow is set only under Auto: a tier picked by hand is left alone. */
  upscale?: { enabled: boolean; mode: string; tier?: UpscaleTier; onSlow?: () => void }
  /** The sound settings, and where a change made here is remembered. */
  sound?: { options: SoundOptions; onChange: (next: SoundOptions) => void }
  title: string
  subtitle?: string
  /** The show's cover, for the system's media controls. */
  artwork?: string
  /** played is media time that actually played since the last report. */
  /** final: the episode's last report (its end, Next, leaving the page), worth waiting on. */
  onProgress: (position: number, duration: number, played: number, final: boolean) => void
  onEnded: () => void
  onNext?: () => void
  /** Playing or seeking again, which after the end means the viewer went back. */
  onResume?: () => void
  /** The server no longer knows the stream; reopen it under the same URLs. */
  onSessionLost?: () => Promise<unknown>
  /** Drawn inside the player, so it survives fullscreen and picture in picture. */
  overlay?: ReactNode
}

export function Player({
  stream,
  starting,
  startAt,
  skips,
  autoSkip,
  autoPlay,
  subLanguages,
  upscale,
  sound,
  title,
  subtitle,
  artwork,
  onProgress,
  onEnded,
  onNext,
  onResume,
  onSessionLost,
  overlay,
}: PlayerProps) {
  const [video, setVideo] = useState<HTMLVideoElement | null>(null)
  const [canvas, setCanvas] = useState<HTMLCanvasElement | null>(null)
  const qc = useQueryClient()
  const shell = useRef<HTMLDivElement>(null)
  // The player renders into this through a portal, so picture in picture can
  // move it to the floating window with its event handlers still attached.
  const [host] = useState(() => document.createElement('div'))
  const anchor = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    anchor.current?.append(host)
    return () => host.remove()
  }, [host])

  const [playing, setPlaying] = useState(false)
  const [waiting, setWaiting] = useState(true)
  const [time, setTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)
  const [rate, setRate] = useState(() => loadPersisted().rate ?? 1)
  const [controlsVisible, setControlsVisible] = useState(true)
  // A key changed something with nothing else on screen; say where it landed.
  const [notice, setNotice] = useState<string | null>(null)
  const noticeTimer = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(noticeTimer.current), [])
  const showNotice = useCallback((what: string) => {
    setNotice(what)
    window.clearTimeout(noticeTimer.current)
    noticeTimer.current = window.setTimeout(() => setNotice(null), 900)
  }, [])
  const showVolume = useCallback(() => showNotice(VOLUME_NOTICE), [showNotice])
  const [track, setTrack] = useState<number | null>(null)

  const [subScale, setSubScale] = useState(() => loadPersisted().subScale ?? 1)
  const changeSubScale = useCallback((s: number) => {
    setSubScale(s)
    savePersisted({ subScale: s })
  }, [])
  // Per episode: a delay fixes one file's timing.
  const [subDelay, setSubDelay] = useState(0)
  useEffect(() => setSubDelay(0), [stream?.id])
  const subDelayRef = useRef(subDelay)
  subDelayRef.current = subDelay
  const shiftSubDelay = useCallback(
    (by: number) => {
      const next = Math.round((subDelayRef.current + by) * 10) / 10
      setSubDelay(next)
      showNotice(`Subtitle delay ${delayLabel(next)}`)
    },
    [showNotice],
  )

  // Restore the last episode's settings; a new source resets the rate, so it's reapplied each load.
  useEffect(() => {
    if (!video) return
    const saved = loadPersisted()
    if (typeof saved.volume === 'number') video.volume = Math.min(1, Math.max(0, saved.volume))
    if (typeof saved.muted === 'boolean') video.muted = saved.muted
    const apply = () => {
      if (video.playbackRate !== rate) video.playbackRate = rate
    }
    apply()
    for (const ev of ['loadstart', 'loadedmetadata', 'canplay']) video.addEventListener(ev, apply)
    return () => {
      for (const ev of ['loadstart', 'loadedmetadata', 'canplay']) video.removeEventListener(ev, apply)
    }
  }, [video, rate])

  const changeRate = useCallback((r: number) => {
    setRate(r)
    savePersisted({ rate: r })
  }, [])

  // After an audio switch or recovery, the epoch forces a fresh HLS instance so old segments aren't mixed in.
  const [audioTrack, setAudioTrack] = useState(0)
  const [soundState, setSoundState] = useState<SoundOptions>(sound?.options ?? DEFAULT_SOUND)
  const [epoch, setEpoch] = useState(0)
  const [resumeAt, setResumeAt] = useState(startAt)
  // The session could not be reopened after the reaper took it.
  const [lostError, setLostError] = useState<string | null>(null)

  // Reset during render: the HLS effect runs first and would attach at the last episode's position.
  const [attached, setAttached] = useState(stream?.id)
  if (stream?.id !== attached) {
    setAttached(stream?.id)
    setAudioTrack(stream?.audioTrack ?? 0)
    setSoundState(stream?.sound ? soundFrom(stream.sound) : (sound?.options ?? DEFAULT_SOUND))
    setEpoch(0)
    setResumeAt(startAt)
    // Or the scrubber reads the last episode's length and a skip button for its ranges sits over the new one.
    setTime(0)
    setDuration(0)
    // Or a new episode sits on a paused frame while it buffers.
    setWaiting(true)
    setLostError(null)
  }

  const playlist = stream?.playlist
    ? `${stream.playlist}${epoch ? `?a=${epoch}` : ''}`
    : undefined

  // Set on any pick, even track 0, or a rebuilt session's sub/dub pick undoes it.
  const audioChosen = useRef(false)
  const switchAudio = useCallback(
    (index: number) => {
      const id = stream?.id
      if (!id || index === audioTrack) return
      audioChosen.current = true
      // Loading the new source leaves the element paused without a pause event.
      resumeAfterRebuild(video, !!video && !video.paused && !video.ended)
      setResumeAt(video?.currentTime ?? startAt)
      setAudioTrack(index)
      setWaiting(true)
      setEpoch((e) => e + 1)
      void api.post(`/api/stream/${id}/audio?track=${index}`).catch(() => {})
    },
    [stream?.id, audioTrack, video, startAt],
  )
  useEffect(() => {
    audioChosen.current = false
  }, [stream?.id])

  // Same shape as a track switch: the pass restarts where the viewer is, and the change is remembered.
  const switchSound = useCallback(
    (patch: Partial<SoundOptions>) => {
      const id = stream?.id
      const next = { ...soundState, ...patch }
      if (!id || sameSound(next, soundState)) return
      resumeAfterRebuild(video, !!video && !video.paused && !video.ended)
      setResumeAt(video?.currentTime ?? startAt)
      setSoundState(next)
      setWaiting(true)
      setEpoch((e) => e + 1)
      void api.post(`/api/stream/${id}/sound${soundQuery(patch)}`).catch(() => {})
      sound?.onChange(next)
    },
    [stream?.id, soundState, video, startAt, sound],
  )
  const cycleVoice = useCallback(() => {
    switchSound({ voice: VOICE_LEVELS[(VOICE_LEVELS.indexOf(soundState.voice) + 1) % VOICE_LEVELS.length] })
  }, [switchSound, soundState.voice])

  // Filled below once hls exists; keepalive and fatal 404 can notice together, so one reopen at a time.
  const recoverRef = useRef<() => void>(() => {})
  const recovering = useRef(false)
  const recover = useCallback(() => recoverRef.current(), [])

  const { error, hls } = useHlsSource(video, playlist, resumeAt, recover)
  useKeepAlive(video, playlist, recover)
  recoverRef.current = () => {
    if (recovering.current || !onSessionLost) return
    recovering.current = true
    const wasPlaying = !!video && !video.paused && !video.ended
    const track = audioTrack
    const snd = soundState
    void onSessionLost()
      .then(() => {
        setLostError(null)
        // The rebuilt session picks the track from the sub/dub preference, so a
        // mid-episode switch has to be asked for again or the menu lies.
        if (audioChosen.current && stream?.id) {
          void api.post(`/api/stream/${stream.id}/audio?track=${track}`).catch(() => {})
        }
        if (!sameSound(snd, DEFAULT_SOUND) && stream?.id) {
          void api.post(`/api/stream/${stream.id}/sound${soundQuery(snd)}`).catch(() => {})
        }
        setResumeAt(video?.currentTime ?? startAt)
        setWaiting(true)
        setEpoch((e) => e + 1)
        // The rebuild resets the element to paused; only autoplay restarts it.
        resumeAfterRebuild(video, wasPlaying)
      })
      // Rebuilding against a session that failed to reopen only loops.
      .catch((err: unknown) => setLostError(err instanceof Error ? err.message : 'Playback stopped'))
      .finally(() => {
        recovering.current = false
      })
  }
  useSeekRecovery(video, hls)
  useStallWatchdog(video, hls)
  const fonts = useEmbeddedFonts(stream?.fontsUrl, epoch)
  const pip = useDocumentPiP(host)
  // Picture in picture moves the player to another document, so the subtitle canvas is rebuilt there.
  const subLook = useMemo(() => ({ scale: subScale, delay: subDelay }), [subScale, subDelay])
  useSubtitles(video, stream, track, fonts, pip.active, subLook)
  useAutoSkip(video, skips, autoSkip)

  const upscaleState = useAnime4K({
    video,
    canvas,
    // Not while floating: the upscaler's canvas is bound to the document it was configured in.
    enabled: !!upscale?.enabled && !pip.active,
    mode: upscale?.mode ?? 'A',
    tier: upscale?.tier ?? 'full',
    onSlow: upscale?.onSlow,
    // The playlist, not the session id, which stays when another release is picked.
    source: playlist,
  })
  // Only worth showing while it holds the current frame; seeking or a stopped
  // renderer leaves it stale, and the plain video underneath should show.
  const painted = useFreshFrames(video, upscaleState === 'on')
  const showUpscaled = upscaleState === 'on' && painted

  // Keyed on the tracks, not the session id, which a recovery and a hand-picked release both keep.
  const trackList = (stream?.subtitles ?? [])
    .map((s) => `${s.index}:${s.language ?? ''}:${s.title ?? ''}`)
    .join('|')
  useEffect(() => {
    setTrack(stream?.subtitles?.length ? chooseTrack(stream.subtitles, subLanguages).index : null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trackList])

  // The reused element's autoplay fires once; a ref, so flipping it mid-episode can't undo a pause.
  const autoPlayRef = useRef(autoPlay)
  autoPlayRef.current = autoPlay
  useEffect(() => {
    if (!video || !stream?.id || !autoPlayRef.current) return
    const start = () => void video.play().catch(() => {})
    // Straight away; waiting on canplay left the next episode still for seconds.
    start()
    video.addEventListener('canplay', start, { once: true })
    return () => video.removeEventListener('canplay', start)
  }, [video, stream?.id])

  const reportRef = useRef(onProgress)
  reportRef.current = onProgress
  const endedRef = useRef(onEnded)
  endedRef.current = onEnded

  // Reports on a timer with the time really played (a big step is a seek); per stream, keeping the last
  // known position, since the source is torn down by the time Next or leaving sends it.
  useEffect(() => {
    if (!video || !stream?.id) return
    const reporter = reportRef.current

    let played = 0
    let last = video.currentTime
    let position = video.currentTime
    let duration = video.duration
    const onTime = () => {
      const now = video.currentTime
      const step = now - last
      if (step > 0 && step < 2) played += step
      last = now
      if (video.duration > 0) {
        position = now
        duration = video.duration
      }
    }
    const onSeeking = () => {
      last = video.currentTime
    }

    const report = (at: number, final = false) => {
      reporter(at, duration, played, final)
      played = 0
    }
    const id = window.setInterval(() => {
      if (!video.paused && duration > 0) report(position)
    }, 10_000)

    const flush = () => {
      if (duration > 0) report(position)
    }
    const ended = () => {
      if (duration > 0) report(duration, true)
      endedRef.current()
    }
    video.addEventListener('timeupdate', onTime)
    video.addEventListener('seeking', onSeeking)
    video.addEventListener('pause', flush)
    video.addEventListener('ended', ended)
    window.addEventListener('pagehide', flush)
    return () => {
      window.clearInterval(id)
      video.removeEventListener('timeupdate', onTime)
      video.removeEventListener('seeking', onSeeking)
      video.removeEventListener('pause', flush)
      video.removeEventListener('ended', ended)
      window.removeEventListener('pagehide', flush)
      if (duration > 0) report(position, true)
    }
  }, [video, stream?.id])

  const togglePlay = useCallback(() => {
    if (!video) return
    if (video.paused) void video.play().catch(() => {})
    else video.pause()
  }, [video])

  const seekBy = useCallback(
    (delta: number) => {
      if (!video) return
      video.currentTime = Math.max(0, Math.min(video.duration || 0, video.currentTime + delta))
    },
    [video],
  )

  // The system's media controls (media keys, a headset button, the lock screen) show the episode and drive it.
  const onNextRef = useRef(onNext)
  onNextRef.current = onNext
  const hasNext = !!onNext
  useEffect(() => {
    if (!video || !('mediaSession' in navigator)) return
    const session = navigator.mediaSession
    session.metadata = new MediaMetadata({
      title: subtitle ?? title,
      artist: subtitle ? title : '',
      artwork: artwork ? [{ src: artwork }] : [],
    })
    const handlers: Array<[MediaSessionAction, MediaSessionActionHandler | null]> = [
      ['play', () => void video.play().catch(() => {})],
      ['pause', () => video.pause()],
      ['seekbackward', (d) => seekBy(-(d.seekOffset ?? 10))],
      ['seekforward', (d) => seekBy(d.seekOffset ?? 10)],
      ['seekto', (d) => d.seekTime !== undefined && (video.currentTime = d.seekTime)],
      ['nexttrack', hasNext ? () => onNextRef.current?.() : null],
    ]
    for (const [action, handler] of handlers) {
      try {
        session.setActionHandler(action, handler)
      } catch {
        // An action this browser doesn't know.
      }
    }
    return () => {
      for (const [action] of handlers) {
        try {
          session.setActionHandler(action, null)
        } catch {
          // As above.
        }
      }
      session.metadata = null
    }
  }, [video, title, subtitle, artwork, seekBy, hasNext])

  // The lock screen's scrubber follows the episode; it can't place itself on a stream it never saw load.
  useEffect(() => {
    if (!video || !('mediaSession' in navigator) || !navigator.mediaSession.setPositionState) return
    const update = () => {
      const d = video.duration
      if (!Number.isFinite(d) || d <= 0) return
      try {
        navigator.mediaSession.setPositionState({ duration: d, position: Math.min(video.currentTime, d), playbackRate: video.playbackRate || 1 })
      } catch {
        // A position past a duration still settling.
      }
    }
    const events = ['loadedmetadata', 'durationchange', 'seeked', 'ratechange', 'play', 'pause']
    for (const e of events) video.addEventListener(e, update)
    return () => {
      for (const e of events) video.removeEventListener(e, update)
    }
  }, [video])

  useEffect(() => {
    if ('mediaSession' in navigator) navigator.mediaSession.playbackState = playing ? 'playing' : 'paused'
  }, [playing])

  // The screen stays on while an episode plays, muted too, which the browser's own rule leaves to dim.
  useEffect(() => {
    if (!playing || !('wakeLock' in navigator)) return
    let lock: WakeLockSentinel | null = null
    let gone = false
    const take = () => {
      if (document.visibilityState !== 'visible' || (lock && !lock.released)) return
      navigator.wakeLock.request('screen').then(
        (l) => (gone ? void l.release() : (lock = l)),
        () => {
          // Refused: battery saver, or a page not over HTTPS.
        },
      )
    }
    take()
    // A hidden page loses its lock; coming back takes it again.
    document.addEventListener('visibilitychange', take)
    return () => {
      gone = true
      document.removeEventListener('visibilitychange', take)
      void lock?.release()
    }
  }, [playing])

  // Only a slow seek earns a spinner; otherwise every arrow press flashes one.
  const seekTimer = useRef<number>(0)
  const markSeeking = useCallback((active: boolean) => {
    window.clearTimeout(seekTimer.current)
    if (!active) {
      setWaiting(false)
      return
    }
    seekTimer.current = window.setTimeout(() => setWaiting(true), 150)
  }, [])
  useEffect(() => () => window.clearTimeout(seekTimer.current), [])

  // On touch, a double tap on either third of the picture seeks ten seconds
  // that way; a single tap still toggles play. Mouse clicks are unchanged.
  const lastTap = useRef<{ at: number; x: number }>({ at: 0, x: 0 })
  const touchedAt = useRef(0)
  const [flash, setFlash] = useState<'back' | 'forward' | null>(null)
  // A tap on a side third waits for a second, or every double-tap seek pauses.
  const pendingTap = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(pendingTap.current), [])
  const onSurfaceTouch = useCallback(
    (e: React.PointerEvent<HTMLElement>) => {
      if (e.pointerType !== 'touch') return
      const now = performance.now()
      touchedAt.current = now
      const rect = e.currentTarget.getBoundingClientRect()
      const x = (e.clientX - rect.left) / rect.width
      const side = x < 0.35 || x > 0.65
      const prev = lastTap.current
      lastTap.current = { at: now, x }
      if (now - prev.at < 300 && Math.abs(prev.x - x) < 0.2 && side) {
        window.clearTimeout(pendingTap.current)
        const dir = x < 0.35 ? 'back' : 'forward'
        seekBy(dir === 'back' ? -10 : 10)
        setFlash(dir)
        window.setTimeout(() => setFlash(null), 500)
        lastTap.current = { at: 0, x }
        return
      }
      if (!side) {
        togglePlay()
        return
      }
      window.clearTimeout(pendingTap.current)
      pendingTap.current = window.setTimeout(togglePlay, 300)
    },
    [togglePlay, seekBy],
  )
  // The click that follows a touch is the same gesture, already handled.
  const onSurfaceClick = useCallback(() => {
    if (performance.now() - touchedAt.current < 700) return
    togglePlay()
  }, [togglePlay])

  // Hide the controls while playing, but never while paused: a paused player with no controls looks broken.
  const hideTimer = useRef<number | undefined>(undefined)
  const nudge = useCallback(() => {
    setControlsVisible(true)
    window.clearTimeout(hideTimer.current)
    hideTimer.current = window.setTimeout(() => {
      if (!video?.paused) setControlsVisible(false)
    }, 2600)
  }, [video])

  const activeSkip = skips.find((r) => time >= r.start && time < r.end)

  useKeyboard({
    video,
    togglePlay,
    seekBy,
    nudge,
    shell: shell.current,
    extra: pip.window,
    showVolume,
    shiftSubDelay: track === null ? undefined : shiftSubDelay,
    skipTo: activeSkip?.end,
    // Only where the control shows: a file with no audio has nothing to lift.
    cycleVoice: (stream?.audio?.length ?? 0) > 0 ? cycleVoice : undefined,
  })

  // How long the picture has been stuck, so a slow swarm can say so rather than looking like a hang.
  const [stalledFor, setStalledFor] = useState(0)
  // Carried over, it declares the next episode too slow before it has loaded.
  useEffect(() => setStalledFor(0), [stream?.id])

  // Numbers for the stall message: a swarm too slow for the bitrate is a download, not a wait.
  const [health, setHealth] = useState<StreamHealth | null>(null)
  const [queued, setQueued] = useState(false)
  useEffect(() => setQueued(false), [stream?.id])
  const stalledLong = stalledFor > 12
  useEffect(() => {
    const id = stream?.id
    if (!id || !stalledLong) {
      setHealth(null)
      return
    }
    let stop = false
    const poll = async () => {
      if (video?.paused || document.visibilityState !== 'visible') return
      return api
        .get<StreamHealth>(`/api/stream/${id}/health`)
        .then((h) => {
          if (!stop) setHealth(h)
        })
        .catch(() => {})
    }
    void poll()
    const timer = window.setInterval(() => void poll(), 5000)
    return () => {
      stop = true
      window.clearInterval(timer)
    }
  }, [stream?.id, stalledLong, video])

  const downloadInstead = async () => {
    const [animeId, episode] = (stream?.id ?? '').split('-').map(Number)
    if (!animeId || !episode) return
    try {
      await api.post('/api/download', { animeId, episode })
      refreshDownloads(qc)
      setQueued(true)
      setQueueError(null)
    } catch (err) {
      setQueueError(err instanceof Error ? err.message : 'Could not queue it')
    }
  }
  const [queueError, setQueueError] = useState<string | null>(null)
  useEffect(() => {
    if (!waiting && stream) {
      setStalledFor(0)
      return
    }
    // Paused is not stuck: counting on would start the health poll, and that keeps the session alive.
    const id = window.setInterval(() => setStalledFor((s) => (stream && video?.paused ? s : s + 1)), 1000)
    return () => window.clearInterval(id)
  }, [waiting, stream, video])

  // Floating, the player fills its window; fullscreen is CSS, since `:fullscreen` never matches there.
  const corners = pip.active ? '' : 'sm:rounded-xl'

  const body = (
    <div
      ref={shell}
      onMouseMove={nudge}
      onTouchStart={nudge}
      // Tabbing onto a hidden control shows the bar, as a mouse would.
      onFocusCapture={nudge}
      className={cx(
        // Never clipped or isolated: clipping a composited video paints it black,
        // and a stacking context put the subtitle menu behind the header.
        'group/player relative w-full bg-black select-none',
        // Filling the user-shaped floating window, or the 16:9 box leaves bands.
        pip.active ? 'h-full' : 'aspect-video',
        corners,
        !controlsVisible && 'cursor-none',
      )}
    >
      {/* Before the video, as the subtitle canvas goes right after it and later things paint over. */}
      {upscale?.enabled && (
        <canvas
          ref={setCanvas}
          onClick={onSurfaceClick}
          onPointerUp={onSurfaceTouch}
          className={cx(
            'absolute inset-0 size-full object-contain',
            corners,
            showUpscaled ? 'opacity-100' : 'pointer-events-none opacity-0',
          )}
        />
      )}

      {/* Never hidden: the canvas above paints only on presented frames. */}
      <video
        ref={setVideo}
        playsInline
        autoPlay={autoPlay}
        className={cx('size-full', corners)}
        onClick={onSurfaceClick}
        onPointerUp={onSurfaceTouch}
        onPlay={() => {
          setPlaying(true)
          onResume?.()
          // Any resume re-arms the hide timer, as a keyboard one moves no pointer.
          nudge()
        }}
        onPause={() => {
          setPlaying(false)
          setControlsVisible(true)
        }}
        // A new source resets to paused without a pause event; the button must not keep saying Pause over it.
        onEmptied={(e) => setPlaying(!e.currentTarget.paused)}
        onWaiting={() => setWaiting(true)}
        onPlaying={() => setWaiting(false)}
        onCanPlay={() => setWaiting(false)}
        // Paused seeks repaint only once the new position decodes.
        onSeeking={() => {
          markSeeking(true)
          onResume?.()
        }}
        onSeeked={() => markSeeking(false)}
        onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
        onDurationChange={(e) => setDuration(e.currentTarget.duration)}
        onVolumeChange={(e) => {
          setVolume(e.currentTarget.volume)
          setMuted(e.currentTarget.muted)
          savePersisted({ volume: e.currentTarget.volume, muted: e.currentTarget.muted })
        }}
      />

      {notice && (
        <div className="pointer-events-none absolute top-6 left-1/2 -translate-x-1/2 rounded-full bg-base-950/75 px-3 py-1 text-sm font-medium text-white tabular-nums backdrop-blur-sm">
          {notice !== VOLUME_NOTICE ? notice : muted ? 'Muted' : `Volume ${Math.round(volume * 100)}%`}
        </div>
      )}

      {(waiting || !stream) && !error && !lostError && (
        <div className="pointer-events-none absolute inset-0 grid place-items-center">
          <div className="text-center">
            <Spinner className="mx-auto size-8" />
            {/* No stream yet: still resolving, so the stall copy below doesn't apply. */}
            {!stream && (
              <>
                <p className="mt-3 text-sm text-base-300">{starting?.title ?? 'Finding a release…'}</p>
                {starting && (
                  <p className="mx-auto mt-1 max-w-md px-4 text-xs break-words text-base-500 tabular-nums">
                    {starting.detail}
                  </p>
                )}
              </>
            )}
            {/* A spinner looks the same buffering a second or with a dead swarm, so this says which. */}
            {stream && stalledFor > 12 && (
              <p className="mt-3 max-w-xs text-xs text-base-400">
                {/* What is arriving, never whether it is enough: a slow line buffers and plays on, as on any site. */}
                {!health || health.complete
                  ? 'Buffering…'
                  : health.peers > 0
                    ? `Buffering… ${health.mbps.toFixed(1)} Mbps from ${health.peers} ${health.peers === 1 ? 'peer' : 'peers'}`
                    : 'Buffering… no peers connected right now'}
              </p>
            )}
            {health?.slow && stalledFor > 20 &&
              (queued ? (
                <Link
                  to="/downloads"
                  className={cx(buttonClass('secondary', 'sm'), 'pointer-events-auto mt-3')}
                >
                  Queued · open Downloads
                </Link>
              ) : (
                <button
                  onClick={() => void downloadInstead()}
                  className={cx(buttonClass('secondary', 'sm'), 'pointer-events-auto mt-3')}
                >
                  Download it instead and watch later
                </button>
              ))}
            {queueError && <p className="mt-2 text-xs text-recap">{queueError}</p>}
          </div>
        </div>
      )}

      {(error || lostError) && (
        <div className={cx('absolute inset-0 grid place-items-center bg-base-950/80 p-6 text-center', corners)}>
          <div>
            <p className="text-sm text-base-200">{error ?? lostError}</p>
            <button
              onClick={() => {
                if (error) {
                  // A fresh source from where it stopped, playing once it can; the hook clears the error.
                  setResumeAt(video?.currentTime || startAt)
                  setWaiting(true)
                  resumeAfterRebuild(video, true)
                  setEpoch((e) => e + 1)
                  return
                }
                setLostError(null)
                recover()
              }}
              className={cx(buttonClass('secondary', 'sm'), 'pointer-events-auto mt-3')}
            >
              Try again
            </button>
          </div>
        </div>
      )}

      {flash && (
        <div
          className={cx(
            'pointer-events-none absolute top-1/2 -translate-y-1/2 rounded-full bg-base-950/70 px-4 py-2 text-sm font-medium text-white',
            flash === 'back' ? 'left-[12%]' : 'right-[12%]',
          )}
        >
          {flash === 'back' ? '−10s' : '+10s'}
        </div>
      )}

      {activeSkip && (
        <button
          onClick={() => video && (video.currentTime = activeSkip.end)}
          className="absolute right-4 bottom-24 rounded-md bg-base-950/85 px-4 py-2 text-sm font-medium text-white ring-1 ring-white/20 backdrop-blur-sm transition-transform hover:scale-105"
        >
          Skip {skipKind(activeSkip.kind) ?? 'ahead'}
          <kbd className="ml-2 font-sans text-xs text-white/50 max-sm:hidden">S</kbd>
        </button>
      )}

      <div
        className={cx(
          // The gradient passes taps to the picture (half of it on a phone); only its controls take input.
          'pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/90 via-black/50 to-transparent px-3 pt-10 pb-3 transition-opacity duration-200 sm:px-4',
          pip.active ? '' : 'sm:rounded-b-xl',
          controlsVisible ? 'opacity-100' : 'opacity-0',
        )}
      >
        <div className={cx(controlsVisible && 'pointer-events-auto')}>
        <Scrubber
          time={time}
          duration={duration}
          skips={skips}
          streamId={stream?.id}
          video={video}
          onSeek={(t) => video && (video.currentTime = t)}
        />

        <div className="mt-2 flex items-center gap-2 text-white">
          <IconButton label={playing ? 'Pause' : 'Play'} hint="K" onClick={togglePlay}>
            {playing ? <PauseIcon /> : <PlayIcon />}
          </IconButton>

          {/* Desktop only: on a phone double tap seeks and the hardware buttons set volume. */}
          <span className={cx('contents', !pip.active && 'max-sm:hidden')}>
          <IconButton label="Back 10 seconds" hint="J" onClick={() => seekBy(-10)}>
            <SeekIcon back />
          </IconButton>
          <IconButton label="Forward 10 seconds" hint="L" onClick={() => seekBy(10)}>
            <SeekIcon />
          </IconButton>
          </span>

          {onNext && (
            <IconButton label="Next episode" onClick={onNext}>
              <NextIcon />
            </IconButton>
          )}

          <span className="contents max-sm:hidden">
            <Volume
              volume={volume}
              muted={muted}
              onChange={(v) => {
                if (!video) return
                video.volume = v
                video.muted = v === 0
              }}
              onToggle={() => video && (video.muted = !video.muted)}
            />
          </span>

          <span className="ml-1 text-xs whitespace-nowrap tabular-nums text-white/80">
            {clockTime(time)}
            <span className="max-sm:hidden"> / {clockTime(duration)}</span>
          </span>

          <div className="ml-auto flex items-center gap-1">
            {!pip.active && (
              <span className="contents max-sm:hidden">
                <SpeedPicker value={rate} onChange={changeRate} />
              </span>
            )}

            {/* The floating window keeps only subtitles and the way back. */}
            {!pip.active && (stream?.audio?.length ?? 0) > 1 && (
              <AudioPicker
                tracks={stream!.audio!}
                value={audioTrack}
                onChange={switchAudio}
              />
            )}

            {!pip.active && stream && (stream.audio?.length ?? 0) > 0 && (
              <SoundPicker value={soundState} onChange={switchSound} />
            )}

            {(stream?.subtitles?.length ?? 0) > 0 && (
              <SubtitlePicker
                tracks={stream!.subtitles}
                value={track}
                onChange={setTrack}
                scale={subScale}
                onScale={changeSubScale}
                delay={subDelay}
                onDelay={setSubDelay}
              />
            )}

            {pip.active ? (
              <button
                onClick={() => void pip.toggle()}
                className="flex items-center gap-1.5 rounded-lg bg-white/15 px-2.5 py-1.5 text-xs font-medium text-white ring-1 ring-white/20 backdrop-blur-sm hover:bg-white/25"
              >
                <PiPIcon />
                Back to tab
              </button>
            ) : (
              <>
                {/* Not on a phone: no keys there, and no room in the bar. */}
                <span className="contents max-sm:hidden">
                  <IconButton
                    label="Keyboard shortcuts"
                    hint="?"
                    onClick={() => window.dispatchEvent(new Event(SHOW_SHORTCUTS))}
                  >
                    <KeyboardIcon />
                  </IconButton>
                </span>
                {pip.supported && (
                  <IconButton label="Picture in picture" onClick={() => void pip.toggle()}>
                    <PiPIcon />
                  </IconButton>
                )}
                <IconButton
                  label="Fullscreen"
                  hint="F"
                  onClick={() => {
                    toggleFullscreen(shell.current, video)
                  }}
                >
                  <FullscreenIcon />
                </IconButton>
              </>
            )}
          </div>
        </div>
        </div>
      </div>

      <div
        className={cx(
          'pointer-events-none absolute inset-x-0 top-0 bg-gradient-to-b from-black/70 to-transparent transition-opacity duration-200',
          pip.active ? 'p-2' : 'p-4',
          controlsVisible ? 'opacity-100' : 'opacity-0',
        )}
      >
        {/* One line in the floating window, which is small and already titled. */}
        {pip.active ? (
          <p className="truncate text-xs font-medium text-white/90">{subtitle ?? title}</p>
        ) : (
          <>
            <p className="text-sm font-medium text-white">{title}</p>
            {subtitle && <p className="text-xs text-white/70">{subtitle}</p>}
          </>
        )}
        {/* The toolbar says Anime4K is on; this says when it is not working. */}
        {(upscaleState === 'unsupported' || upscaleState === 'failed') && (
          <p className="mt-1.5 inline-block rounded bg-black/60 px-2 py-0.5 text-[11px] text-amber-300">
            {upscaleState === 'unsupported'
              ? "Anime4K needs WebGPU, which this browser doesn't have; playing without it."
              : 'Anime4K stopped (the graphics device was lost); playing without it.'}
          </p>
        )}
      </div>

      {overlay}
    </div>
  )

  return (
    <div ref={anchor} className="w-full">
      {createPortal(body, host)}
    </div>
  )
}

/** What an opening or ending is called, for the marker's tooltip. */
function skipLabel(kind: string): string {
  const k = skipKind(kind)
  return k ? k[0].toUpperCase() + k.slice(1) : kind
}

function Scrubber({
  time,
  duration,
  skips,
  streamId,
  video,
  onSeek,
}: {
  time: number
  duration: number
  skips: SkipRange[]
  streamId?: string
  video: HTMLVideoElement | null
  onSeek: (t: number) => void
}) {
  const sheet = useThumbnails(streamId, video)

  // Where the pointer is, as a fraction. Null when it is not over the bar.
  const [at, setAt] = useState<number | null>(null)
  // Pressed and moving: the bar follows the finger and the seek lands on
  // release, the way every video site does it. A tap is a drag of no distance.
  const [dragging, setDragging] = useState(false)
  const hoverTime = at === null ? 0 : at * duration

  const fraction = (e: React.PointerEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    return Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width))
  }

  const shown = dragging && at !== null ? at * duration : time
  const percent = duration > 0 ? (shown / duration) * 100 : 0
  const loaded = useLoadedRanges(streamId, video)

  return (
    <div
      // touch-none, or the page scrolls instead of scrubbing; taller than the bar, for fingers.
      className="group/scrub relative -my-1 h-6 cursor-pointer touch-none"
      onPointerDown={(e) => {
        if (e.button !== 0) return
        e.currentTarget.setPointerCapture(e.pointerId)
        setDragging(true)
        setAt(fraction(e))
      }}
      onPointerMove={(e) => setAt(fraction(e))}
      onPointerUp={(e) => {
        if (dragging && duration > 0) onSeek(fraction(e) * duration)
        setDragging(false)
        if (e.pointerType !== 'mouse') setAt(null)
      }}
      onPointerCancel={() => {
        setDragging(false)
        setAt(null)
      }}
      onPointerLeave={() => {
        if (!dragging) setAt(null)
      }}
    >
      <div className="absolute top-1/2 h-1 w-full -translate-y-1/2 overflow-hidden rounded-full bg-white/20">
        {/* What is loaded and will play without waiting, lighter than the rest of the bar. */}
        {duration > 0 &&
          loaded.map(([from, to]) => (
            <div
              key={from}
              data-loaded
              className="absolute inset-y-0 bg-white/35"
              style={{ left: `${(from / duration) * 100}%`, width: `${((to - from) / duration) * 100}%` }}
            />
          ))}
        <div className="relative h-full bg-accent-500" style={{ width: `${percent}%` }} />
      </div>

      {/* Openings and endings in amber, so what is about to be skipped shows before the button. */}
      {duration > 0 &&
        skips.map((range) => (
          <div
            key={`${range.kind}-${range.start}`}
            title={`${skipLabel(range.kind)} · ${clockTime(range.start)}–${clockTime(range.end)}`}
            className="absolute top-1/2 h-1 -translate-y-1/2 rounded-full bg-filler"
            style={{
              left: `${(range.start / duration) * 100}%`,
              width: `${Math.max(0.4, ((range.end - range.start) / duration) * 100)}%`,
            }}
          />
        ))}

      <div
        className={cx(
          'absolute top-1/2 size-3 -translate-x-1/2 -translate-y-1/2 rounded-full bg-white transition-opacity group-hover/scrub:opacity-100',
          dragging ? 'scale-125 opacity-100' : 'opacity-0',
        )}
        style={{ left: `${percent}%` }}
      />

      {at !== null && duration > 0 && (
        <ScrubPreview at={at} time={hoverTime} sheet={sheet} skips={skips} />
      )}
    </div>
  )
}

// The stretches that will play without waiting on the network, as [from, to] seconds: the lighter part of
// the bar. Asked of the server; the browser's own buffer is a small window around the playhead, no more.
function useLoadedRanges(streamId: string | undefined, video: HTMLVideoElement | null): Array<[number, number]> {
  const [ranges, setRanges] = useState<Array<[number, number]>>([])
  useEffect(() => {
    setRanges([])
    if (!streamId) return
    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const read = async () => {
      try {
        const res = await api.get<{ complete: boolean; ranges: Array<[number, number]> }>(`/api/stream/${streamId}/loaded`)
        if (stopped) return
        setRanges(res.ranges)
        // Asked again every few seconds while downloading, only while watched: a poll keeps the session alive.
        if (!res.complete) timer = setTimeout(() => void untilWatching(video, () => stopped).then(read), 4000)
      } catch {
        if (!stopped) timer = setTimeout(read, 10_000)
      }
    }
    void read()
    return () => {
      stopped = true
      clearTimeout(timer)
    }
  }, [streamId, video])
  return ranges
}

/** The frame under the pointer, cut from one sprite sheet (no requests), with its time; the time alone until then. */
function ScrubPreview({
  at,
  time,
  sheet,
  skips,
}: {
  at: number
  time: number
  sheet: Sheet | undefined
  skips: SkipRange[]
}) {
  const ready = sheet?.ready && sheet.interval > 0
  const inSkip = skips.find((r) => time >= r.start && time <= r.end)

  let tile: React.CSSProperties | undefined
  if (ready && sheet) {
    const index = Math.min(sheet.count - 1, Math.max(0, Math.floor(time / sheet.interval)))
    const column = index % sheet.columns
    const row = Math.floor(index / sheet.columns)
    tile = {
      width: sheet.width,
      height: sheet.height,
      backgroundImage: `url(${sheet.url})`,
      backgroundSize: `${sheet.columns * sheet.width}px ${sheet.rows * sheet.height}px`,
      backgroundPosition: `-${column * sheet.width}px -${row * sheet.height}px`,
    }
  }

  return (
    <div
      className="pointer-events-none absolute bottom-5 -translate-x-1/2"
      // Clamped so the preview cannot hang off either end of the player.
      style={{ left: `clamp(${(sheet?.width ?? 80) / 2}px, ${at * 100}%, calc(100% - ${(sheet?.width ?? 80) / 2}px))` }}
    >
      <div className="overflow-hidden rounded-md bg-base-950/90 shadow-lift ring-1 ring-white/15">
        {tile && <div style={tile} />}
        <p className="px-2 py-1 text-center text-xs font-medium tabular-nums text-white">
          {clockTime(time)}
          {inSkip && <span className="ml-1.5 text-filler">{skipLabel(inSkip.kind)}</span>}
        </p>
      </div>
    </div>
  )
}

function Volume({
  volume,
  muted,
  onChange,
  onToggle,
}: {
  volume: number
  muted: boolean
  onChange: (v: number) => void
  onToggle: () => void
}) {
  return (
    <div className="group/vol flex items-center">
      <IconButton label={muted ? 'Unmute' : 'Mute'} hint="M" onClick={onToggle}>
        {muted || volume === 0 ? <MutedIcon /> : <VolumeIcon />}
      </IconButton>
      <input
        type="range"
        min={0}
        max={1}
        step={0.05}
        value={muted ? 0 : volume}
        onChange={(e) => onChange(Number(e.target.value))}
        aria-label="Volume"
        className="w-0 accent-[var(--color-accent-500)] opacity-0 transition-all duration-200 group-hover/vol:w-20 group-hover/vol:opacity-100 group-focus-within/vol:w-20 group-focus-within/vol:opacity-100"
      />
    </div>
  )
}

/** A plain menu, as the OS paints a native select white over the dark player. */
function SubtitlePicker({
  tracks,
  value,
  onChange,
  scale,
  onScale,
  delay,
  onDelay,
}: {
  tracks: StreamInfo['subtitles']
  value: number | null
  onChange: (index: number | null) => void
  scale: number
  onScale: (scale: number) => void
  delay: number
  onDelay: (delay: number) => void
}) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close, open)

  // Language first; the release's track name only when it tells two apart (Erai-raws calls all "CR").
  const label = (t: StreamInfo['subtitles'][number]) => {
    const language = languageName(t.language)
    const sameLanguage = tracks.filter((o) => languageName(o.language) === language)
    if (!language) return t.title || `Track ${t.index + 1}`
    if (sameLanguage.length > 1 && t.title) return `${language} · ${t.title}`
    return language
  }
  const active = tracks.find((t) => t.index === value)

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        title="Subtitles"
        className="flex h-9 items-center gap-1.5 rounded-md px-2.5 text-xs text-white/90 transition-colors hover:bg-white/15"
      >
        <CaptionIcon />
        <span className="max-w-24 truncate">{active ? label(active) : 'Off'}</span>
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 bottom-full z-50 mb-2 w-52 origin-bottom-right animate-rise rounded-lg border border-base-700 bg-base-850 py-1 shadow-xl shadow-black/60"
        >
          {/* Only the tracks scroll, so size and delay stay in view. */}
          <div className="max-h-52 overflow-y-auto scrollbar-thin">
            <MenuRow selected={value === null} onClick={() => { onChange(null); close() }}>
              Off
            </MenuRow>
            {tracks.map((t) => (
              <MenuRow
                key={t.index}
                selected={t.index === value}
                onClick={() => { onChange(t.index); close() }}
              >
                {label(t)}
              </MenuRow>
            ))}
          </div>
          {value !== null && (
            <div className="mt-1 border-t border-base-700 pt-1">
              <Stepper
                label="Size"
                value={`${Math.round(scale * 100)}%`}
                onLess={() => onScale(SUB_SCALES[Math.max(0, SUB_SCALES.indexOf(scale) - 1)] ?? 1)}
                onMore={() => onScale(SUB_SCALES[Math.min(SUB_SCALES.length - 1, SUB_SCALES.indexOf(scale) + 1)] ?? 1)}
                onReset={() => onScale(1)}
              />
              <Stepper
                label="Delay"
                value={delayLabel(delay)}
                onLess={() => onDelay(Math.round((delay - DELAY_STEP) * 10) / 10)}
                onMore={() => onDelay(Math.round((delay + DELAY_STEP) * 10) / 10)}
                onReset={() => onDelay(0)}
              />
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/** A − value + row; the value resets it. */
function Stepper({
  label,
  value,
  onLess,
  onMore,
  onReset,
}: {
  label: string
  value: string
  onLess: () => void
  onMore: () => void
  onReset: () => void
}) {
  const step = 'grid size-6 pointer-coarse:size-10 place-items-center rounded text-base-200 hover:bg-base-750 hover:text-white'
  return (
    <div className="flex items-center gap-1 px-3 py-1 text-sm">
      <span className="flex-1 text-base-400">{label}</span>
      <button type="button" role="menuitem" aria-label={`${label} down`} onClick={onLess} className={step}>
        −
      </button>
      <button
        type="button"
        role="menuitem"
        aria-label={`Reset ${label.toLowerCase()}`}
        title="Reset"
        onClick={onReset}
        className="w-12 rounded text-center text-xs tabular-nums text-base-200 hover:bg-base-750"
      >
        {value}
      </button>
      <button type="button" role="menuitem" aria-label={`${label} up`} onClick={onMore} className={step}>
        +
      </button>
    </div>
  )
}

function AudioPicker({
  tracks,
  value,
  onChange,
}: {
  tracks: AudioTrack[]
  value: number
  onChange: (index: number) => void
}) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close, open)

  const label = (t: AudioTrack, i: number) => {
    const language = languageName(t.language)
    const sameLanguage = tracks.filter((o) => languageName(o.language) === language)
    if (!language) return t.title || `Track ${i + 1}`
    if (sameLanguage.length > 1 && t.title) return `${language} · ${t.title}`
    return language
  }

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        title="Audio"
        className="flex h-9 items-center gap-1.5 rounded-md px-2.5 text-xs text-white/90 transition-colors hover:bg-white/15"
      >
        <SpeakerIcon />
        <span className="max-w-24 truncate">{tracks[value] ? label(tracks[value], value) : 'Audio'}</span>
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 bottom-full z-50 mb-2 max-h-64 w-44 origin-bottom-right animate-rise overflow-y-auto rounded-lg border border-base-700 bg-base-850 py-1 shadow-xl shadow-black/60 scrollbar-thin"
        >
          {tracks.map((t, i) => (
            <MenuRow key={i} selected={i === value} onClick={() => { onChange(i); close() }}>
              {label(t, i)}
            </MenuRow>
          ))}
        </div>
      )}
    </div>
  )
}

const LEVEL_LABELS: Record<VoiceLevel, string> = { off: 'Off', medium: 'Medium', high: 'High' }

/** The query for a sound change: only the fields given, so the rest stay as they are on the server. */
function soundQuery(patch: Partial<SoundOptions>): string {
  const q = new URLSearchParams()
  for (const [key, value] of Object.entries(patch)) if (value !== undefined) q.set(key, String(value))
  const s = q.toString()
  return s ? `?${s}` : ''
}

/** What is on, in a few words, for the button. */
function soundSummary(s: SoundOptions): string {
  const on = []
  if (s.voice !== 'off') on.push(`Voice ${LEVEL_LABELS[s.voice].toLowerCase()}`)
  if (s.night) on.push('Night')
  if (s.headphones) on.push('Headphones')
  if (s.mono) on.push('Mono')
  if (s.boost !== 'off') on.push(`Boost ${LEVEL_LABELS[s.boost].toLowerCase()}`)
  return on.length ? on.join(', ') : 'Default'
}

// Enhancing (what the ear picks out) and boosting (how loud the whole thing is) are different asks,
// so they sit apart, as on the services that offer both.
function SoundPicker({ value, onChange }: { value: SoundOptions; onChange: (patch: Partial<SoundOptions>) => void }) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close, open)
  const summary = soundSummary(value)
  const heading = 'px-3 pt-2 pb-1 text-[10px] font-semibold tracking-wider text-base-500 uppercase'

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        title="Sound: voice boost (V), night mode, headphones, mono, volume boost"
        aria-label={`Sound: ${summary}`}
        className={cx(
          'flex h-9 items-center gap-1.5 rounded-md px-2.5 text-xs transition-colors hover:bg-white/15',
          summary === 'Default' ? 'text-white/90' : 'text-accent-300',
        )}
      >
        <VoiceIcon />
        <span className="max-w-32 truncate max-sm:hidden">{summary === 'Default' ? 'Sound' : summary}</span>
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 bottom-full z-50 mb-2 w-52 origin-bottom-right animate-rise rounded-lg border border-base-700 bg-base-850 py-1 shadow-xl shadow-black/60"
        >
          <p className={heading}>Voice boost</p>
          {VOICE_LEVELS.map((level) => (
            <MenuRow key={level} selected={level === value.voice} onClick={() => onChange({ voice: level })}>
              {LEVEL_LABELS[level]}
            </MenuRow>
          ))}
          <p className={heading}>Enhance</p>
          <MenuRow selected={value.night} onClick={() => onChange({ night: !value.night })}>Night mode</MenuRow>
          <MenuRow selected={value.headphones} onClick={() => onChange({ headphones: !value.headphones })}>Headphones</MenuRow>
          <MenuRow selected={value.mono} onClick={() => onChange({ mono: !value.mono })}>Mono</MenuRow>
          <p className={heading}>Volume boost</p>
          {VOICE_LEVELS.map((level) => (
            <MenuRow key={level} selected={level === value.boost} onClick={() => onChange({ boost: level })}>
              {LEVEL_LABELS[level]}
            </MenuRow>
          ))}
        </div>
      )}
    </div>
  )
}

function VoiceIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path
        d="M12 3a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V6a3 3 0 0 0-3-3Zm-6 9a6 6 0 0 0 12 0M12 18v3"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function SpeedPicker({ value, onChange }: { value: number; onChange: (rate: number) => void }) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close, open)

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Playback speed"
        onClick={() => setOpen((v) => !v)}
        title="Speed"
        className="flex h-9 items-center rounded-md px-2.5 text-xs tabular-nums text-white/90 transition-colors hover:bg-white/15"
      >
        {value}×
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 bottom-full z-50 mb-2 w-24 origin-bottom-right animate-rise overflow-hidden rounded-lg border border-base-700 bg-base-850 py-1 shadow-xl shadow-black/60"
        >
          {SPEEDS.map((s) => (
            <MenuRow key={s} selected={s === value} onClick={() => { onChange(s); close() }}>
              {s}×
            </MenuRow>
          ))}
        </div>
      )}
    </div>
  )
}

function SpeakerIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4 shrink-0" aria-hidden>
      <path d="M4 9.5v5h3.5L12 18.5v-13L7.5 9.5H4z" {...stroke} />
      <path d="M15.5 9a4 4 0 0 1 0 6M18 6.5a7.5 7.5 0 0 1 0 11" {...stroke} />
    </svg>
  )
}

function MenuRow({
  selected,
  onClick,
  children,
}: {
  selected: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      role="menuitemradio"
      aria-checked={selected}
      onClick={onClick}
      className={cx(
        'block w-full truncate px-3 py-1.5 text-left text-sm transition-colors',
        selected ? 'text-accent-400' : 'text-base-200 hover:bg-base-750',
      )}
    >
      {children}
    </button>
  )
}

function CaptionIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4 shrink-0" aria-hidden>
      <rect x="3" y="5" width="18" height="14" rx="2" {...stroke} />
      <path d="M8 11.5a1.6 1.6 0 1 0 0 1.6M15 11.5a1.6 1.6 0 1 0 0 1.6" {...stroke} />
    </svg>
  )
}

function useKeyboard({
  video,
  togglePlay,
  seekBy,
  nudge,
  shell,
  extra,
  showVolume,
  shiftSubDelay,
  skipTo,
  cycleVoice,
}: {
  video: HTMLVideoElement | null
  togglePlay: () => void
  seekBy: (n: number) => void
  nudge: () => void
  shell: HTMLElement | null
  /** The picture-in-picture window, whose keys never reach this one. */
  extra: Window | null
  showVolume: () => void
  /** z and x, as in mpv; absent while subtitles are off. */
  shiftSubDelay?: (by: number) => void
  /** Where the opening or ending on screen ends; absent when there is none to skip. */
  skipTo?: number
  /** v: off, medium, high, round again; absent before the stream is open. */
  cycleVoice?: () => void
}) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement | null
      if (target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) return
      // An open dialog or menu owns the keyboard, or Space wouldn't press its buttons.
      if (target?.closest('[role=dialog], [role=menu], [role=listbox], [data-portal-menu]')) return
      // Space on a button outside the player is that button's own.
      if (e.key === ' ' && target?.closest('button, a, [role=switch], [role=tab]') && !shell?.contains(target)) return
      // Ctrl+F, Ctrl+L and the like belong to the browser.
      if (e.ctrlKey || e.metaKey || e.altKey) return
      // The page must not scroll while the arrows seek or change volume.
      if (e.key.startsWith('Arrow')) e.preventDefault()

      switch (e.key) {
        case ' ':
        case 'k':
          e.preventDefault()
          togglePlay()
          break
        case 'ArrowLeft':
          seekBy(-5)
          break
        case 'ArrowRight':
          seekBy(5)
          break
        case 'j':
          seekBy(-10)
          break
        case 'l':
          seekBy(10)
          break
        case 'ArrowUp':
          if (video) video.volume = Math.min(1, video.volume + 0.1)
          showVolume()
          break
        case 'ArrowDown':
          if (video) video.volume = Math.max(0, video.volume - 0.1)
          showVolume()
          break
        case 'm':
          if (video) video.muted = !video.muted
          showVolume()
          break
        case 'f':
          toggleFullscreen(shell, video)
          break
        case 'z':
        case 'x':
          if (!shiftSubDelay) return
          shiftSubDelay(e.key === 'z' ? -DELAY_STEP : DELAY_STEP)
          break
        case 's':
          // The same as the button, so only while it shows.
          if (skipTo === undefined || !video) return
          video.currentTime = skipTo
          break
        case 'v':
          if (!cycleVoice) return
          cycleVoice()
          break
        default:
          return
      }
      // Only for a key that did something.
      nudge()
    }
    window.addEventListener('keydown', onKey)
    extra?.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      extra?.removeEventListener('keydown', onKey)
    }
  }, [video, togglePlay, seekBy, nudge, shell, extra, showVolume, shiftSubDelay, skipTo, cycleVoice])
}

function IconButton({
  label,
  hint,
  onClick,
  children,
}: {
  label: string
  /** The key that does the same, named in the tooltip: hovering a button is how its key gets learned. */
  hint?: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={hint ? `${label} (${hint})` : label}
      className="tap relative grid size-9 place-items-center rounded-md text-white/90 transition-colors hover:bg-white/15 hover:text-white"
    >
      {children}
    </button>
  )
}

const stroke = {
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
}

function PauseIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path d="M8 5h3v14H8zM13 5h3v14h-3z" fill="currentColor" />
    </svg>
  )
}

function SeekIcon({ back }: { back?: boolean }) {
  return (
    <svg viewBox="0 0 24 24" className={cx('size-5', back && 'scale-x-[-1]')} aria-hidden>
      <path d="M4 12a8 8 0 1 1 2.5 5.8" {...stroke} />
      <path d="M4 7v5h5" {...stroke} />
    </svg>
  )
}

function NextIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path d="M6 5.5v13l9-6.5-9-6.5Z" fill="currentColor" />
      <path d="M17 5v14" {...stroke} strokeWidth={2} />
    </svg>
  )
}

function VolumeIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path d="M4 9v6h3.5L12 19V5L7.5 9H4Z" fill="currentColor" />
      <path d="M16 9.5a3.5 3.5 0 0 1 0 5M18.5 7a7 7 0 0 1 0 10" {...stroke} />
    </svg>
  )
}

function MutedIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path d="M4 9v6h3.5L12 19V5L7.5 9H4Z" fill="currentColor" />
      <path d="m16 9.5 5 5M21 9.5l-5 5" {...stroke} />
    </svg>
  )
}

function KeyboardIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <rect x="3" y="6" width="18" height="12" rx="2" {...stroke} />
      <path d="M7 10h.01M11 10h.01M15 10h.01M8 14h8" {...stroke} strokeLinecap="round" />
    </svg>
  )
}

function PiPIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <rect x="3" y="5" width="18" height="14" rx="2" {...stroke} />
      <rect x="12" y="11" width="7" height="6" rx="1" fill="currentColor" />
    </svg>
  )
}

function FullscreenIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <path d="M4 9V5h4M20 9V5h-4M4 15v4h4M20 15v4h-4" {...stroke} />
    </svg>
  )
}
