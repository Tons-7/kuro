import { useEffect, useRef, useState, type RefObject } from 'react'
import type Hls from 'hls.js'
import type JASSUB from 'jassub'
// ?worker&url, not ?url: the plain form copies worker.js verbatim, leaving its
// relative imports pointing at paths that do not exist in a flat bundle.
import jassubWorkerUrl from './subtitle-worker?worker&url'
import jassubWasmUrl from 'jassub/dist/wasm/jassub-worker.wasm?url'
import jassubModernWasmUrl from 'jassub/dist/wasm/jassub-worker-modern.wasm?url'
import type { SkipRange, StreamInfo, SubtitleTrack } from '../lib/api'

/** Attaches an HLS stream with hls.js wherever it runs (Chrome's own HLS hides errors); native only without MSE. */
export function useHlsSource(
  video: HTMLVideoElement | null,
  playlist?: string,
  startAt = 0,
  onGone?: () => void,
) {
  const [error, setError] = useState<string | null>(null)
  const hls = useRef<Hls | null>(null)
  // Read at attach time, not tracked: a resume update mid-episode must not tear the player down.
  const startRef = useRef(startAt)
  startRef.current = startAt
  const goneRef = useRef(onGone)
  goneRef.current = onGone

  useEffect(() => {
    // Cleared before the early return too: the last episode's error must not sit over the next one's search.
    setError(null)
    if (!video || !playlist) return

    let cancelled = false
    let native = false
    let seek: (() => void) | undefined
    let recoveries = 0
    let recoveredAt = 0
    // Native HLS (iOS) reports failure only here.
    const onMediaError = () => {
      if (!native) return
      const code = video.error?.code
      setError(
        code === MediaError.MEDIA_ERR_DECODE || code === MediaError.MEDIA_ERR_SRC_NOT_SUPPORTED
          ? "This device can't decode this release."
          : 'Playback failed',
      )
    }
    video.addEventListener('error', onMediaError)

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
        // A plain src reload starts at zero unless seeked by hand.
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
        // The resume segment first, so the encoder started there isn't restarted from zero.
        startPosition: startRef.current > 0 ? startRef.current : -1,
        // A modest lookahead: asking far past one in-order encoder reads as a stall.
        maxBufferLength: 20,
        maxMaxBufferLength: 60,
        // No LL-HLS parts here; it only adds edge-seeking behaviour to a plain on-demand transcode.
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
          case HlsCtor.ErrorTypes.MEDIA_ERROR: {
            // A codec this browser cannot decode fails the same way forever;
            // recover twice (the second time swapping audio codec), then say so.
            const now = Date.now()
            recoveries = now - recoveredAt < 10_000 ? recoveries + 1 : 1
            recoveredAt = now
            if (recoveries > 2) {
              setError("This browser can't decode this release. Try mpv, or another release.")
              break
            }
            if (recoveries === 2) instance.swapAudioCodec()
            instance.recoverMediaError()
            break
          }
          default:
            // hls.js names like "manifestParsingError" mean nothing to a viewer.
            setError(
              /manifest|level/i.test(data.details ?? '')
                ? "The episode's stream could not be loaded."
                : 'Playback stopped unexpectedly.',
            )
        }
      })

      instance.loadSource(playlist)
      instance.attachMedia(video)
      hls.current = instance
    })()

    return () => {
      cancelled = true
      video.removeEventListener('error', onMediaError)
      hls.current?.destroy()
      hls.current = null
      if (native && seek) video.removeEventListener('loadedmetadata', seek)
      // Episodes share the element, emptied so the next source doesn't attach to a finished one.
      video.removeAttribute('src')
      video.load()
    }
  }, [video, playlist])

  return { error, hls }
}

