import { useCallback } from 'react'
import { cx } from '../lib/format'
import { useDismiss } from './ui'

export const ANIME4K_MODES = [
  { id: 'A', title: 'A · Restore', hint: 'Sharpens soft, blurry lines. Safe default for most 1080p web releases.' },
  { id: 'B', title: 'B · Soft restore', hint: 'Gentler. Better when the source is noisy rather than blurry, since it will not harden compression artefacts.' },
  { id: 'C', title: 'C · Denoise only', hint: 'Upscales and denoises without touching linework. Cheapest, and the right pick for clean Blu-ray sources.' },
  { id: 'A+A', title: 'A+A · Restore twice', hint: 'Mode A run again. For heavily degraded or low-resolution sources like 480p.' },
  { id: 'B+B', title: 'B+B · Soft restore twice', hint: 'Mode B run again, for low-resolution sources that are noisy rather than blurry.' },
  { id: 'C+A', title: 'C+A · Denoise then restore', hint: 'Cleans the noise first, then rebuilds lines. For sources that are both noisy and soft.' },
] as const

// mpv only; the browser build ships fixed networks. Each step doubles GPU cost.
export const ANIME4K_SIZES = [
  { id: 'S', hint: 'Integrated graphics' },
  { id: 'M', hint: 'Balanced default' },
  { id: 'L', hint: 'Dedicated GPU' },
  { id: 'VL', hint: 'Strong GPU' },
  { id: 'UL', hint: 'High end only; diminishing returns' },
] as const

export function Anime4KDialog({
  open,
  enabled,
  mode,
  onClose,
  onChange,
}: {
  open: boolean
  enabled: boolean
  mode: string
  onClose: () => void
  onChange: (next: { enabled: boolean; mode: string }) => void
}) {
  const close = useCallback(() => onClose(), [onClose])
  const panel = useDismiss<HTMLDivElement>(close)

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" />

      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label="Anime4K"
        className="animate-fade-in relative max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-xl border border-base-800 bg-base-900 p-4 shadow-2xl scrollbar-thin"
      >
        <div className="mb-3 flex items-start justify-between gap-4">
          <div>
            <h2 className="text-base font-semibold text-white">Anime4K</h2>
            <p className="mt-0.5 text-xs text-base-500">
              Upscaling for this episode only. Your saved default is not changed.
            </p>
          </div>
          <button
            onClick={close}
            aria-label="Close"
            className="rounded-md px-2 py-1 text-base-400 hover:bg-base-800 hover:text-base-100"
          >
            ✕
          </button>
        </div>

        <button
          onClick={() => onChange({ enabled: !enabled, mode })}
          className={cx(
            'mb-3 flex w-full items-center justify-between rounded-lg border px-3 py-2.5 text-sm transition-colors',
            enabled
              ? 'border-accent-500/50 bg-accent-500/10 text-white'
              : 'border-base-800 bg-base-950/40 text-base-300 hover:bg-base-850',
          )}
        >
          <span className="font-medium">{enabled ? 'On' : 'Off'}</span>
          <span
            className={cx(
              'relative h-5 w-9 rounded-full transition-colors',
              enabled ? 'bg-accent-500' : 'bg-base-700',
            )}
          >
            <span
              className={cx(
                'absolute top-0.5 size-4 rounded-full bg-white transition-all',
                enabled ? 'left-[1.125rem]' : 'left-0.5',
              )}
            />
          </span>
        </button>

        {/* Clickable while off: picking a mode is how you turn it on. */}
        <div className={cx('space-y-1.5 transition-opacity', !enabled && 'opacity-60')}>
          {ANIME4K_MODES.map((option) => (
            <button
              key={option.id}
              onClick={() => onChange({ enabled: true, mode: option.id })}
              className={cx(
                'block w-full rounded-lg border px-3 py-2 text-left transition-colors',
                mode === option.id
                  ? 'border-accent-500/50 bg-accent-500/10'
                  : 'border-base-850 hover:bg-base-850',
              )}
            >
              <span
                className={cx(
                  'text-sm font-medium',
                  mode === option.id ? 'text-accent-300' : 'text-base-100',
                )}
              >
                {option.title}
              </span>
              <span className="mt-0.5 block text-xs leading-snug text-base-500">{option.hint}</span>
            </button>
          ))}
        </div>

        <p className="mt-3 text-xs text-base-600">
          Network size is an mpv-only setting and lives in Settings → Playback; the browser build
          ships fixed networks.
        </p>
      </div>
    </div>
  )
}
