import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Link,
  NavLink,
  Outlet,
  useLocation,
  useMatch,
  useNavigate,
  useNavigationType,
} from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, LIST_STATUSES, statusLabel, type LibraryItem } from '../lib/api'
import { cx } from '../lib/format'
import { usePrefs, useSetPref, useSetup } from '../lib/queries'
import { NotificationPanel } from './NotificationPanel'
import { useDebounced, useDismiss } from './ui'

const TABS = [
  { to: '/', label: 'Home', end: true },
  { to: '/browse', label: 'Browse' },
  { to: '/recent', label: 'Recent' },
  { to: '/schedule', label: 'Schedule' },
  { to: '/library', label: 'Library' },
]

export function Layout() {
  // The watch page manages its own width so the player can run edge to edge on
  // a phone; everything else gets the padded column.
  const fullBleed = useMatch('/watch/:animeId/:episode') !== null

  // Keyed on the path so each page animates in. Short enough that navigation
  // still feels immediate; the point is to show that something changed.
  const { pathname } = useLocation()

  // Every episode is the same page; re-keying would tear the player down.
  const pageKey = fullBleed ? 'watch' : pathname

  // Scroll to top on navigation, except back/forward (POP), where the old
  // position is the point.
  const navigation = useNavigationType()
  useEffect(() => {
    if (navigation !== 'POP') window.scrollTo(0, 0)
  }, [pathname, navigation])

  // A fresh install has no torrent engine and no sites, so nudge to setup
  // once — not on every route, or a local-only or failed install could reach
  // nothing.
  const setup = useSetup()
  const navigate = useNavigate()
  const nudged = useRef(false)
  useEffect(() => {
    if (nudged.current || !setup.data || pathname === '/setup') return
    if (setup.data.ready && setup.data.indexers > 0) return
    nudged.current = true
    navigate('/setup', { replace: true })
  }, [setup.data, pathname, navigate])

  return (
    <div className="min-h-dvh">
      <Header />
      <main
        key={pageKey}
        className={cx(
          'mx-auto w-full animate-enter',
          fullBleed ? 'pb-8' : 'max-w-[1600px] px-4 pt-4 pb-16 sm:px-6',
        )}
      >
        <Outlet />
      </main>
    </div>
  )
}

function Header() {
  const [scrolled, setScrolled] = useState(false)

  // The header sits over the hero art, so it only earns a background once the
  // page has moved past it.
  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8)
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  return (
    <header
      className={cx(
        'sticky top-0 z-40 transition-colors duration-200',
        scrolled ? 'border-b border-base-800 bg-base-950/85 backdrop-blur-md' : 'bg-transparent',
      )}
    >
      <div className="mx-auto flex h-16 w-full max-w-[1600px] items-center gap-4 px-4 sm:px-6">
        <Link to="/" className="group flex shrink-0 items-baseline gap-1">
          <span className="text-xl font-bold tracking-tight text-white">kuro</span>
          <span className="size-1.5 rounded-full bg-accent-500 transition-colors group-hover:bg-accent-400" />
        </Link>

        {/* A rule rather than more space. Set at the same weight as the links
            and separated only by a gap, the name read as the first of six. */}
        <span className="hidden h-6 w-px shrink-0 bg-base-800 sm:block" />

        {/* A solid, lighter surface: base-900/70 over base-950 is nearly the
            background, so the group read as loose text rather than a control. */}
        <nav className="hidden items-center gap-0.5 rounded-full bg-base-800 p-1 ring-1 ring-white/5 sm:flex">
          <Tabs />
        </nav>

        <SearchBox />
        <TitleLanguage />
        <NotificationPanel />
        <ProfileMenu />
      </div>

      {/* On a phone the links don't fit beside search, pushing everything to
          their right off-screen. Below sm they get their own row. */}
      <nav className="no-scrollbar mx-4 mb-2 flex items-center gap-0.5 overflow-x-auto rounded-full bg-base-800 p-1 ring-1 ring-white/5 sm:hidden">
        <Tabs />
      </nav>
    </header>
  )
}

function Tabs() {
  return (
    <>
      {TABS.map((tab) => (
        <NavLink
          key={tab.to}
          to={tab.to}
          end={tab.end}
          className={({ isActive }) =>
            cx(
              'shrink-0 rounded-full px-3 py-1.5 text-sm transition-colors',
              isActive
                ? 'bg-base-700 text-white shadow-card ring-1 ring-white/10'
                : 'text-base-300 hover:bg-base-850 hover:text-white',
            )
          }
        >
          {tab.label}
        </NavLink>
      ))}
    </>
  )
}

