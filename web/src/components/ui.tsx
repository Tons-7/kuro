import {
  useEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
} from 'react'
import { Link } from 'react-router-dom'
import { cx } from '../lib/format'

export type ButtonVariant = 'primary' | 'accent' | 'secondary' | 'ghost' | 'danger'
export type ButtonSize = 'sm' | 'md'

const VARIANTS: Record<ButtonVariant, string> = {
  primary: 'bg-white text-base-950 hover:bg-base-100 active:scale-[0.98]',
  accent:
    'bg-accent-500 text-white shadow-[0_0_20px_rgb(111_92_255/0.35)] hover:bg-accent-400 active:scale-[0.98]',
  secondary: 'bg-base-800 text-base-100 ring-1 ring-white/[0.06] hover:bg-base-700',
  ghost: 'text-base-300 hover:bg-base-850 hover:text-white',
  danger: 'bg-red-500/90 text-white hover:bg-red-500',
}

const SIZES: Record<ButtonSize, string> = {
  sm: 'px-2.5 py-1 text-xs',
  md: 'px-3.5 py-1.5 text-sm',
}

/** Class list for something that should look like a button but is a link. */
export function buttonClass(variant: ButtonVariant = 'secondary', size: ButtonSize = 'md') {
  return cx(
    'inline-flex shrink-0 items-center justify-center gap-1.5 rounded-md font-medium whitespace-nowrap transition-[background-color,color,transform] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400 disabled:pointer-events-none disabled:opacity-50',
    VARIANTS[variant],
    SIZES[size],
  )
}

export function Button({
  variant = 'secondary',
  size = 'md',
  className,
  type = 'button',
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant; size?: ButtonSize }) {
  return <button type={type} className={cx(buttonClass(variant, size), className)} {...props} />
}

export function LinkButton({
  to,
  variant = 'secondary',
  size = 'md',
  className,
  children,
  ...props
}: {
  to: string
  variant?: ButtonVariant
  size?: ButtonSize
  className?: string
  children: ReactNode
  title?: string
  'aria-label'?: string
}) {
  return (
    <Link to={to} className={cx(buttonClass(variant, size), className)} {...props}>
      {children}
    </Link>
  )
}

/** An on/off filter as a pill, which reads better than a bare checkbox. */
export function FilterToggle({
  on,
  onChange,
  children,
}: {
  on: boolean
  onChange: (on: boolean) => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={() => onChange(!on)}
      className={cx(
        'inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-sm font-medium ring-1 transition-colors',
        on
          ? 'bg-accent-500/20 text-accent-300 ring-accent-500/40'
          : 'bg-base-850 text-base-300 ring-white/[0.06] hover:text-white',
      )}
    >
      <span
        className={cx(
          'grid size-3.5 place-items-center rounded-full text-[9px] transition-colors',
          on ? 'bg-accent-500 text-white' : 'bg-base-700 text-transparent',
        )}
      >
        ✓
      </span>
      {children}
    </button>
  )
}

/** Title, optional subtitle and the page's actions on one line. */
export function PageHeader({
  title,
  subtitle,
  meta,
  actions,
}: {
  title: string
  subtitle?: string
  meta?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h1 className="page-title">{title}</h1>
          {meta && <span className="text-sm text-base-500">{meta}</span>}
        </div>
        {subtitle && <p className="mt-1 text-sm text-base-400">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

/** Text input with a leading magnifier, for filtering a list in place. */
export function SearchInput({
  className,
  inputClassName,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { inputClassName?: string }) {
  return (
    <div className={cx('relative', className)}>
      <svg
        viewBox="0 0 24 24"
        aria-hidden
        className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-base-500"
      >
        <circle cx="11" cy="11" r="6.5" fill="none" stroke="currentColor" strokeWidth="1.8" />
        <path d="m16 16 4.5 4.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
      <input
        type="search"
        className={cx(
          'w-full rounded-lg bg-base-900 py-2 pr-3 pl-9 text-sm text-base-100 ring-1 ring-white/[0.06] transition-[box-shadow] placeholder:text-base-500 focus:ring-accent-500 focus:outline-none [&::-webkit-search-cancel-button]:hidden',
          inputClassName,
        )}
        {...props}
      />
    </div>
  )
}

export function Spinner({ className }: { className?: string }) {
  return (
    <div
      role="status"
      aria-label="Loading"
      className={cx(
        'size-5 animate-spin rounded-full border-2 border-base-600 border-t-accent-400',
        className,
      )}
    />
  )
}

export function Centered({ children }: { children: ReactNode }) {
  return <div className="flex min-h-64 items-center justify-center">{children}</div>
}

export function Empty({
  title,
  hint,
  icon,
  action,
}: {
  title: string
  hint?: string
  icon?: ReactNode
  action?: ReactNode
}) {
  return (
    <Centered>
      <div className="flex max-w-sm flex-col items-center px-4 text-center">
        <span className="mb-4 grid size-14 place-items-center rounded-2xl bg-base-850 text-base-500 ring-1 ring-white/[0.06]">
          {icon ?? <EmptyIcon />}
        </span>
        <p className="font-medium text-base-100">{title}</p>
        {hint && <p className="mt-1 text-sm text-base-400">{hint}</p>}
        {action && <div className="mt-4">{action}</div>}
      </div>
    </Centered>
  )
}

function EmptyIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-6" aria-hidden>
      <path
        d="M4 7.5 12 3l8 4.5v9L12 21l-8-4.5v-9Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
      <path d="M4 7.5 12 12l8-4.5M12 12v9" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
    </svg>
  )
}

export function ErrorState({ error, retry }: { error: unknown; retry?: () => void }) {
  const message = error instanceof Error ? error.message : 'Something went wrong'
  return (
    <Centered>
      <div className="max-w-md text-center">
        <p className="text-base-200">{message}</p>
        {retry && (
          <button
            onClick={retry}
            className="mt-3 rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-200 transition-colors hover:bg-base-700"
          >
            Try again
          </button>
        )}
      </div>
    </Centered>
  )
}

/** Grey block that holds layout while data loads, so nothing jumps. */
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      className={cx(
        'animate-shimmer rounded-md bg-base-850 bg-[linear-gradient(90deg,transparent_25%,var(--color-base-800)_50%,transparent_75%)] bg-[length:200%_100%]',
        className,
      )}
    />
  )
}