/** Nudges a phone decoder that stops presenting frames after a seek, only while the page itself paints. */
export function useSeekRecovery(video: HTMLVideoElement | null, hls: RefObject<Hls | null>) {
  useEffect(() => {
    if (!video) return
    const request = video.requestVideoFrameCallback?.bind(video)
    if (!request) return

    // Frames and paints are counted only while a seek is being watched, not for the player's whole life.
    let frames = 0
    let painted = 0
    let videoHandle = 0
    let paintHandle = 0
    let watching = false
    let timer = 0
    let attempts = 0

    const watch = () => {
      if (watching) return
      watching = true
      videoHandle = request(function onVideoFrame() {
        frames++
        if (watching) videoHandle = request(onVideoFrame)
      })
      paintHandle = requestAnimationFrame(function onPaint() {
        painted++
        if (watching) paintHandle = requestAnimationFrame(onPaint)
      })
    }
    const unwatch = () => {
      watching = false
      window.clearInterval(timer)
      video.cancelVideoFrameCallback?.(videoHandle)
      cancelAnimationFrame(paintHandle)
    }

    const arm = () => {
      window.clearInterval(timer)
      watch()
      const framesAtSeek = frames
      const paintedAtSeek = painted
      const timeAtSeek = video.currentTime
      timer = window.setInterval(() => {
        if (frames > framesAtSeek) {
          attempts = 0
          unwatch()
          return
        }
        // Nothing is expected while paused, still buffering, or out of sight.
        if (video.paused || video.seeking || video.readyState < 3) return
        if (document.visibilityState !== 'visible') return
        // The page is not painting either, so the video is not the odd one out.
        if (painted - paintedAtSeek < 10) return
        if (video.currentTime - timeAtSeek < 3) return

        unwatch()
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
      unwatch()
      video.removeEventListener('seeked', arm)
    }
  }, [video, hls])
}

// Well under the server's ten-minute idle reaper, even with hidden-tab throttling.
const KEEPALIVE_EVERY = 45_000

/** Pings the playlist while playing (not paused) so the reaper keeps it; a 404 has onLost reopen it. */
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

/** Pokes a clock stopped with data buffered ahead (tiny seek, then recovery); no buffer is a slow encoder. */
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

export interface SubtitleLook {
  /** Dialogue size as a multiple of the file's own. */
  scale: number
  /** Seconds the subtitles show late; negative shows them early. */
  delay: number
}

type ASSStyle = Awaited<ReturnType<JASSUB['renderer']['getStyles']>>[number]

// Signs and songs are typeset to the picture; only dialogue is resized.
const TYPESET_STYLE = /sign|song|kara|title|logo|note|insert|\b(op|ed)\b/i

/** Renders ASS subtitles with JASSUB, as native tracks lose the styling and embedded fonts. */
export function useSubtitles(
  video: HTMLVideoElement | null,
  stream: StreamInfo | undefined,
  trackIndex: number | null,
  fonts: string[] = [],
  rebuild?: unknown,
  look: SubtitleLook = { scale: 1, delay: 0 },
) {
  const instance = useRef<JASSUB | null>(null)
  // The last instance's teardown, which the next one waits for.
  const destroying = useRef<Promise<unknown> | undefined>(undefined)
  const lookRef = useRef(look)
  lookRef.current = look
  // Set per renderer: re-applies the size, and redraws the current cue.
  const restyle = useRef<(() => Promise<void>) | null>(null)
  const repaint = useRef<(() => void) | null>(null)

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
      // Overlap left a dead renderer on screen; capped, as a teardown first waits out a renderer still starting.
      await Promise.race([destroying.current?.catch(() => {}), new Promise((done) => setTimeout(done, 250))])
      if (instance.current) await Promise.resolve(instance.current.destroy()).catch(() => {})
      instance.current = null
      if (!track) return

      // No dimensions yet, no canvas; the listeners above rerun this once they arrive.
      if (!video.videoWidth || !video.videoHeight) return

      const { default: Renderer } = await import('jassub')
      if (cancelled) return

      const renderer = new Renderer({
        video,
        subUrl: track.url,
        fonts,
        workerUrl: jassubWorkerUrl,
        wasmUrl: jassubWasmUrl,
        modernWasmUrl: jassubModernWasmUrl,
        // Subtitles are authored at 1080p; rendering above that is waste.
        prescaleFactor: 0.8,
        // Capped: uncapped, every resize step repaints, and dragging a window edge locked the machine.
        maxRenderHeight: 1080,
        timeOffset: -lookRef.current.delay,
      })
      instance.current = renderer

      // Without this a startup failure is silent: nothing is ever drawn.
      try {
        await renderer.ready
      } catch (err) {
        console.error('subtitle renderer failed to start:', err)
      }
      if (cancelled) return

      // Draw the cue the clock is on now; the next frame, paused, never comes.
      const resync = () => {
        if (document.visibilityState !== 'visible') return
        if (!video.videoWidth || !video.videoHeight) return
        // Caught: a call still in flight when the renderer is torn down rejects on its released worker.
        void renderer
          .manualRender(
            {
              mediaTime: video.currentTime,
              width: video.videoWidth,
              height: video.videoHeight,
              expectedDisplayTime: performance.now(),
            },
            true,
          )
          ?.catch(() => {})
      }
      document.addEventListener('visibilitychange', resync)
      // A paused seek may present no new frame, leaving the old cue up.
      video.addEventListener('seeked', resync)
      detach = () => {
        document.removeEventListener('visibilitychange', resync)
        video.removeEventListener('seeked', resync)
      }
      repaint.current = resync

      // Scaled from the file's own sizes, reread after each refresh so steps never compound.
      let originals: ASSStyle[] | null = null
      let applied = 1
      restyle.current = async () => {
        const scale = lookRef.current.scale
        if (scale === applied || cancelled) return
        const worker = renderer.renderer
        originals ??= await worker.getStyles()
        if (!originals.length) originals = null
        await Promise.all(
          (originals ?? []).map((s, i) =>
            TYPESET_STYLE.test(s.Name) ? undefined : worker.setStyle({ ...s, FontSize: s.FontSize * scale }, i),
          ),
        )
        applied = scale
        resync()
      }
      resync()
      void restyle.current().catch(() => {})

      void keepFilling(renderer, track.url, video, () => cancelled, () => {
        originals = null
        applied = 1
        void restyle.current?.().catch(() => {})
      }).catch(() => {})
    })()

    return () => {
      cancelled = true
      detach?.()
      restyle.current = null
      repaint.current = null
      destroying.current = instance.current ? Promise.resolve(instance.current.destroy()) : undefined
      instance.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [video, stream, trackIndex, fontKey, rebuild, surface])

  useEffect(() => {
    if (!instance.current) return
    instance.current.timeOffset = -look.delay
    repaint.current?.()
  }, [look.delay])

  useEffect(() => {
    void restyle.current?.().catch(() => {})
  }, [look.scale])
}