function SearchBox() {
  const navigate = useNavigate()
  const [value, setValue] = useState('')

  // Slash focuses search the way every site with a search box does.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement | null
      const typing = target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA'
      if (e.key === '/' && !typing) {
        e.preventDefault()
        document.getElementById('kuro-search')?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Instant results from what kuro already holds; AniList is the slow path
  // behind Enter or the last row.
  const q = useDebounced(value.trim(), 200)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const close = useCallback(() => setOpen(false), [])
  const box = useDismiss<HTMLFormElement>(close)
  const local = useQuery({
    enabled: q.length >= 2,
    queryKey: ['search', 'local', q],
    queryFn: () => api.get<{ results: LibraryItem[] }>(`/api/search/local?q=${encodeURIComponent(q)}&limit=8`),
    staleTime: 30_000,
  })
  const hits = q.length >= 2 ? (local.data?.results ?? []) : []
  const showMenu = open && q.length >= 2

  const browse = () => {
    if (!value.trim()) return
    setOpen(false)
    navigate(`/browse?q=${encodeURIComponent(value.trim())}`)
  }
  const pick = (id: number) => {
    setOpen(false)
    setValue('')
    navigate(`/anime/${id}`)
  }

  return (
    <form
      ref={box}
      className="relative ml-auto flex min-w-0 flex-1 justify-end sm:max-w-xs"
      onSubmit={(e) => {
        e.preventDefault()
        if (active >= 0 && hits[active]) pick(hits[active].id)
        else browse()
      }}
    >
      <div className="relative w-full max-w-56 sm:max-w-none">
        <SearchIcon />
        <input
          id="kuro-search"
          value={value}
          onChange={(e) => {
            setValue(e.target.value)
            setOpen(true)
            setActive(-1)
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setActive((a) => Math.min(hits.length, a + 1))
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setActive((a) => Math.max(-1, a - 1))
            } else if (e.key === 'Escape') {
              setOpen(false)
            }
          }}
          role="combobox"
          aria-expanded={showMenu}
          aria-controls="kuro-search-results"
          autoComplete="off"
          placeholder="Search…"
          aria-label="Search anime"
          className={cx(
            'w-full rounded-md border border-base-800 bg-base-900/80 py-1.5 pl-8 text-sm text-base-100 placeholder:text-base-500 focus:border-accent-500 focus:outline-none',
            // Padding only while there's something to clear, so the button never
            // sits over the text.
            value ? 'pr-8' : 'pr-3',
          )}
        />
        {!value && (
          <kbd className="pointer-events-none absolute top-1/2 right-2 hidden -translate-y-1/2 rounded border border-base-750 bg-base-850 px-1.5 font-mono text-[10px] leading-4 text-base-500 sm:block">
            /
          </kbd>
        )}
        {value && (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => {
              setValue('')
              document.getElementById('kuro-search')?.focus()
            }}
            className="absolute top-1/2 right-1 grid size-6 -translate-y-1/2 place-items-center rounded text-base-500 transition-colors hover:text-base-100"
          >
            <svg viewBox="0 0 20 20" aria-hidden className="size-3.5">
              <path
                d="M5 5l10 10M15 5L5 15"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
              />
            </svg>
          </button>
        )}
      </div>

      {showMenu && (
        <ul
          id="kuro-search-results"
          role="listbox"
          className="absolute top-full right-0 z-50 mt-1.5 w-80 max-w-[90vw] animate-rise overflow-hidden rounded-xl border border-base-750 bg-base-850 p-1 shadow-panel"
        >
          {hits.map((hit, i) => (
            <li key={hit.id} role="option" aria-selected={i === active}>
              <button
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => pick(hit.id)}
                className={cx(
                  'flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left transition-colors',
                  i === active ? 'bg-base-800' : 'hover:bg-base-800',
                )}
              >
                {hit.cover ? (
                  <img src={hit.cover} alt="" className="h-11 w-8 shrink-0 rounded object-cover" />
                ) : (
                  <div className="h-11 w-8 shrink-0 rounded bg-base-800" />
                )}
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm text-base-100">{hit.title}</span>
                  <span className="block text-[11px] text-base-500">
                    {hit.status ? statusLabel(hit.status) : 'Not on your list'}
                    {hit.progress > 0 && ` · ${hit.progress}${hit.episodes ? `/${hit.episodes}` : ''} watched`}
                  </span>
                </span>
              </button>
            </li>
          ))}
          {hits.length === 0 && local.isSuccess && (
            <li className="px-2 py-1.5 text-xs text-base-500">Nothing in your library.</li>
          )}
          <li role="option" aria-selected={active === hits.length}>
            <button
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={browse}
              className={cx(
                'block w-full rounded-lg px-2 py-1.5 text-left text-xs text-accent-400 transition-colors',
                active === hits.length ? 'bg-base-800' : 'hover:bg-base-800',
              )}
            >
              Search AniList for “{value.trim()}”
            </button>
          </li>
        </ul>
      )}
    </form>
  )
}

