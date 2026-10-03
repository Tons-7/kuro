import { useEffect, useRef, useState } from 'react'
import type { Anime4KPipeline } from 'anime4k-webgpu'

/**
 * Anime4K in the browser via WebGPU; mode names match mpv so one setting drives
 * both. Best-effort: WebGPU may be absent, and every failure path plays plain.
 */
export function webgpuAvailable(): boolean {
  return typeof navigator !== 'undefined' && 'gpu' in navigator
}

/** How much network runs per frame: a sharpen filter, the medium networks, or the very large ones. */
export type UpscaleTier = 'light' | 'balanced' | 'full'
/** This device's say over the shared setting. Auto picks for it and steps down when frames run late. */
export type DeviceUpscale = 'auto' | 'off' | UpscaleTier
/** Where Auto settled on this device after stepping down. */
export type AutoTier = UpscaleTier | 'off'

const DEVICE_KEY = 'kuro.anime4k'
const AUTO_KEY = 'kuro.anime4k.auto'
const TIERS: readonly string[] = ['light', 'balanced', 'full']

const read = (key: string) => {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}
const write = (key: string, value: string | null) => {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    // Private mode: the choice lasts for the page.
  }
}

export function deviceUpscale(): DeviceUpscale {
  const v = read(DEVICE_KEY)
  // "on" is what the first per-device version stored.
  if (v === 'on') return 'full'
  return v === 'off' || TIERS.includes(v ?? '') ? (v as DeviceUpscale) : 'auto'
}
export const setDeviceUpscale = (choice: DeviceUpscale) => write(DEVICE_KEY, choice === 'auto' ? null : choice)

export function autoTier(): AutoTier | null {
  const v = read(AUTO_KEY)
  return v === 'off' || TIERS.includes(v ?? '') ? (v as AutoTier) : null
}
export const setAutoTier = (tier: AutoTier | null) => write(AUTO_KEY, tier)

/** One step down from tier; past light it is off. */
export const lowerTier = (tier: UpscaleTier): AutoTier => (tier === 'full' ? 'balanced' : tier === 'balanced' ? 'light' : 'off')

const touchDevice = () => typeof matchMedia === 'function' && matchMedia('(hover: none) and (pointer: coarse)').matches

/** Auto follows the shared setting on a computer and is off on phones; a tier picked here always applies. */
export function upscaleHere(choice: DeviceUpscale, shared: boolean, settled: AutoTier | null) {
  const fallback: UpscaleTier = touchDevice() ? 'light' : 'full'
  if (choice === 'off') return { enabled: false, tier: fallback, auto: false }
  if (choice !== 'auto') return { enabled: true, tier: choice, auto: false }
  const tier = settled && settled !== 'off' ? settled : fallback
  return { enabled: shared && !touchDevice() && settled !== 'off', tier, auto: true }
}

type Mode = 'A' | 'B' | 'C' | 'A+A' | 'B+B' | 'C+A'
type Size = { width: number; height: number }
type Lib = typeof import('anime4k-webgpu')

/** The networks for a mode at a tier. Full is the library's own preset; the lighter chains are built here. */
function chain(lib: Lib, device: GPUDevice, input: GPUTexture, native: Size, target: Size, mode: Mode, tier: UpscaleTier) {
  const out: Anime4KPipeline[] = []
  let texture = input
  const add = (p: Anime4KPipeline) => {
    out.push(p)
    texture = p.getOutputTexture()
  }
  const here = () => ({ device, inputTexture: texture })

  // A screen smaller than the video: work at the size shown, not the size stored.
  let size = native
  if (target.width < native.width * 0.85) {
    add(new lib.Downscale({ ...here(), targetDimensions: target }))
    size = target
  }

  if (tier === 'full') {
    const presets = { A: lib.ModeA, B: lib.ModeB, C: lib.ModeC, 'A+A': lib.ModeAA, 'B+B': lib.ModeBB, 'C+A': lib.ModeCA }
    add(new presets[mode]({ ...here(), nativeDimensions: size, targetDimensions: target }))
    return out
  }

  const grows = target.width > size.width * 1.2 && target.height > size.height * 1.2
  const upscale = () => {
    add(new lib.CNNx2M(here()))
    if (target.width < size.width * 2) add(new lib.Downscale({ ...here(), targetDimensions: target }))
  }

  if (tier === 'light') {
    add(new lib.DoG(here()))
    if (grows) upscale()
    return out
  }

  // Balanced: the medium networks. Mode C's denoise only exists very large, so C upscales without it.
  const Restore = mode.startsWith('B') ? lib.CNNSoftM : lib.CNNM
  add(new lib.ClampHighlights(here()))
  if (mode !== 'C' && mode !== 'C+A') add(new Restore(here()))
  if (grows) upscale()
  if (mode === 'A+A' || mode === 'B+B') add(new Restore(here()))
  if (mode === 'C+A') add(new lib.CNNM(here()))
  return out
}