function cueCount(ass: string): number {
  return (ass.match(/^Dialogue:/gm) ?? []).length
}

// A gap larger than this is an unfilled hole in the download, not the silence between two lines of dialogue.
const CUE_GAP = 30

/** How far the cues cover contiguously from `from` seconds, not the furthest cue (holes). */
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
// The shortest gap between asks when new video keeps arriving.
const FILL_FRESH = 1_000
const FILL_EVERY = 15_000
const FILL_QUIET = 45_000
// How far past the playhead the track should reach before polling can relax.
const FILL_AHEAD = 120

/** Where the buffered video around the playhead ends. */
function bufferedEnd(video: HTMLVideoElement): number {
  const at = video.currentTime
  for (let i = 0; i < video.buffered.length; i++) {
    if (video.buffered.start(i) <= at + 0.5 && video.buffered.end(i) >= at) return video.buffered.end(i)
  }
  return at
}

// Polls wait here: on a paused or hidden tab they'd keep the session alive and hold the queue.
export async function untilWatching(video: HTMLVideoElement | null, cancelled: () => boolean) {
  while (!cancelled() && (document.visibilityState !== 'visible' || (video && video.paused))) {
    await new Promise((r) => setTimeout(r, 1000))
  }
}

/** Rereads a track while the source grows, counting cues, not reach (head and tail load first). */
async function keepFilling(
  // Behind a Comlink proxy, so setTrack is called as a method, or Comlink posts the proxy itself.
  renderer: { renderer?: { setTrack?: (content: string) => unknown } },
  url: string,
  video: HTMLVideoElement,
  cancelled: () => boolean,
  onTrack: () => void,
) {
  const worker = renderer.renderer
  if (!worker?.setTrack) return

  let cues = 0
  let loaded = ''
  let quiet = 0
  // How far the video was buffered at the last ask: the encoder writes a segment's lines with it.
  let askedUpTo = 0

  for (let attempt = 0; !cancelled(); attempt++) {
    // Poll sooner while just ahead of the playhead is uncovered, whatever a tail cue suggests.
    const near = coverageFrom(loaded, video.currentTime) - video.currentTime < FILL_AHEAD
    const wait = near ? (quiet >= 6 ? FILL_EVERY : FILL_SOON) : quiet >= 3 ? FILL_QUIET : FILL_EVERY
    const began = Date.now()
    // A seek lands somewhere with no lines yet, and new video brings new lines; ask then, not on the timer.
    await new Promise<void>((r) => {
      let soon = 0
      const done = () => {
        clearTimeout(timer)
        clearTimeout(soon)
        video.removeEventListener('seeked', done)
        video.removeEventListener('progress', grew)
        r()
      }
      const grew = () => {
        if (soon || bufferedEnd(video) < askedUpTo + 1) return
        soon = window.setTimeout(done, Math.max(0, FILL_FRESH - (Date.now() - began)))
      }
      const timer = setTimeout(done, wait)
      video.addEventListener('seeked', done, { once: true })
      video.addEventListener('progress', grew)
      grew()
    })
    askedUpTo = bufferedEnd(video)
    await untilWatching(video, cancelled)
    if (cancelled()) return

    try {
      // fill defeats the cache; at is the viewer's place, read directly while downloading.
      const res = await fetch(`${url}?fill=${attempt}&at=${Math.floor(video.currentTime)}`)
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
        onTrack()
      } else {
        quiet++
      }
      if (complete) return
    } catch {
      // A failed refresh just means trying again.
    }
  }
}