export function SectionHeading({
  title,
  action,
}: {
  title: string
  action?: ReactNode
}) {
  return (
    <div className="mb-3 flex items-center justify-between gap-4">
      <h2 className="section-title">{title}</h2>
      {action}
    </div>
  )
}

/**
 * One choice among a few, as a filled track. Underlined links read as
 * navigation and leave the selected one hard to spot.
 */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  size = 'md',
}: {
  options: ReadonlyArray<{ value: T; label: string; count?: number }>
  value: T
  onChange: (value: T) => void
  size?: 'sm' | 'md'
}) {
  return (
    <div
      role="tablist"
      className="inline-flex max-w-full gap-0.5 overflow-x-auto rounded-lg bg-base-900 p-1 no-scrollbar"
    >
      {options.map((option) => {
        const active = option.value === value
        return (
          <button
            key={option.value}
            role="tab"
            aria-selected={active}
            onClick={() => onChange(option.value)}
            className={cx(
              'shrink-0 rounded-md font-medium whitespace-nowrap transition-colors',
              size === 'sm' ? 'px-2.5 py-1 text-xs' : 'px-3.5 py-1.5 text-sm',
              active
                ? 'bg-base-750 text-white shadow-card'
                : 'text-base-400 hover:text-base-100',
            )}
          >
            {option.label}
            {option.count !== undefined && (
              <span className={cx('ml-1.5 tabular-nums', active ? 'text-base-400' : 'text-base-600')}>
                {option.count}
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}

/** Closes on an outside click or Escape — both matter for a menu to feel right. */
export function useDismiss<T extends HTMLElement>(onClose: () => void) {
  const ref = useRef<T>(null)

  useEffect(() => {
    function onPointer(e: MouseEvent | TouchEvent) {
      const target = e.target as Node | null
      if (!ref.current || ref.current.contains(target)) return
      // A portal menu isn't inside the trigger, so without this a click on it
      // reads as outside and closes it.
      if (target instanceof Element && target.closest('[data-portal-menu]')) return
      onClose()
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', onPointer)
    document.addEventListener('touchstart', onPointer)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onPointer)
      document.removeEventListener('touchstart', onPointer)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose])

  return ref
}

/** Debounced value, for search-as-you-type against a rate-limited API. */
export function useDebounced<T>(value: T, delay = 300): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(id)
  }, [value, delay])
  return debounced
}

export function ProgressBar({ value, className }: { value: number; className?: string }) {
  return (
    <div className={cx('h-1 w-full overflow-hidden rounded-full bg-base-800', className)}>
      <div
        className="h-full rounded-full bg-accent-500 transition-[width] duration-300"
        style={{ width: `${Math.min(100, Math.max(0, value))}%` }}
      />
    </div>
  )
}

export function Badge({
  children,
  tone = 'neutral',
}: {
  children: ReactNode
  tone?: 'neutral' | 'filler' | 'recap' | 'accent' | 'success' | 'warning'
}) {
  const tones = {
    neutral: 'bg-base-800 text-base-300',
    filler: 'bg-filler/15 text-filler',
    recap: 'bg-recap/15 text-recap',
    accent: 'bg-accent-500/15 text-accent-400',
    success: 'bg-emerald-500/15 text-emerald-400',
    warning: 'bg-amber-500/15 text-amber-400',
  }
  return (
    <span
      className={cx(
        'rounded px-1.5 py-0.5 text-[11px] leading-tight font-medium whitespace-nowrap',
        tones[tone],
      )}
    >
      {children}
    </span>
  )
}

/**
 * A styled select that keeps the real element (native keyboard and phone
 * picker); the browser's own control paints an OS-grey box that clashes.
 */
export function Select({
  value,
  onChange,
  children,
  className,
  'aria-label': label,
}: {
  value: string
  onChange: (value: string) => void
  children: ReactNode
  className?: string
  'aria-label'?: string
}) {
  return (
    <div className={cx('relative shrink-0', className)}>
      <select
        value={value}
        aria-label={label}
        onChange={(e) => onChange(e.target.value)}
        className="w-full appearance-none rounded-md bg-base-800 py-1.5 pr-8 pl-3 text-sm text-base-100 transition-colors hover:bg-base-750 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        {children}
      </select>
      <svg
        viewBox="0 0 20 20"
        aria-hidden
        className="pointer-events-none absolute top-1/2 right-2.5 size-3.5 -translate-y-1/2 text-base-400"
      >
        <path
          d="M6 8l4 4 4-4"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </div>
  )
}
