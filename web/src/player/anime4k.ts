import { useEffect, useState } from 'react'

/**
 * Anime4K in the browser via WebGPU; mode names match mpv so one setting drives
 * both. Best-effort: WebGPU may be absent, and every failure path plays plain.
 */
export function webgpuAvailable(): boolean {
  return typeof navigator !== 'undefined' && 'gpu' in navigator
}

type Preset = 'A' | 'B' | 'C' | 'A+A' | 'B+B' | 'C+A'

async function presetFor(mode: string) {
  const lib = await import('anime4k-webgpu')
  const presets: Record<Preset, unknown> = {
    A: lib.ModeA,
    B: lib.ModeB,
    C: lib.ModeC,
    'A+A': lib.ModeAA,
    'B+B': lib.ModeBB,
    'C+A': lib.ModeCA,
  }
  const key = (mode?.toUpperCase() as Preset) || 'A'
  return (presets[key] ?? lib.ModeA) as new (opts: unknown) => unknown
}

export type UpscaleState = 'off' | 'starting' | 'on' | 'unsupported' | 'failed'

export function useAnime4K({
  video,
  canvas,
  enabled,
  mode,
  source,
}: {
  video: HTMLVideoElement | null
  canvas: HTMLCanvasElement | null
  enabled: boolean
  mode: string
  /** Changes per source: the renderer sizes its textures from the first frame
   *  it sees, so a different release has to rebuild it. */
  source?: string
}) {
  const [state, setState] = useState<UpscaleState>('off')

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

      const [{ render }, Preset] = await Promise.all([
        import('anime4k-webgpu'),
        presetFor(mode),
      ])
      if (cancelled) return

      // Upscaling past the display resolution costs GPU time nobody sees.
      const target = {
        width: Math.min(video.videoWidth * 2, window.screen.width * devicePixelRatio),
        height: Math.min(video.videoHeight * 2, window.screen.height * devicePixelRatio),
      }
      canvas.width = target.width
      canvas.height = target.height

      await render({
        video,
        canvas,
        // The renderer returns no handle to stop its frame loop, so capture the
        // device here and destroy it on teardown.
        pipelineBuilder: (gpu: GPUDevice, inputTexture: unknown) => {
          device = gpu

          // A device can also be lost on its own (driver reset, backgrounded
          // too long); report it so the plain video comes back.
          void gpu.lost.then(() => {
            if (!cancelled) setState('failed')
          })

          return [
            new Preset({
              device: gpu,
              inputTexture,
              nativeDimensions: { width: video.videoWidth, height: video.videoHeight },
              targetDimensions: target,
            }),
          ]
        },
      } as never)

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
      // Otherwise the frame loop and its device leak on every restart until the
      // GPU gives up.
      device?.destroy()
      device = undefined
    }
  }, [video, canvas, enabled, mode, generation, source])

  return state
}
