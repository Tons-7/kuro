import { useEffect, useRef, useState, type RefObject } from 'react'
import type Hls from 'hls.js'
// ?worker&url, not ?url: the plain form copies worker.js verbatim, leaving its
// relative imports pointing at paths that do not exist in a flat bundle.
import jassubWorkerUrl from 'jassub/dist/worker/worker.js?worker&url'
import jassubWasmUrl from 'jassub/dist/wasm/jassub-worker.wasm?url'
import jassubModernWasmUrl from 'jassub/dist/wasm/jassub-worker-modern.wasm?url'
import type { SkipRange, StreamInfo, SubtitleTrack } from '../lib/api'

/**
 * Attaches an HLS stream. hls.js wins whenever it can run: recent Chrome also
 * answers canPlayType for HLS (its own built-in player), but that player hides
 * every error and ignores resume positions. The native path is only for
 * browsers without MSE (iOS Safari), which really cannot run hls.js.
 */
export function useHlsSource(
  video: HTMLVideoElement | null,
  playlist?: string,
  startAt = 0,
  onGone?: () => void,
) {
  const [error, setError] = useState<string | null>(null)
  const hls = useRef<Hls | null>(null)
  // Read at attach time, not tracked: a resume update mid-episode must not tear
  // the player down.
  const startRef = useRef(startAt)
  startRef.current = startAt
  const goneRef = useRef(onGone)
  goneRef.current = onGone

  useEffect(() => {
    if (!video || !playlist) return
    setError(null)

    let cancelled = false
    let native = false
    let seek: (() => void) | undefined

    void (async () => {
      const { default: HlsCtor } = await import('hls.js')
      if (cancelled) return

      if (!HlsCtor.isSupported()) {
        if (!video.canPlayType('application/vnd.apple.mpegurl')) {
          setError('This browser cannot play HLS.')
          return
        }
        native = true
        video.src = playlist
        // A plain src reload (resume, audio switch, session recovery) starts
        // at zero unless the element is seeked by hand.
        const at = startRef.current
        if (at > 0) {
          seek = () => {
            if (Math.abs(video.currentTime - at) > 2) video.currentTime = at
          }
          video.addEventListener('loadedmetadata', seek, { once: true })
        }
        return
      }

      const instance = new HlsCtor({
        // Load the resume segment first so the encoder, started at that same
        // point, is not killed and restarted from zero a moment later.
        startPosition: startRef.current > 0 ? startRef.current : -1,
        // A single ffmpeg produces segments in order; a big buffer goal makes
        // the player demand segments far ahead of the encoder, which on a slow
        // transcode reads as a stall. Keep the lookahead modest.
        maxBufferLength: 20,
        maxMaxBufferLength: 60,
        // No LL-HLS parts here; it only adds edge-seeking behaviour to a plain
        // on-demand transcode.
        lowLatencyMode: false,
        manifestLoadingMaxRetry: 8,
        manifestLoadingRetryDelay: 800,
        levelLoadingMaxRetry: 8,
        fragLoadingMaxRetry: 10,
      })

      instance.on(HlsCtor.Events.ERROR, (_event, data) => {
        if (!data.fatal) return
        switch (data.type) {
          case HlsCtor.ErrorTypes.NETWORK_ERROR:
            // 404 means the session was reaped, not a flaky network; retrying
            // can never succeed. Stop and let the reopen rebuild the player.
            if (data.response?.code === 404) {
              instance.stopLoad()
              goneRef.current?.()
            } else {
              instance.startLoad()
            }
            break
          case HlsCtor.ErrorTypes.MEDIA_ERROR:
            instance.recoverMediaError()
            break
          default:
            setError(data.details ?? 'Playback failed')
        }
      })

      instance.loadSource(playlist)
      instance.attachMedia(video)
      hls.current = instance
    })()

    return () => {
      cancelled = true
      hls.current?.destroy()
      hls.current = null
      if (native && seek) video.removeEventListener('loadedmetadata', seek)
      // Episodes share the element now, so empty it: the next source would
      // attach to a media element still sitting finished at the end.
      video.removeAttribute('src')
      video.load()
    }
  }, [video, playlist])

  return { error, hls }
}

