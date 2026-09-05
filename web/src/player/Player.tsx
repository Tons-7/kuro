import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import {
  api,
  type AudioTrack,
  type SkipRange,
  type StreamHealth,
  type StreamInfo,
  type SubtitleTrack,
} from '../lib/api'
import { clockTime, cx, languageName } from '../lib/format'
import { PlayIcon } from '../components/PosterCard'
import { Spinner, useDismiss } from '../components/ui'
import { useAnime4K } from './anime4k'
import {
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

const ENGLISH = /^(en|eng|english)$/i
// A signs-and-songs track translates only on-screen text; playing it by default
// looks like broken subtitles rather than the wrong track.
const SIGNS = /\b(signs?|songs?|s&s|forced)\b/i

/**
 * Which track to show. The file's default flag is trusted last: multi-language
 * releases often flag one arbitrarily, not the one anyone here can read.
 */
export function chooseTrack(tracks: SubtitleTrack[]): SubtitleTrack {
  const dialogue = (t: SubtitleTrack) => !SIGNS.test(t.title ?? '')
  const english = (t: SubtitleTrack) => ENGLISH.test(t.language ?? '')

  return (
    tracks.find((t) => english(t) && dialogue(t) && t.default) ??
    tracks.find((t) => english(t) && dialogue(t)) ??
    tracks.find((t) => english(t)) ??
    tracks.find((t) => dialogue(t) && t.default) ??
    tracks.find((t) => dialogue(t)) ??
    tracks.find((t) => t.default) ??
    tracks[0]
  )
}

const SPEEDS = [0.75, 1, 1.25, 1.5, 2]

// Volume, mute and speed carry from episode to episode; the element itself
// starts every page at full volume.
const PERSIST_KEY = 'kuro.player'
interface Persisted {
  volume?: number
  muted?: boolean
  rate?: number
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
  startAt: number
  skips: SkipRange[]
  autoSkip: { op: boolean; ed: boolean }
  autoPlay: boolean
  upscale?: { enabled: boolean; mode: string }
  title: string
  subtitle?: string
  /** played is media time that actually played since the last report. */
  onProgress: (position: number, duration: number, played: number) => void
  onEnded: () => void
  onNext?: () => void
  /** The server no longer knows the stream; reopen it under the same URLs. */
  onSessionLost?: () => Promise<unknown>
  /** Drawn inside the player, so it survives fullscreen and picture in picture. */
  overlay?: ReactNode
}

export function Player({
  stream,
  startAt,
  skips,
  autoSkip,
  autoPlay,
  upscale,
  title,
  subtitle,
  onProgress,
  onEnded,
  onNext,
  onSessionLost,
  overlay,
}: PlayerProps) {
  const [video, setVideo] = useState<HTMLVideoElement | null>(null)
  const [canvas, setCanvas] = useState<HTMLCanvasElement | null>(null)
  const shell = useRef<HTMLDivElement>(null)

  const [playing, setPlaying] = useState(false)
  const [waiting, setWaiting] = useState(true)
  const [time, setTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)
  const [rate, setRate] = useState(() => loadPersisted().rate ?? 1)
  const [controlsVisible, setControlsVisible] = useState(true)
  const [track, setTrack] = useState<number | null>(null)

  // Restore what the last episode was left at; a new source resets the
  // element's playback rate, so it is reapplied on every load.
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

  // An audio switch re-encodes every segment, and a recovered session encodes
  // afresh; either way old buffered segments and the new pass must not share a
  // SourceBuffer, so the epoch forces a fresh HLS instance and resumeAt seeks it.
  const [audioTrack, setAudioTrack] = useState(0)
  const [epoch, setEpoch] = useState(0)
  const [resumeAt, setResumeAt] = useState(startAt)

  // Reset during render, not in an effect: the player outlives an episode
  // change now, and the HLS effect below runs first — it would attach the new
  // stream at the previous episode's position.
  const [attached, setAttached] = useState(stream?.id)
  if (stream?.id !== attached) {
    setAttached(stream?.id)
    setAudioTrack(stream?.audioTrack ?? 0)
    setEpoch(0)
    setResumeAt(startAt)
    // Or the scrubber reads the last episode's length and a skip button for its
    // ranges sits over the new one.
    setTime(0)
    setDuration(0)
  }

  const playlist = stream?.playlist
    ? `${stream.playlist}${epoch ? `?a=${epoch}` : ''}`
    : undefined

  const switchAudio = useCallback(
    (index: number) => {
      const id = stream?.id
      if (!id || index === audioTrack) return
      setResumeAt(video?.currentTime ?? startAt)
      setAudioTrack(index)
      setEpoch((e) => e + 1)
      void api.post(`/api/stream/${id}/audio?track=${index}`).catch(() => {})
    },
    [stream?.id, audioTrack, video, startAt],
  )

  // Filled below once hls exists; keepalive and fatal 404 can notice together,
  // so one reopen at a time.
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
    void onSessionLost()
      .then(() => {
        // The rebuilt session picks the track from the sub/dub preference, so a
        // mid-episode switch has to be asked for again or the menu lies.
        if (track !== 0 && stream?.id) {
          void api.post(`/api/stream/${stream.id}/audio?track=${track}`).catch(() => {})
        }
        setResumeAt(video?.currentTime ?? startAt)
        setEpoch((e) => e + 1)
        // The rebuild resets the element to paused; only autoplay restarts it.
        if (wasPlaying && video) {
          video.addEventListener('canplay', () => void video.play().catch(() => {}), {
            once: true,
          })
        }
      })
      .catch(() => {})
      .finally(() => {
        recovering.current = false
      })
  }
  useSeekRecovery(video, hls)
  useStallWatchdog(video, hls)
  const fonts = useEmbeddedFonts(stream?.fontsUrl)
  const pip = useDocumentPiP(shell.current)
  // Picture in picture moves the player into a second document, which the
  // subtitle renderer has to be rebuilt for: its canvas belongs to whichever
  // document it was created in.
  useSubtitles(video, stream, track, fonts, pip.active)
  useAutoSkip(video, skips, autoSkip)

  const upscaleState = useAnime4K({
    video,
    canvas,
    // Not while floating: the upscaler's canvas is bound to the surface of the
    // document it was configured in, and following the player into another one
    // leaves it rendering into nothing.
    enabled: !!upscale?.enabled && !pip.active,
    mode: upscale?.mode ?? 'A',
    // The playlist, not the session id: the id is the episode and does not
    // change when a different release is picked for it.
    source: playlist,
  })
  // Only worth showing while it holds the current frame; seeking or a stopped
  // renderer leaves it stale, and the plain video underneath should show.
  const painted = useFreshFrames(video, upscaleState === 'on')
  const showUpscaled = upscaleState === 'on' && painted

  // Keyed on the tracks themselves: the session id is the episode, so it is
  // unchanged both by a recovery (where re-picking would drop the viewer's
  // choice) and by a hand-picked release (where the old index is meaningless).
  const trackList = (stream?.subtitles ?? [])
    .map((s) => `${s.index}:${s.language ?? ''}:${s.title ?? ''}`)
    .join('|')
  useEffect(() => {
    setTrack(stream?.subtitles?.length ? chooseTrack(stream.subtitles).index : null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trackList])

  // The element is reused from one episode to the next so fullscreen survives,
  // and its autoplay attribute only ever fires once. Read from a ref so
  // flipping the switch mid-episode cannot resume a deliberate pause.
  const autoPlayRef = useRef(autoPlay)
  autoPlayRef.current = autoPlay
  useEffect(() => {
    if (!video || !stream?.id || !autoPlayRef.current) return
    const start = () => void video.play().catch(() => {})
    video.addEventListener('canplay', start, { once: true })
    return () => video.removeEventListener('canplay', start)
  }, [video, stream?.id])

  const reportRef = useRef(onProgress)
  reportRef.current = onProgress
  const endedRef = useRef(onEnded)
  endedRef.current = onEnded

  // Report on a timer, not every timeupdate. Each report carries how much
  // actually played: a step larger than a second or two is a seek, not play.
  useEffect(() => {
    if (!video) return

    let played = 0
    let last = video.currentTime
    const onTime = () => {
      const now = video.currentTime
      const step = now - last
      if (step > 0 && step < 2) played += step
      last = now
    }
    const onSeeking = () => {
      last = video.currentTime
    }

    const report = (position: number) => {
      reportRef.current(position, video.duration, played)
      played = 0
    }
    const id = window.setInterval(() => {
      if (!video.paused && video.duration > 0) report(video.currentTime)
    }, 10_000)

    const flush = () => {
      if (video.duration > 0) report(video.currentTime)
    }
    const ended = () => {
      if (video.duration > 0) report(video.duration)
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
      flush()
    }
  }, [video])

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

  // On touch, a double tap on either third of the picture seeks ten seconds
  // that way; a single tap still toggles play. Mouse clicks are unchanged.
  const lastTap = useRef<{ at: number; x: number }>({ at: 0, x: 0 })
  const touchedAt = useRef(0)
  const [flash, setFlash] = useState<'back' | 'forward' | null>(null)
  const onSurfaceTouch = useCallback(
    (e: React.PointerEvent<HTMLElement>) => {
      if (e.pointerType !== 'touch') return
      const now = performance.now()
      touchedAt.current = now
      const rect = e.currentTarget.getBoundingClientRect()
      const x = (e.clientX - rect.left) / rect.width
      const prev = lastTap.current
      lastTap.current = { at: now, x }
      if (now - prev.at < 300 && Math.abs(prev.x - x) < 0.2 && (x < 0.35 || x > 0.65)) {
        const dir = x < 0.35 ? 'back' : 'forward'
        seekBy(dir === 'back' ? -10 : 10)
        setFlash(dir)
        window.setTimeout(() => setFlash(null), 500)
        lastTap.current = { at: 0, x }
        return
      }
      togglePlay()
    },
    [togglePlay, seekBy],
  )
  // The click that follows a touch is the same gesture, already handled.
  const onSurfaceClick = useCallback(() => {
    if (performance.now() - touchedAt.current < 700) return
    togglePlay()
  }, [togglePlay])

  // Hide the controls while playing, but never while paused: a paused player
  // with no controls looks broken.
  const hideTimer = useRef<number | undefined>(undefined)
  const nudge = useCallback(() => {
    setControlsVisible(true)
    window.clearTimeout(hideTimer.current)
    hideTimer.current = window.setTimeout(() => {
      if (!video?.paused) setControlsVisible(false)
    }, 2600)
  }, [video])

  useKeyboard({ video, togglePlay, seekBy, nudge, shell: shell.current })

  // How long the picture has been stuck, so a slow swarm can say so rather
  // than looking like a hang.
  const [stalledFor, setStalledFor] = useState(0)
  // Carried over, it declares the next episode too slow before it has loaded.
  useEffect(() => setStalledFor(0), [stream?.id])

  // Numbers for the stall message: a swarm too slow for the bitrate is a
  // download, not a wait.
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
    const poll = () =>
      api
        .get<StreamHealth>(`/api/stream/${id}/health`)
        .then((h) => {
          if (!stop) setHealth(h)
        })
        .catch(() => {})
    void poll()
    const timer = window.setInterval(() => void poll(), 5000)
    return () => {
      stop = true
      window.clearInterval(timer)
    }
  }, [stream?.id, stalledLong])

  const downloadInstead = async () => {
    const [animeId, episode] = (stream?.id ?? '').split('-').map(Number)
    if (!animeId || !episode) return
    await api.post('/api/download', { animeId, episode }).catch(() => {})
    setQueued(true)
  }
  useEffect(() => {
    if (!waiting && stream) {
      setStalledFor(0)
      return
    }
    const id = window.setInterval(() => setStalledFor((s) => s + 1), 1000)
    return () => window.clearInterval(id)
  }, [waiting, stream])

  const activeSkip = skips.find((r) => time >= r.start && time < r.end)

  // Floating in its own window, the player fills it; fullscreen is handled in
  // CSS, which a second document's stylesheets still carry but `:fullscreen`
  // never matches there.
  const corners = pip.active ? '' : 'sm:rounded-xl'

  return (
    <div
      ref={shell}
      onMouseMove={nudge}
      onTouchStart={nudge}
      className={cx(
        // Never clipped or isolated: clipping a composited video paints it black,
        // and a stacking context put the subtitle menu behind the header.
        'group/player relative w-full bg-black select-none',
        // Filling the floating window, which the user shapes; the 16:9 box
        // would otherwise leave bands of its own inside it.
        pip.active ? 'h-full' : 'aspect-video',
        corners,
        !controlsVisible && 'cursor-none',
      )}
    >
      {/* Before the video: the subtitle renderer inserts its canvas right after
          it, so anything declared later paints over the subtitles. */}
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

      {/* Never hidden: the canvas above only paints on a presented frame, so a
          pause, a seek or a lost device would leave nothing on screen. */}
      <video
        ref={setVideo}
        playsInline
        autoPlay={autoPlay}
        className={cx('size-full', corners)}
        onClick={onSurfaceClick}
        onPointerUp={onSurfaceTouch}
        onPlay={() => {
          setPlaying(true)
          // Resuming by any route re-arms the hide timer; a keyboard resume
          // moves no pointer, so nothing else would.
          nudge()
        }}
        onPause={() => {
          setPlaying(false)
          setControlsVisible(true)
        }}
        onWaiting={() => setWaiting(true)}
        onPlaying={() => setWaiting(false)}
        onCanPlay={() => setWaiting(false)}
        onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
        onDurationChange={(e) => setDuration(e.currentTarget.duration)}
        onVolumeChange={(e) => {
          setVolume(e.currentTarget.volume)
          setMuted(e.currentTarget.muted)
          savePersisted({ volume: e.currentTarget.volume, muted: e.currentTarget.muted })
        }}
      />

      {(waiting || !stream) && !error && (
        <div className="pointer-events-none absolute inset-0 grid place-items-center">
          <div className="text-center">
            <Spinner className="mx-auto size-8" />
            {/* No stream yet means a release is still being resolved; the stall
                copy below would blame a download that has not started. */}
            {!stream && (
              <p className="mt-3 text-xs text-base-400">Finding a release…</p>
            )}
            {/* A frozen frame with a spinner looks the same whether it is
                buffering for a second or the swarm has stopped feeding us. */}
            {stream && stalledFor > 12 && (
              <p className="mt-3 max-w-xs text-xs text-base-400">
                {health?.slow
                  ? `Peers are sending ${health.mbps.toFixed(1)} Mbps; playing needs about ${health.needed.toFixed(0)}.`
                  : stalledFor > 40
                    ? 'This release is downloading too slowly to stream. Try another episode or come back later.'
                    : 'Waiting for the download to catch up…'}
              </p>
            )}
            {health?.slow && stalledFor > 20 && (
              <button
                onClick={() => void downloadInstead()}
                disabled={queued}
                className="pointer-events-auto mt-3 rounded-md bg-base-800 px-3 py-1.5 text-xs text-base-100 hover:bg-base-700 disabled:opacity-50"
              >
                {queued ? 'Queued — see Downloads' : 'Download it instead and watch later'}
              </button>
            )}
          </div>
        </div>
      )}

      {error && (
        <div className={cx('absolute inset-0 grid place-items-center bg-base-950/80 p-6 text-center', corners)}>
          <p className="text-sm text-base-200">{error}</p>
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
          Skip {activeSkip.kind.includes('ed') ? 'ending' : 'opening'}
        </button>
      )}

      <div
        className={cx(
          // The gradient itself passes taps through to the picture; on a phone
          // it covers half of it. Only the controls inside accept input.
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
          onSeek={(t) => video && (video.currentTime = t)}
        />

        <div className="mt-2 flex items-center gap-2 text-white">
          <IconButton label={playing ? 'Pause' : 'Play'} onClick={togglePlay}>
            {playing ? <PauseIcon /> : <PlayIcon />}
          </IconButton>

          <IconButton label="Back 10 seconds" onClick={() => seekBy(-10)}>
            <SeekIcon back />
          </IconButton>
          <IconButton label="Forward 10 seconds" onClick={() => seekBy(10)}>
            <SeekIcon />
          </IconButton>

          {onNext && (
            <IconButton label="Next episode" onClick={onNext}>
              <NextIcon />
            </IconButton>
          )}

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

          <span className="ml-1 text-xs tabular-nums text-white/80">
            {clockTime(time)} / {clockTime(duration)}
          </span>

          <div className="ml-auto flex items-center gap-1">
            <SpeedPicker value={rate} onChange={changeRate} />

            {(stream?.audio?.length ?? 0) > 1 && (
              <AudioPicker
                tracks={stream!.audio!}
                value={audioTrack}
                onChange={switchAudio}
              />
            )}

            {(stream?.subtitles?.length ?? 0) > 0 && (
              <SubtitlePicker
                tracks={stream!.subtitles}
                value={track}
                onChange={setTrack}
              />
            )}

            {pip.supported && (
              <IconButton label="Picture in picture" onClick={() => void pip.toggle()}>
                <PiPIcon />
              </IconButton>
            )}

            <IconButton
              label="Fullscreen"
              onClick={() => {
                if (document.fullscreenElement) void document.exitFullscreen()
                else void shell.current?.requestFullscreen()
              }}
            >
              <FullscreenIcon />
            </IconButton>
          </div>
        </div>
        </div>
      </div>

      <div
        className={cx(
          'pointer-events-none absolute inset-x-0 top-0 bg-gradient-to-b from-black/70 to-transparent p-4 transition-opacity duration-200',
          controlsVisible ? 'opacity-100' : 'opacity-0',
        )}
      >
        <p className="text-sm font-medium text-white">{title}</p>
        {subtitle && <p className="text-xs text-white/70">{subtitle}</p>}
      </div>

      {overlay}
    </div>
  )
}

/** What an opening or ending is called, for the marker's tooltip. */
function skipLabel(kind: string): string {
  const k = kind.toLowerCase()
  if (k.includes('op')) return 'Opening'
  if (k.includes('ed')) return 'Ending'
  if (k.includes('recap')) return 'Recap'
  return kind
}

function Scrubber({
  time,
  duration,
  skips,
  streamId,
  onSeek,
}: {
  time: number
  duration: number
  skips: SkipRange[]
  streamId?: string
  onSeek: (t: number) => void
}) {
  const sheet = useThumbnails(streamId)

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

  return (
    <div
      // touch-none: otherwise the page scrolls instead of the bar scrubbing.
      // Taller than the bar it draws, for fingers.
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
      <div className="absolute top-1/2 h-1 w-full -translate-y-1/2 overflow-hidden rounded-full bg-white/30">
        <div className="h-full bg-accent-500" style={{ width: `${percent}%` }} />
      </div>

      {/* Openings and endings in amber, so what is about to be skipped is
          visible before the button appears — and legible against both the
          filled and unfilled halves of the bar. */}
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

/**
 * The frame under the pointer, with its timestamp. The frames arrive as one
 * sprite sheet and this offsets into it, so scrubbing costs no requests at all.
 * Until the sheet exists it is the time alone, which is still the useful half.
 */
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
      <IconButton label={muted ? 'Unmute' : 'Mute'} onClick={onToggle}>
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
        className="w-0 accent-[var(--color-accent-500)] opacity-0 transition-all duration-200 group-hover/vol:w-20 group-hover/vol:opacity-100"
      />
    </div>
  )
}

/**
 * A native select is painted by the OS, so it comes out as a white menu over a
 * dark player no matter what CSS says. This is a plain menu instead.
 */
function SubtitlePicker({
  tracks,
  value,
  onChange,
}: {
  tracks: StreamInfo['subtitles']
  value: number | null
  onChange: (index: number | null) => void
}) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)

  // Language first, because that is what is being chosen. The release's own
  // name for the track only earns a place when it tells two of them apart —
  // Erai-raws calls every track "CR", so a list of titles is a list of "CR".
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
          className="absolute right-0 bottom-full z-50 mb-2 max-h-64 w-44 origin-bottom-right animate-rise overflow-y-auto rounded-lg border border-base-700 bg-base-850 py-1 shadow-xl shadow-black/60 scrollbar-thin"
        >
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
      )}
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
  const ref = useDismiss<HTMLDivElement>(close)

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

function SpeedPicker({ value, onChange }: { value: number; onChange: (rate: number) => void }) {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)

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
}: {
  video: HTMLVideoElement | null
  togglePlay: () => void
  seekBy: (n: number) => void
  nudge: () => void
  shell: HTMLElement | null
}) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement | null
      if (target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) return
      // A dialog or menu owns the keyboard while it is open; swallowing Space
      // here would stop its buttons from activating.
      if (target?.closest('[role=dialog], [role=menu], [role=listbox], [data-portal-menu]')) return

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
          break
        case 'ArrowDown':
          if (video) video.volume = Math.max(0, video.volume - 0.1)
          break
        case 'm':
          if (video) video.muted = !video.muted
          break
        case 'f':
          if (document.fullscreenElement) void document.exitFullscreen()
          else void shell?.requestFullscreen()
          break
        default:
          return
      }
      // Only for a key that did something.
      nudge()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [video, togglePlay, seekBy, nudge, shell])
}

function IconButton({
  label,
  onClick,
  children,
}: {
  label: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      className="grid size-9 place-items-center rounded-md text-white/90 transition-colors hover:bg-white/15 hover:text-white"
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