/**
 * English vs romaji titles — worth flipping mid-browse. Titles resolve
 * server-side, so every list has to be refetched.
 */
function TitleLanguage() {
  const prefs = usePrefs()
  const setPref = useSetPref()
  const qc = useQueryClient()

  const mode = prefs.data?.effective['display.titles'] ?? 'english'

  const choose = (next: string) => {
    if (next === mode) return
    setPref.mutate(
      { key: 'display.titles', value: next },
      {
        onSuccess: () => {
          for (const key of ['discover', 'browse', 'library', 'continue', 'schedule', 'aired', 'anime', 'search']) {
            void qc.invalidateQueries({ queryKey: [key] })
          }
        },
      },
    )
  }

  return (
    <div className="hidden shrink-0 items-center rounded-full bg-base-850 p-0.5 sm:flex">
      {[
        { value: 'english', label: 'EN' },
        { value: 'romaji', label: 'JP' },
      ].map((option) => (
        <button
          key={option.value}
          onClick={() => choose(option.value)}
          aria-pressed={mode === option.value}
          title={option.value === 'english' ? 'English titles' : 'Romaji titles'}
          className={cx(
            'rounded-full px-2.5 py-1 text-xs font-semibold transition-colors',
            mode === option.value
              ? 'bg-accent-500 text-white'
              : 'text-base-400 hover:text-base-100',
          )}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}

const MENU_ITEMS = [
  { to: '/history', label: 'Watch history', icon: <ClockIcon /> },
  { to: '/downloads', label: 'Downloads', icon: <DownloadIcon /> },
  { to: '/local', label: 'Local files', icon: <FolderIcon /> },
  { to: '/settings', label: 'Settings', icon: <GearIcon /> },
]

function ProfileMenu() {
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const ref = useDismiss<HTMLDivElement>(close)

  return (
    <div className="relative shrink-0" ref={ref}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Profile and settings"
        onClick={() => setOpen((v) => !v)}
        className="grid size-9 place-items-center rounded-full bg-base-800 text-sm font-medium text-base-200 transition-colors hover:bg-base-700"
      >
        <UserIcon />
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 z-50 mt-2 w-64 origin-top-right animate-rise overflow-hidden rounded-xl border border-base-750 bg-base-850 p-1.5 shadow-panel"
        >
          {/* The list statuses are the reason this menu exists, so they read as
              a set rather than as eleven identical rows with the rest. */}
          <p className="px-2 pt-1 pb-1.5 text-[11px] font-medium tracking-wide text-base-500 uppercase">
            My list
          </p>
          <div className="grid grid-cols-2 gap-0.5">
            {LIST_STATUSES.map((status) => (
              <Link
                key={status.value}
                role="menuitem"
                to={`/library?status=${status.value}`}
                onClick={close}
                className="rounded-lg px-2.5 py-1.5 text-sm text-base-200 transition-colors hover:bg-base-800 hover:text-white"
              >
                {status.label}
              </Link>
            ))}
          </div>

          <div className="my-1.5 border-t border-base-750" />

          {MENU_ITEMS.map((item) => (
            <Link
              key={item.to}
              role="menuitem"
              to={item.to}
              onClick={close}
              className="flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-sm text-base-200 transition-colors hover:bg-base-800 hover:text-white"
            >
              <span className="text-base-500">{item.icon}</span>
              {item.label}
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}

function SearchIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-base-500"
      aria-hidden
    >
      <circle cx="11" cy="11" r="6.5" fill="none" stroke="currentColor" strokeWidth="1.8" />
      <path d="m16 16 4.5 4.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
    </svg>
  )
}

function UserIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-5" aria-hidden>
      <circle cx="12" cy="8.5" r="3.5" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <path
        d="M5 20c1-3.5 3.8-5 7-5s6 1.5 7 5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
      />
    </svg>
  )
}

function ClockIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <circle cx="12" cy="12" r="8.5" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <path d="M12 7.5V12l3 2" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  )
}

function DownloadIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M12 4v10m0 0 4-4m-4 4-4-4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M5 18h14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  )
}

function FolderIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <path d="M4 7.5A1.5 1.5 0 0 1 5.5 6h3.4a1.5 1.5 0 0 1 1.2.6l.9 1.2h7.5A1.5 1.5 0 0 1 20 9.3v8.2a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 17.5v-10Z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
    </svg>
  )
}

function GearIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
      <circle cx="12" cy="12" r="3" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <path d="M12 3.5v2m0 13v2M20.5 12h-2m-13 0h-2m14.1-6.1-1.4 1.4M7.8 16.2l-1.4 1.4m12.3 0-1.4-1.4M7.8 7.8 6.4 6.4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  )
}