/** Fonts arrive after the stream opens; a new generation (rebuilt session) asks for them again. */
export function useEmbeddedFonts(fontsUrl?: string, generation = 0) {
  const [fonts, setFonts] = useState<string[]>([])
  const lastUrl = useRef(fontsUrl)

  useEffect(() => {
    // A fresh [] rebuilds the renderer, so only a new episode starts from none.
    if (lastUrl.current !== fontsUrl) setFonts((prev) => (prev.length === 0 ? prev : []))
    lastUrl.current = fontsUrl
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
          const next = body.fonts.map((f) => f.url)
          // Same list: keep the identity so the renderer is not rebuilt.
          setFonts((prev) => (prev.join('|') === next.join('|') ? prev : next))
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
  }, [fontsUrl, generation])

  return fonts
}

/** Auto-skip fires once per range; re-entering by seeking back is deliberate. */
/** What an AniSkip range is: op and mixed-op open, ed and mixed-ed end, recap is its own. */
export function skipKind(kind: string): 'opening' | 'ending' | 'recap' | undefined {
  const k = kind.toLowerCase()
  if (k === 'op' || k === 'mixed-op') return 'opening'
  if (k === 'ed' || k === 'mixed-ed') return 'ending'
  if (k === 'recap') return 'recap'
  return undefined
}

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
        // A recap has no toggle of its own, so it is offered, never skipped unasked.
        const kind = skipKind(range.kind)
        if (kind === 'ending' ? !enabled.ed : kind === 'opening' ? !enabled.op : true) continue

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

/** Floats the whole player (native PiP drops subtitles); container must be a portal host to keep its handlers. */
export function useDocumentPiP(container: HTMLElement | null) {
  const [active, setActive] = useState(false)
  const [win, setWin] = useState<Window | null>(null)
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
    // Not the on-page size: full width on a wide monitor opens a window partly off screen.
    const pip = await dpip.requestWindow(pipSize(container))

    // Styles are copied across, linked sheets relinked: inlined, their font URLs resolve against about:blank.
    for (const sheet of Array.from(document.styleSheets)) {
      if (sheet.href) {
        const link = pip.document.createElement('link')
        link.rel = 'stylesheet'
        link.href = sheet.href
        pip.document.head.append(link)
        continue
      }
      try {
        const style = pip.document.createElement('style')
        style.textContent = Array.from(sheet.cssRules)
          .map((rule) => rule.cssText)
          .join('')
        pip.document.head.append(style)
      } catch {
        // A cross-origin sheet cannot be read; nothing here depends on one.
      }
    }
    pip.document.title = document.title
    pip.document.body.style.margin = '0'
    pip.document.body.style.background = '#000'
    pip.document.documentElement.style.height = '100%'
    pip.document.body.style.height = '100%'

    // A placeholder holds the moved player's place, or the page collapses around the gap.
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
    container.style.height = '100%'
    pip.document.body.append(container)
    setActive(true)
    setWin(pip)

    pip.addEventListener('pagehide', () => {
      container.style.height = ''
      placeholder.replaceWith(container)
      setActive(false)
      setWin(null)
    })
  }

  // Leaving the page takes the player with it, so the floating window would be left holding a dead one.
  useEffect(() => {
    return () => {
      const w = (window as unknown as { documentPictureInPicture?: { window: Window | null } })
        .documentPictureInPicture?.window
      w?.close()
    }
  }, [])

  return { supported, active, toggle, window: win }
}

// Small enough to sit over other windows (a third of the screen at most), shaped like the video.
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

/** Asks for the preview sheet, polling while it is built; it needs the whole file. */
export function useThumbnails(streamId?: string, video?: HTMLVideoElement | null): Sheet | undefined {
  const [sheet, setSheet] = useState<Sheet | undefined>()

  useEffect(() => {
    setSheet(undefined)
    if (!streamId) return

    let timer = 0
    let cancelled = false

    let asked = false
    const ask = async () => {
      try {
        // The first answer may already be ready; only the re-polls wait.
        if (asked) await untilWatching(video ?? null, () => cancelled)
        asked = true
        if (cancelled) return
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
  }, [streamId, video])

  return sheet
}

/** Whether the upscaler's canvas holds the video's current frame, which a pause, seek or lost device spoils. */
export function useFreshFrames(video: HTMLVideoElement | null, enabled: boolean): boolean {
  const [fresh, setFresh] = useState(false)

  useEffect(() => {
    setFresh(false)
    if (!video || !enabled) return

    // Without it the canvas never claims to be current, costing upscaling, not the picture.
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

    // A seek leaves the canvas on the old frame, so it stands down until the renderer catches up.
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