/**
 * After a seek, some phone decoders keep audio going but never present another
 * frame. Nudge the pipeline (tiny seek, then media-error recovery) — but only
 * when animation frames are arriving and video frames are not, so a hidden or
 * throttled tab isn't mistaken for a stuck decoder.
 */
export function useSeekRecovery(video: HTMLVideoElement | null, hls: RefObject<Hls | null>) {
  useEffect(() => {
    if (!video) return
    const request = video.requestVideoFrameCallback?.bind(video)
    if (!request) return

    let frames = 0
    let painted = 0
    let videoHandle = request(function onVideoFrame() {
      frames++
      videoHandle = request(onVideoFrame)
    })
    let paintHandle = requestAnimationFrame(function onPaint() {
      painted++
      paintHandle = requestAnimationFrame(onPaint)
    })

    let timer = 0
    let attempts = 0

    const arm = () => {
      window.clearInterval(timer)
      const framesAtSeek = frames
      const paintedAtSeek = painted
      const timeAtSeek = video.currentTime
      timer = window.setInterval(() => {
        if (frames > framesAtSeek) {
          attempts = 0
          window.clearInterval(timer)
          return
        }
        // Nothing is expected while paused, still buffering, or out of sight.
        if (video.paused || video.seeking || video.readyState < 3) return
        if (document.visibilityState !== 'visible') return
        // The page is not painting either, so the video is not the odd one out.
        if (painted - paintedAtSeek < 10) return
        if (video.currentTime - timeAtSeek < 3) return

        window.clearInterval(timer)
        attempts++
        if (attempts === 1) {
          video.currentTime += 0.01
        } else if (attempts === 2) {
          hls.current?.recoverMediaError()
        }
      }, 1000)
    }

    video.addEventListener('seeked', arm)
    return () => {
      window.clearInterval(timer)
      video.cancelVideoFrameCallback?.(videoHandle)
      cancelAnimationFrame(paintHandle)
      video.removeEventListener('seeked', arm)
    }
  }, [video, hls])
}

// Well under the server's ten-minute idle reaper, even with hidden-tab throttling.
const KEEPALIVE_EVERY = 45_000

/**
 * A backgrounded tab stops requesting segments and the server reaps the
 * session after ten minutes; every request then 404s for ever. Ping the
 * playlist while playing to keep it alive; a 404 means it is gone and onLost
 * reopens it. No pings while paused, so an abandoned tab still frees its
 * encoder — the visibility/play pings recover on return.
 */
export function useKeepAlive(
  video: HTMLVideoElement | null,
  playlist?: string,
  onLost?: () => void,
) {
  const lostRef = useRef(onLost)
  lostRef.current = onLost

  useEffect(() => {
    if (!video || !playlist) return
    let cancelled = false

    const ping = async () => {
      try {
        const res = await fetch(playlist, { credentials: 'same-origin' })
        if (!cancelled && res.status === 404) lostRef.current?.()
      } catch {
        // Offline or mid-restart; the next ping tries again.
      }
    }
    const kick = () => void ping()

    const id = window.setInterval(() => {
      if (!video.paused && !video.ended) void ping()
    }, KEEPALIVE_EVERY)
    const onVisible = () => {
      if (document.visibilityState === 'visible') void ping()
    }
    document.addEventListener('visibilitychange', onVisible)
    video.addEventListener('play', kick)

    return () => {
      cancelled = true
      window.clearInterval(id)
      document.removeEventListener('visibilitychange', onVisible)
      video.removeEventListener('play', kick)
    }
  }, [video, playlist])
}

/**
 * A tab back from the background can return with the clock stopped despite
 * data buffered ahead, until the user pauses and unpauses by hand. Do that
 * poke for them: a tiny seek, then media-error recovery. A stopped clock with
 * nothing buffered is a slow encoder, not a wedge, and is left alone.
 */