// A 24 fps frame lasts 42 ms; a GPU that needs more than this each frame shows them late.
const SLOW_FRAME_MS = 36
const SLOW_WINDOW = 72

export type UpscaleState = 'off' | 'starting' | 'on' | 'unsupported' | 'failed'

export function useAnime4K({
  video,
  canvas,
  enabled,
  mode,
  tier,
  source,
  onSlow,
}: {
  video: HTMLVideoElement | null
  canvas: HTMLCanvasElement | null
  enabled: boolean
  mode: string
  tier: UpscaleTier
  /** Changes per source: the renderer sizes its textures from the first frame
   *  it sees, so a different release has to rebuild it. */
  source?: string
  /** The GPU cannot keep up at this tier; unset when the tier was picked by hand. */
  onSlow?: () => void
}) {
  const [state, setState] = useState<UpscaleState>('off')
  const slowRef = useRef(onSlow)
  slowRef.current = onSlow

  // Fullscreen transitions replace the compositing surface the canvas was
  // configured against, so the old renderer paints black over the video.
  const [generation, setGeneration] = useState(0)
  useEffect(() => {
    const restart = () => setGeneration((n) => n + 1)
    document.addEventListener('fullscreenchange', restart)
    return () => document.removeEventListener('fullscreenchange', restart)
  }, [])

  useEffect(() => {
    if (!enabled) {
      setState('off')
      return
    }
    if (!webgpuAvailable()) {
      setState('unsupported')
      return
    }
    if (!video || !canvas) return

    let cancelled = false
    let device: GPUDevice | undefined
    setState('starting')

    const start = async () => {
      // The renderer needs real frame dimensions to size its textures, which
      // only exist once metadata has loaded.
      if (!video.videoWidth) {
        await new Promise<void>((resolve) => {
          video.addEventListener('loadedmetadata', () => resolve(), { once: true })
        })
      }
      if (cancelled) return

      const lib = await import('anime4k-webgpu')
      if (cancelled) return

      // Up to double, never past the screen, and in the video's own shape.
      const native = { width: video.videoWidth, height: video.videoHeight }
      const fit = Math.min(
        (window.screen.width * devicePixelRatio) / native.width,
        (window.screen.height * devicePixelRatio) / native.height,
      )
      const scale = Math.min(2, fit)
      const target = { width: Math.round(native.width * scale) & ~1, height: Math.round(native.height * scale) & ~1 }
      canvas.width = target.width
      canvas.height = target.height

      const key = mode?.toUpperCase() as Mode
      const picked: Mode = ['A', 'B', 'C', 'A+A', 'B+B', 'C+A'].includes(key) ? key : 'A'

      await lib.render({
        video,
        canvas,
        // The renderer returns no handle to stop its frame loop, so capture the
        // device here and destroy it on teardown.
        pipelineBuilder: (gpu: GPUDevice, inputTexture: GPUTexture) => {
          device = gpu

          // A device can also be lost on its own (driver reset, backgrounded
          // too long); report it so the plain video comes back.
          void gpu.lost.then(() => {
            if (!cancelled) setState('failed')
          })

          const pipelines = chain(lib, gpu, inputTexture, native, target, picked, tier)
          timeFrames(gpu, pipelines[pipelines.length - 1], () => !cancelled && slowRef.current?.())
          return pipelines as [...Anime4KPipeline[], Anime4KPipeline]
        },
      })

      if (cancelled) {
        // Started while tearing down: stop it rather than leaving it running.
        device?.destroy()
        return
      }
      setState('on')
    }

    void start().catch((err) => {
      if (cancelled) return
      console.warn('anime4k unavailable, playing without it:', err)
      setState('failed')
    })

    return () => {
      cancelled = true
      // Otherwise the frame loop and its device leak on every restart until the GPU gives up.
      device?.destroy()
      device = undefined
    }
  }, [video, canvas, enabled, mode, tier, generation, source])

  return state
}

/** Times each frame through the GPU; slow fires once when a window's median is over the budget. */
function timeFrames(device: GPUDevice, last: Anime4KPipeline, slow: () => void) {
  const pass = last.pass.bind(last)
  let times: number[] = []
  let fired = false
  last.pass = (encoder) => {
    pass(encoder)
    if (fired) return
    const began = performance.now()
    // After the renderer submits this frame, which it does in this same task.
    queueMicrotask(() => {
      void device.queue.onSubmittedWorkDone().then(() => {
        times.push(performance.now() - began)
        if (times.length < SLOW_WINDOW) return
        const median = times.sort((a, b) => a - b)[times.length >> 1]
        times = []
        if (median > SLOW_FRAME_MS) {
          fired = true
          slow()
        }
      }, () => {})
    })
  }
}