export function useStallWatchdog(video: HTMLVideoElement | null, hls: RefObject<Hls | null>) {
  useEffect(() => {
    if (!video) return

    let last = -1
    let stalledFor = 0
    let attempts = 0

    const id = window.setInterval(() => {
      const t = video.currentTime
      if (
        document.visibilityState !== 'visible' ||
        video.paused || video.ended || video.seeking || t !== last
      ) {
        if (t !== last) attempts = 0
        last = t
        stalledFor = 0
        return
      }
      stalledFor++

      let ahead = false
      for (let i = 0; i < video.buffered.length; i++) {
        if (video.buffered.start(i) <= t + 0.5 && video.buffered.end(i) > t + 1) ahead = true
      }
      if (!ahead || stalledFor < 4 || attempts >= 2) return

      stalledFor = 0
      attempts++
      if (attempts === 1) video.currentTime = t + 0.05
      else hls.current?.recoverMediaError()
    }, 1000)

    return () => window.clearInterval(id)
  }, [video, hls])
}

/**
 * Renders ASS subtitles with JASSUB. Native tracks cannot express the styling
 * anime subtitles rely on, and the embedded fonts have to be handed over.
 */
export function useSubtitles(
  video: HTMLVideoElement | null,
  stream: StreamInfo | undefined,
  trackIndex: number | null,
  fonts: string[] = [],
  rebuild?: unknown,
) {
  const instance = useRef<{ destroy: () => void | Promise<void> } | null>(null)

  // Identity, not contents, is what re-ran this effect.
  const fontKey = fonts.join('|')

  // A fullscreen transition leaves the canvas opaque over the video: black
  // picture, controls and audio fine. It belongs to a worker, so rebuild it.
  const [surface, setSurface] = useState(0)
  useEffect(() => {
    const rebuild = () => setSurface((n) => n + 1)
    document.addEventListener('fullscreenchange', rebuild)
    return () => document.removeEventListener('fullscreenchange', rebuild)
  }, [])

  // Same opaque canvas at startup: the renderer sizes against the video, so
  // attaching before dimensions exist leaves it covering the picture. Rebuild.
  useEffect(() => {
    if (!video) return
    const rebuild = () => setSurface((n) => n + 1)
    video.addEventListener('loadedmetadata', rebuild)
    video.addEventListener('resize', rebuild)
    return () => {
      video.removeEventListener('loadedmetadata', rebuild)
      video.removeEventListener('resize', rebuild)
    }
  }, [video])

  useEffect(() => {
    if (!video || !stream) return

    const track: SubtitleTrack | undefined =
      trackIndex === null ? undefined : stream.subtitles?.find((t) => t.index === trackIndex)

    let cancelled = false
    let detach: (() => void) | undefined

    void (async () => {
      // Awaited: overlapping destroy with the next instance left a dead
      // renderer on screen.
      await instance.current?.destroy()
      instance.current = null
      if (!track) return

      // No dimensions yet means no canvas worth building; the listeners above
      // run this again the moment they arrive.
      if (!video.videoWidth || !video.videoHeight) return

      const { default: JASSUB } = await import('jassub')
      if (cancelled) return

      const renderer = new JASSUB({
        video,
        subUrl: track.url,
        fonts,
        workerUrl: jassubWorkerUrl,
        wasmUrl: jassubWasmUrl,
        modernWasmUrl: jassubModernWasmUrl,
        // Subtitles are authored at 1080p; rendering above that is waste.
        prescaleFactor: 0.8,
        // Uncapped by default, and it repaints on every step of a resize —
        // dragging a window edge was enough to lock up the machine.
        maxRenderHeight: 1080,
      })
      instance.current = renderer

      // Without this a startup failure is silent: nothing is ever drawn.
      try {
        await renderer.ready
      } catch (err) {
        console.error('subtitle renderer failed to start:', err)
      }
      if (cancelled) return

      // Subtitles are drawn per presented frame, so a tab that was away has the
      // cue it left on. Draw the one the clock is on now rather than waiting for
      // the next frame — which, paused, never comes.
      const resync = () => {
        if (document.visibilityState !== 'visible') return
        if (!video.videoWidth || !video.videoHeight) return
        void renderer.manualRender(
          {
            mediaTime: video.currentTime,
            width: video.videoWidth,
            height: video.videoHeight,
            expectedDisplayTime: performance.now(),
          },
          true,
        )
      }
      document.addEventListener('visibilitychange', resync)
      detach = () => document.removeEventListener('visibilitychange', resync)

      void keepFilling(renderer, track.url, video, () => cancelled)
    })()

    return () => {
      cancelled = true
      detach?.()
      void instance.current?.destroy()
      instance.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [video, stream, trackIndex, fontKey, rebuild, surface])
}

function cueCount(ass: string): number {
  return (ass.match(/^Dialogue:/gm) ?? []).length
}

// A gap larger than this is an unfilled hole in the download, not the silence
// between two lines of dialogue.
const CUE_GAP = 30

/**
 * How far the loaded cues cover contiguously from `from` seconds — not the
 * furthest cue, since head and tail load first and can leave a hole between.
 */
export function coverageFrom(ass: string, from: number): number {
  const cues: Array<[number, number]> = []
  for (const m of ass.matchAll(
    /^Dialogue:\s*[^,]*,(\d+):(\d+):(\d+(?:\.\d+)?),(\d+):(\d+):(\d+(?:\.\d+)?)/gm,
  )) {
    const start = Number(m[1]) * 3600 + Number(m[2]) * 60 + Number(m[3])
    const end = Number(m[4]) * 3600 + Number(m[5]) * 60 + Number(m[6])
    cues.push([start, end])
  }
  cues.sort((a, b) => a[0] - b[0])

  let reach = from
  for (const [start, end] of cues) {
    if (start > reach + CUE_GAP) break
    if (end > reach) reach = end
  }
  return reach
}

const FILL_SOON = 5_000
const FILL_EVERY = 15_000
const FILL_QUIET = 45_000
// How far past the playhead the track should reach before polling can relax.
const FILL_AHEAD = 120

/**
 * A track reads only as far as the download reached, so keep asking while the
 * source can still grow (the server says when it can't). Growth is counted in
 * cues, not reach, since head and tail load first and can leave a hole between.
 */
async function keepFilling(
  // Lives behind a Comlink proxy; setTrack must be called as a method on it, or
  // Comlink tries to post the un-cloneable proxy itself.
  renderer: { renderer?: { setTrack?: (content: string) => unknown } },
  url: string,
  video: HTMLVideoElement,
  cancelled: () => boolean,
) {
  const worker = renderer.renderer
  if (!worker?.setTrack) return

  let cues = 0
  let loaded = ''
  let quiet = 0

  for (let attempt = 0; !cancelled(); attempt++) {
    // Poll sooner while the region just ahead of the playhead is not yet
    // covered, whatever a far-off tail cue suggests.
    const near = coverageFrom(loaded, video.currentTime) - video.currentTime < FILL_AHEAD
    const wait = near ? (quiet >= 6 ? FILL_EVERY : FILL_SOON) : quiet >= 3 ? FILL_QUIET : FILL_EVERY
    await new Promise((r) => setTimeout(r, wait))
    if (cancelled()) return

    try {
      // The query only defeats the browser cache; the server ignores it.
      const res = await fetch(`${url}?fill=${attempt}`)
      // The session is gone; so is the player, shortly.
      if (res.status === 404) return
      if (!res.ok) continue

      const complete = res.headers.get('X-Kuro-Complete') === 'true'
      const text = await res.text()
      const got = cueCount(text)
      if (got > cues) {
        cues = got
        loaded = text
        quiet = 0
        await worker.setTrack(text)
      } else {
        quiet++
      }
      if (complete) return
    } catch {
      // A failed refresh just means trying again.
    }
  }
}

/**
 * Fonts are dumped after the stream opens; doing it first delayed the episode
 * by twenty seconds. Until they land the renderer substitutes.
 */
export function useEmbeddedFonts(fontsUrl?: string) {
  const [fonts, setFonts] = useState<string[]>([])

  useEffect(() => {
    // A fresh [] is a new identity, which rebuilds the subtitle renderer.
    setFonts((prev) => (prev.length === 0 ? prev : []))
    if (!fontsUrl) return

    let cancelled = false
    let attempts = 0

    const poll = async () => {
      if (cancelled || attempts++ > 40) return
      try {
        const res = await fetch(fontsUrl, { credentials: 'same-origin' })
        const body = (await res.json()) as {
          ready: boolean
          fonts: Array<{ url: string }>
        }
        if (cancelled) return
        if (body.ready) {
          setFonts(body.fonts.map((f) => f.url))
          return
        }
      } catch {
        // A failed poll is not worth surfacing; the default font is readable.
      }
      window.setTimeout(poll, 2000)
    }

    void poll()
    return () => {
      cancelled = true
    }
  }, [fontsUrl])

  return fonts
}

/** Auto-skip fires once per range; re-entering by seeking back is deliberate. */
export function useAutoSkip(
  video: HTMLVideoElement | null,
  ranges: SkipRange[],
  enabled: { op: boolean; ed: boolean },
) {
  const skipped = useRef<Set<string>>(new Set())

  useEffect(() => {
    skipped.current.clear()
  }, [ranges])

  useEffect(() => {
    if (!video || ranges.length === 0) return

    const onTime = () => {
      const t = video.currentTime
      for (const range of ranges) {
        // A shape change upstream must not throw inside timeupdate.
        if (!range?.kind) continue
        const isEnding = range.kind.includes('ed')
        if (isEnding ? !enabled.ed : !enabled.op) continue

        const key = `${range.kind}-${range.start}`
        if (skipped.current.has(key)) continue
        if (t >= range.start && t < range.end - 0.4) {
          skipped.current.add(key)
          video.currentTime = range.end
          return
        }
      }
    }

    video.addEventListener('timeupdate', onTime)
    return () => video.removeEventListener('timeupdate', onTime)
  }, [video, ranges, enabled.op, enabled.ed])
}

/**
 * Moves the whole player into a floating window. Native video PiP carries only
 * the video frames, so it would drop the subtitle canvas.
 */
export function useDocumentPiP(container: HTMLElement | null) {
  const [active, setActive] = useState(false)
  const supported =
    typeof window !== 'undefined' && 'documentPictureInPicture' in window

  const toggle = async () => {
    if (!supported || !container) return

    const dpip = (window as unknown as {
      documentPictureInPicture: {
        window: Window | null
        requestWindow: (o: { width: number; height: number }) => Promise<Window>
      }
    }).documentPictureInPicture

    if (dpip.window) {
      dpip.window.close()
      setActive(false)
      return
    }

    const rect = container.getBoundingClientRect()
    // Not the on-page size: full width on a wide monitor asks for a window as
    // large as the screen, which opens partly off it.
    const pip = await dpip.requestWindow(pipSize(container))

    // The floating window starts blank, so styles have to be copied across.
    for (const sheet of Array.from(document.styleSheets)) {
      try {
        const css = Array.from(sheet.cssRules)
          .map((rule) => rule.cssText)
          .join('')
        const style = pip.document.createElement('style')
        style.textContent = css
        pip.document.head.append(style)
      } catch {
        // A cross-origin sheet cannot be read; nothing here depends on one.
      }
    }
    pip.document.body.style.margin = '0'
    pip.document.body.style.background = '#000'

    // The element is moved rather than copied, so without something holding its
    // place the player vanishes and the page collapses around the gap. Every
    // site that does this leaves the box behind saying where the video went.
    const placeholder = document.createElement('div')
    placeholder.style.cssText = [
      `height:${Math.round(rect.height)}px`,
      'display:grid',
      'place-items:center',
      'background:#000',
      'border-radius:0.75rem',
      'color:#8891a5',
      'font-size:0.875rem',
    ].join(';')
    placeholder.textContent = 'Playing in picture in picture'

    container.replaceWith(placeholder)
    pip.document.body.append(container)
    setActive(true)

    pip.addEventListener('pagehide', () => {
      placeholder.replaceWith(container)
      setActive(false)
    })
  }

  // Leaving the page takes the player with it, so the floating window would be
  // left holding a dead one.
  useEffect(() => {
    return () => {
      const w = (window as unknown as { documentPictureInPicture?: { window: Window | null } })
        .documentPictureInPicture?.window
      w?.close()
    }
  }, [])

  return { supported, active, toggle }
}

// Small enough to sit over other windows, never wider than a third of the
// screen, and shaped like the video rather than the page.
const pipMinWidth = 320
const pipMaxWidth = 800

function pipSize(container: HTMLElement) {
  const video = container.querySelector('video')
  const ratio =
    video && video.videoWidth > 0 && video.videoHeight > 0
      ? video.videoWidth / video.videoHeight
      : 16 / 9

  const available = window.screen?.availWidth || window.innerWidth || 1280
  const width = Math.round(
    Math.min(pipMaxWidth, Math.max(pipMinWidth, available * 0.35)),
  )
  return { width, height: Math.round(width / ratio) }
}

/** The scrub preview sheet, once ffmpeg has finished laying it out. */
export interface Sheet {
  ready: boolean
  interval: number
  columns: number
  rows: number
  count: number
  width: number
  height: number
  url: string
}

/**
 * Asks for the preview sheet, and keeps asking while it is being built. The
 * frames are sampled across the whole episode, so it cannot exist until the
 * file does — a few polls, then it either appears or it never will.
 */
export function useThumbnails(streamId?: string): Sheet | undefined {
  const [sheet, setSheet] = useState<Sheet | undefined>()

  useEffect(() => {
    setSheet(undefined)
    if (!streamId) return

    let timer = 0
    let cancelled = false

    const ask = async () => {
      try {
        const res = await fetch(`/api/stream/${streamId}/thumbnails`)
        if (!res.ok || cancelled) return
        const body = (await res.json()) as Omit<Sheet, 'url'>
        if (cancelled || !body.interval) return

        if (body.ready) {
          setSheet({ ...body, url: `/api/stream/${streamId}/thumbs.jpg` })
          return
        }
        // The sheet is only built once the file is whole, which for a torrent
        // can be well into the episode; keep asking for as long as it plays.
        timer = window.setTimeout(ask, 15_000)
      } catch {
        // A preview is a nicety; failing to get one is not worth reporting.
      }
    }
    void ask()

    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [streamId])

  return sheet
}

/**
 * Whether the upscaler's canvas holds the frame the video is actually on. It
 * draws on `requestVideoFrameCallback`, so a pause, a seek or a lost device
 * leaves it stale or blank.
 */
export function useFreshFrames(video: HTMLVideoElement | null, enabled: boolean): boolean {
  const [fresh, setFresh] = useState(false)

  useEffect(() => {
    setFresh(false)
    if (!video || !enabled) return

    // Not every engine has it; without it the canvas simply never claims to be
    // current, which costs upscaling rather than the picture.
    const request = video.requestVideoFrameCallback?.bind(video)
    if (!request) return

    let handle = 0
    let cancelled = false

    const onFrame = () => {
      if (cancelled) return
      setFresh(true)
      handle = request(onFrame)
    }
    handle = request(onFrame)

    // A seek leaves the canvas on the frame it drew before the jump, so it has
    // to stand down until the renderer catches up.
    const stale = () => setFresh(false)
    video.addEventListener('seeking', stale)
    video.addEventListener('emptied', stale)

    return () => {
      cancelled = true
      video.cancelVideoFrameCallback?.(handle)
      video.removeEventListener('seeking', stale)
      video.removeEventListener('emptied', stale)
    }
  }, [video, enabled])

  return fresh
}
