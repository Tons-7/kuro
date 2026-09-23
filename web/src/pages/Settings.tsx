import { useContext, useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { api, ApiError, withToken } from '../lib/api'
import { bytes, cx, relativeTime } from '../lib/format'
import { usePrefs, useSetPref, useSetup } from '../lib/queries'
import { ANIME4K_MODES, ANIME4K_SIZES } from '../components/Anime4KDialog'
import { ComponentState } from '../components/ComponentState'
import { WhereThingsGo } from '../components/WhereThingsGo'
import { DESKTOP_NOTIFY_KEY, desktopNotifyWanted } from '../components/NotificationPanel'
import { buttonClass, ControlLabel, ProgressBar, Segmented, Select, Skeleton, Spinner } from '../components/ui'

const TABS = ['Playback', 'Quality', 'Trackers', 'Library', 'Notifications', 'Access', 'Jobs', 'About'] as const
type Tab = (typeof TABS)[number]

export function Settings() {
  // The OAuth callback lands here rather than on a dead "you may close this
  // tab" page, so it opens on the tab that shows the result. ?tab= is how an
  // update notification lands on About.
  const [params, setParams] = useSearchParams()
  const connected = params.get('connected')
  const wanted = params.get('tab') as Tab | null
  const [tab, setTab] = useState<Tab>(
    connected ? 'Trackers' : wanted && TABS.includes(wanted) ? wanted : 'Playback',
  )
  // A link to ?tab= (the update notification) while already here must switch.
  useEffect(() => {
    if (wanted && TABS.includes(wanted)) setTab(wanted)
  }, [wanted])

  return (
    // Centred as one block: the slack becomes margins, not a gulf between
    // every label and its control.
    <div className="mx-auto max-w-[59rem] space-y-5">
      <h1 className="text-xl font-semibold text-white">Settings</h1>

      {connected && (
        <div className="animate-fade-in flex items-center justify-between gap-4 rounded-lg border border-accent-500/40 bg-accent-500/10 px-3 py-2">
          <p className="text-sm text-accent-200">
            Connected to {connected === 'mal' ? 'MyAnimeList' : 'AniList'}.
          </p>
          <button
            onClick={() => setParams({}, { replace: true })}
            aria-label="Dismiss"
            className="rounded px-2 text-accent-300/70 hover:text-accent-100"
          >
            ✕
          </button>
        </div>
      )}

      {/* Beside the settings on a desktop, above them on a phone. */}
      <div className="lg:hidden">
        <Segmented
          options={TABS.map((name) => ({ value: name, label: name }))}
          value={tab}
          onChange={setTab}
        />
      </div>

      <div className="flex gap-6">
        <SideTabs tab={tab} onChange={setTab} />

        <div className="animate-fade-in w-full max-w-[47rem] min-w-0">
        {tab === 'Playback' && <PlaybackTab />}
        {tab === 'Quality' && <QualityTab />}
        {tab === 'Trackers' && <TrackersTab />}
        {tab === 'Library' && <LibraryTab />}
        {tab === 'Notifications' && <NotificationsTab />}
        {tab === 'Access' && <AccessTab />}
        {tab === 'Jobs' && <JobsTab />}
        {tab === 'About' && <AboutTab />}
        </div>
      </div>
    </div>
  )
}

function SideTabs({ tab, onChange }: { tab: Tab; onChange: (t: Tab) => void }) {
  return (
    // Stays beside a long tab instead of scrolling away with its top.
    <div role="tablist" className="sticky top-20 hidden w-42 shrink-0 flex-col gap-0.5 self-start lg:flex">
      {TABS.map((name) => (
        <button
          key={name}
          role="tab"
          aria-selected={name === tab}
          onClick={() => onChange(name)}
          className={cx(
            'rounded-lg px-3 py-1.5 text-left text-sm transition-colors',
            name === tab
              ? 'bg-accent-500/15 font-medium text-accent-200 shadow-[inset_2px_0_0_var(--color-accent-500)]'
              : 'text-base-400 hover:bg-base-900 hover:text-base-100',
          )}
        >
          {name}
        </button>
      ))}
    </div>
  )
}

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section className="surface p-5">
      <h2 className="font-display text-base font-semibold text-white">{title}</h2>
      {hint && <p className="mt-1 text-xs leading-relaxed text-base-500">{hint}</p>}
      {/* A rule per row: a two-line hint otherwise runs into the row below it. */}
      <div className="mt-2 divide-y divide-white/5">{children}</div>
    </section>
  )
}

// The row's label names its control for screen readers; the switches and
// selects themselves have no visible text.
const RowLabel = ControlLabel

function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    // Wraps on a narrow screen rather than clipping buttons off the edge.
    <div className="flex flex-wrap items-center justify-between gap-x-5 gap-y-2 py-2.5">
      <div className="min-w-0 flex-1 basis-48">
        <p className="text-sm text-base-200">{label}</p>
        {hint && <p className="text-xs text-base-500">{hint}</p>}
      </div>
      {/* One track so the right edge does not zigzag; not on a phone, where it
          would come out of the label. */}
      <div className="flex max-w-full justify-end sm:min-w-36">
        <RowLabel.Provider value={label}>{children}</RowLabel.Provider>
      </div>
    </div>
  )
}

function Switch({ on, onChange, label }: { on: boolean; onChange: (v: boolean) => void; label?: string }) {
  const rowLabel = useContext(RowLabel)
  return (
    <button
      role="switch"
      aria-checked={on}
      aria-label={label ?? rowLabel}
      onClick={() => onChange(!on)}
      className={cx(
        'relative h-5 w-9 rounded-full transition-colors',
        on ? 'bg-accent-500' : 'bg-base-700',
      )}
    >
      <span
        className={cx(
          'absolute top-0.5 size-4 rounded-full bg-white transition-all',
          on ? 'left-[1.125rem]' : 'left-0.5',
        )}
      />
    </button>
  )
}

function useFlag() {
  const prefs = usePrefs()
  const setPref = useSetPref()
  const values = prefs.data?.effective ?? {}
  return {
    loading: prefs.isPending,
    get: (key: string) => values[key] === 'true',
    value: (key: string) => values[key] ?? '',
    set: (key: string, value: string) => setPref.mutate({ key, value }),
  }
}

// Only when chosen and missing: kuro does not install VLC.
function VLCNote() {
  const f = useFlag()
  const setup = useSetup()
  if (f.value('playback.player') !== 'vlc' || !setup.data || setup.data.vlc) return null
  return (
    <p className="text-xs text-recap">
      VLC was not found. Install it from videolan.org, or if it is installed somewhere unusual set{' '}
      <code>vlc_path</code> in {setup.data.configPath} to its folder, then restart kuro.
    </p>
  )
}

function PlaybackTab() {
  const f = useFlag()
  if (f.loading) return <Skeleton className="h-64 w-full" />

  return (
    <div className="space-y-4">
      <Section title="Automatic behaviour" hint="All off by default. Flipping one here or in the player makes it the new default.">
        <Row label="Auto play" hint="Start playing as soon as the page opens">
          <Switch on={f.get('playback.autoplay')} onChange={(v) => f.set('playback.autoplay', String(v))} />
        </Row>
        <Row label="Auto next episode">
          <Switch on={f.get('playback.autonext')} onChange={(v) => f.set('playback.autonext', String(v))} />
        </Row>
        <Row label="Skip opening">
          <Switch on={f.get('playback.autoskip_op')} onChange={(v) => f.set('playback.autoskip_op', String(v))} />
        </Row>
        <Row label="Skip ending">
          <Switch on={f.get('playback.autoskip_ed')} onChange={(v) => f.set('playback.autoskip_ed', String(v))} />
        </Row>
        <Row label="Skip filler and recaps" hint="Auto-next and prefetch step over them; the list still shows them">
          <Switch on={f.get('playback.skip_filler')} onChange={(v) => f.set('playback.skip_filler', String(v))} />
        </Row>
      </Section>

      <Section title="Player">
        <Row label="Default player" hint="Desktop players only work on this machine; a phone or TV needs the browser. Progress, skips and auto-next work in all three.">
          <Select
            value={f.value('playback.player')}
            onChange={(v) => f.set('playback.player', v)}
          >
            <option value="browser">In browser</option>
            <option value="mpv">mpv</option>
            <option value="vlc">VLC</option>
          </Select>
        </Row>
        <VLCNote />
        <Row label="Anime4K upscaling" hint="Needs mpv, or a browser with WebGPU">
          <Switch on={f.get('playback.anime4k')} onChange={(v) => f.set('playback.anime4k', String(v))} />
        </Row>
        <Row
          label="Anime4K mode"
          hint={ANIME4K_MODES.find((m) => m.id === f.value('playback.anime4k_mode'))?.hint}
        >
          <Select
            value={f.value('playback.anime4k_mode')}
            onChange={(v) => f.set('playback.anime4k_mode', v)}
          >
            {ANIME4K_MODES.map((m) => (
              <option key={m.id} value={m.id}>{m.title}</option>
            ))}
          </Select>
        </Row>
        <Row
          label="Anime4K size"
          hint={`mpv only. Each step up roughly doubles GPU cost. ${
            ANIME4K_SIZES.find((s) => s.id === f.value('playback.anime4k_size'))?.hint ?? ''
          }`}
        >
          <Select
            value={f.value('playback.anime4k_size')}
            onChange={(v) => f.set('playback.anime4k_size', v)}
          >
            {ANIME4K_SIZES.map((s) => (
              <option key={s.id} value={s.id}>{s.id}</option>
            ))}
          </Select>
        </Row>
        <Row
          label="Find the next episode ahead of time"
          hint="Looks up its release while you watch, so pressing next starts at once instead of searching. Costs no real bandwidth."
        >
          <Switch
            on={f.get('playback.prepare_next')}
            onChange={(v) => f.set('playback.prepare_next', String(v))}
          />
        </Row>
        <Row
          label="Download next episode while watching"
          hint="Shares your connection with the episode playing. Leave off on a slow line."
        >
          <Switch
            on={f.get('cache.prefetch_next')}
            onChange={(v) => f.set('cache.prefetch_next', String(v))}
          />
        </Row>
      </Section>

      <Section title="Titles">
        <Row label="Show titles as">
          <Select
            value={f.value('display.titles')}
            onChange={(v) => f.set('display.titles', v)}
          >
            <option value="english">English</option>
            <option value="romaji">Romaji</option>
          </Select>
        </Row>
        <Row label="Audio preference">
          <Select
            value={f.value('audio.prefer')}
            onChange={(v) => f.set('audio.prefer', v)}
          >
            <option value="sub">Subbed</option>
            <option value="dub">Dubbed</option>
            <option value="either">Either</option>
          </Select>
        </Row>
      </Section>

      <Section title="Window">
        <Row
          label="Open kuro"
          hint="Takes effect the next time kuro starts. F11 switches either way."
        >
          <Select value={f.value('window.mode') || 'fullscreen'} onChange={(v) => f.set('window.mode', v)}>
            <option value="fullscreen">Fullscreen</option>
            <option value="maximized">Maximized</option>
          </Select>
        </Row>
      </Section>

      <SubtitleLanguages f={f} />
    </div>
  )
}

const LANGUAGES = [
  { code: 'en', name: 'English' },
  { code: 'es', name: 'Spanish' },
  { code: 'pt', name: 'Portuguese' },
  { code: 'fr', name: 'French' },
  { code: 'de', name: 'German' },
  { code: 'it', name: 'Italian' },
  { code: 'ru', name: 'Russian' },
  { code: 'ar', name: 'Arabic' },
  { code: 'zh', name: 'Chinese' },
  { code: 'ko', name: 'Korean' },
  { code: 'id', name: 'Indonesian' },
  { code: 'vi', name: 'Vietnamese' },
  { code: 'th', name: 'Thai' },
]

/**
 * Which subtitles are worth having. The order matters twice over: it decides
 * which release is picked, and which track plays inside it.
 */
function SubtitleLanguages({ f }: { f: ReturnType<typeof useFlag> }) {
  const chosen: string[] = (() => {
    try {
      const parsed = JSON.parse(f.value('subtitle.languages') || '["en"]')
      return Array.isArray(parsed) ? parsed : ['en']
    } catch {
      return ['en']
    }
  })()

  const set = (next: string[]) => f.set('subtitle.languages', JSON.stringify(next))
  const toggle = (code: string) =>
    set(chosen.includes(code) ? chosen.filter((c) => c !== code) : [...chosen, code])

  return (
    <Section
      title="Subtitle languages"
      hint="Best first. A release in none of them is ranked lower, never refused — for some shows it is the only one there is."
    >
      <div className="flex flex-wrap gap-1.5 px-3 py-2">
        {LANGUAGES.map((lang) => {
          const at = chosen.indexOf(lang.code)
          return (
            <button
              key={lang.code}
              onClick={() => toggle(lang.code)}
              className={cx(
                'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                at >= 0
                  ? 'bg-accent-500/20 text-accent-300 ring-1 ring-accent-500/40'
                  : 'bg-base-850 text-base-400 hover:bg-base-800 hover:text-base-200',
              )}
            >
              {at >= 0 && <span className="mr-1 tabular-nums opacity-70">{at + 1}</span>}
              {lang.name}
            </button>
          )
        })}
      </div>
      {chosen.length === 0 && (
        <p className="px-3 pb-2 text-xs text-base-500">
          With none chosen, subtitle language is ignored when ranking releases.
        </p>
      )}
    </Section>
  )
}

interface OrphanResult {
  files: { name: string; bytes: number }[]
  removed: number
  freedBytes: number
}

function QualityTab() {
  const f = useFlag()
  const qc = useQueryClient()
  const cache = useQuery({ queryKey: ['cache'], queryFn: () => api.get<Record<string, number>>('/api/cache') })
  const sweep = useMutation({
    mutationFn: () => api.post('/api/cache/sweep'),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['cache'] }),
  })
  // Looked at first, deleted only once the list has been seen.
  const preview = useMutation({
    meta: { inline: true },
    mutationFn: () => api.get<OrphanResult>('/api/cache/orphans'),
  })
  const orphans = useMutation({
    meta: { inline: true },
    mutationFn: () => api.post<OrphanResult>('/api/cache/orphans'),
    onSuccess: () => {
      preview.reset()
      qc.invalidateQueries({ queryKey: ['cache'] })
    },
  })
  const found = preview.data

  if (f.loading) return <Skeleton className="h-48 w-full" />

  return (
    <div className="space-y-4">
      <Section title="Cache" hint="Watched episodes stay on disk so a rewatch is instant. Oldest are evicted first. Episodes you download yourself are kept outside this budget.">
        <Row label="Budget">
          <Select
            value={f.value('cache.budget_bytes')}
            onChange={(v) => f.set('cache.budget_bytes', v)}
          >
            {[5, 10, 20, 40, 80].map((gb) => (
              <option key={gb} value={String(gb * 1024 ** 3)}>{gb} GB</option>
            ))}
          </Select>
        </Row>
        <Row
          label="Keep whole episode"
          hint="Keep downloading after you stop, so coming back needs no swarm. Off, a download pauses the moment you leave."
        >
          <Switch on={f.get('cache.prefetch_full')} onChange={(v) => f.set('cache.prefetch_full', String(v))} />
        </Row>
        <Row
          label="Auto-delete watched"
          hint="Once two more are watched keeps the last one, so going back is still free."
        >
          <Select value={f.value('cache.autodelete') || 'off'} onChange={(v) => f.set('cache.autodelete', v)}>
            <option value="off">Off</option>
            <option value="now">Right after watching</option>
            <option value="keep2">Once two more are watched</option>
          </Select>
        </Row>
        {(f.value('cache.autodelete') || 'off') !== 'off' && (
          <Row
            label="Also delete downloaded episodes"
            hint="Downloads are never evicted, so this is their only automatic cleanup."
          >
            <Switch
              on={f.get('cache.autodelete_downloads')}
              onChange={(v) => f.set('cache.autodelete_downloads', String(v))}
            />
          </Row>
        )}
        <Row
          label="In use"
          hint={
            cache.data
              ? `${bytes(Number(cache.data.bytes ?? 0))} cached · ${bytes(Number(cache.data.keptBytes ?? 0))} downloaded`
              : '—'
          }
        >
          <button
            onClick={() => sweep.mutate()}
            disabled={sweep.isPending}
            className={buttonClass()}
          >
            {sweep.isPending ? 'Sweeping…' : 'Sweep now'}
          </button>
        </Row>
        <Row
          label="Orphaned files"
          hint={
            orphans.data
              ? `Removed ${orphans.data.removed} · freed ${bytes(orphans.data.freedBytes)}`
              : found
                ? found.removed === 0
                  ? 'Nothing orphaned.'
                  : `${found.removed} found · ${bytes(found.freedBytes)}`
                : 'Folders in the cache the torrent engine no longer knows about — left by a crash or a delete outside kuro. They escape the budget until removed.'
          }
        >
          {found && found.removed > 0 ? (
            <div className="flex gap-2">
              <button
                onClick={() => orphans.mutate()}
                disabled={orphans.isPending}
                className={buttonClass('danger')}
              >
                {orphans.isPending ? 'Deleting…' : `Delete ${found.removed}`}
              </button>
              <button
                onClick={() => preview.reset()}
                className={buttonClass()}
              >
                Cancel
              </button>
            </div>
          ) : (
            <button
              onClick={() => {
                orphans.reset()
                preview.mutate()
              }}
              disabled={preview.isPending}
              className={buttonClass()}
            >
              {preview.isPending ? 'Looking…' : 'Find'}
            </button>
          )}
        </Row>
        {found && found.removed > 0 && (
          <ul className="max-h-40 overflow-y-auto rounded-md bg-base-950 px-3 py-2 font-mono text-[11px] text-base-400">
            {found.files.map((f) => (
              <li key={f.name} className="flex justify-between gap-4">
                <span className="truncate">{f.name}</span>
                <span className="shrink-0">{bytes(f.bytes)}</span>
              </li>
            ))}
          </ul>
        )}
        {(preview.error ?? orphans.error) && (
          <p className="text-xs text-recap">{((preview.error ?? orphans.error) as Error).message}</p>
        )}
      </Section>

      <Section title="Downloads">
        <PreferGroups f={f} />
        <Row label="Allow 10-bit H.264 (Hi10P)" hint="No hardware decodes it; expect high CPU">
          <Switch on={f.get('quality.allow_hi10p')} onChange={(v) => f.set('quality.allow_hi10p', String(v))} />
        </Row>
        <Row label="Auto-download followed anime">
          <Switch on={f.get('autodownload.enabled')} onChange={(v) => f.set('autodownload.enabled', String(v))} />
        </Row>
      </Section>
    </div>
  )
}

// Release groups to favour, typed as a comma list and stored as a JSON array.
function PreferGroups({ f }: { f: ReturnType<typeof useFlag> }) {
  const stored: string[] = (() => {
    try {
      const parsed = JSON.parse(f.value('release.prefer_groups') || '[]')
      return Array.isArray(parsed) ? parsed : []
    } catch {
      return []
    }
  })()
  const [text, setText] = useState(stored.join(', '))
  useEffect(() => setText(stored.join(', ')), [f.value('release.prefer_groups')]) // eslint-disable-line react-hooks/exhaustive-deps

  const save = () => {
    const groups = text.split(',').map((g) => g.trim()).filter(Boolean)
    if (groups.join(',') !== stored.join(',')) f.set('release.prefer_groups', JSON.stringify(groups))
  }

  return (
    <Row label="Preferred release groups" hint="Comma separated, e.g. SubsPlease, Erai-raws. A match ranks the release higher.">
      <input
        value={text}
        onChange={(e) => setText(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
        placeholder="SubsPlease, Erai-raws"
        aria-label="Preferred release groups"
        className="w-56 rounded-md border border-base-800 bg-base-900 px-2.5 py-1.5 text-sm text-base-100 placeholder:text-base-600 focus:border-accent-500 focus:outline-none"
      />
    </Row>
  )
}

interface Tracker {
  provider: string
  name: string
  clientId: string
  hasSecret: boolean
  configured: boolean
  connected: boolean
  reconnect?: boolean
  user?: string
  redirect: string
  register: string
  secretRequired: boolean
}

function TrackersTab() {
  const trackers = useQuery({
    queryKey: ['trackers'],
    queryFn: () => api.get<{ trackers: Tracker[] }>('/api/trackers'),
  })

  return (
    <div className="space-y-4">
      {trackers.isPending ? (
        <Skeleton className="h-64 w-full" />
      ) : (
        trackers.data?.trackers.map((tracker) => (
          <TrackerCard key={tracker.provider} tracker={tracker} />
        ))
      )}
    </div>
  )
}

function TrackerCard({ tracker }: { tracker: Tracker }) {
  const qc = useQueryClient()
  const [clientId, setClientId] = useState(tracker.clientId)
  const [secret, setSecret] = useState('')
  const [editing, setEditing] = useState(!tracker.configured)

  const save = useMutation({
    meta: { inline: true },
    mutationFn: () =>
      api.post('/api/trackers', {
        provider: tracker.provider,
        clientId,
        // An untouched field must not wipe the stored secret.
        clientSecret: secret === '' && tracker.hasSecret ? undefined : secret,
      }),
    onSuccess: () => {
      setSecret('')
      setEditing(false)
      qc.invalidateQueries({ queryKey: ['trackers'] })
    },
  })

  // MAL's API has no favourites; the public profile can be read once.
  const importFavs = useMutation({
    meta: { inline: true },
    mutationFn: () =>
      api.post<{ found: number; matched: number; favourited: number; unmatched: number }>(
        '/api/mal/favourites/import',
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['bookmarks'] }),
  })

  const status = tracker.reconnect
    ? `${tracker.name} refused the saved login. Reconnect to resume syncing.`
    : tracker.connected
      ? `Connected as ${tracker.user ?? '—'}`
      : tracker.configured
        ? 'Set up, not connected'
        : 'Not set up'

  const loginPath = tracker.provider === 'anilist' ? '/api/auth/login' : '/api/mal/auth/login'
  const logoutPath =
    tracker.provider === 'anilist' ? '/api/auth/logout' : '/api/mal/auth/logout'

  const [confirmingOut, setConfirmingOut] = useState(false)
  const logout = useMutation({
    mutationFn: () => api.post(logoutPath),
    onSuccess: () => {
      setConfirmingOut(false)
      qc.invalidateQueries({ queryKey: ['trackers'] })
    },
  })

  return (
    <Section
      title={tracker.name}
      hint={
        tracker.provider === 'anilist'
          ? 'Progress syncs automatically as you watch.'
          : 'Optional second tracker, kept in step from the same local progress.'
      }
    >
      <Row label={status}>
        <div className="flex gap-2">
          {tracker.configured && (
            <a
              href={loginPath}
              className={buttonClass('primary')}
            >
              {tracker.connected ? 'Reconnect' : 'Connect'}
            </a>
          )}
          <button
            onClick={() => setEditing((open) => !open)}
            className={buttonClass()}
          >
            {editing ? 'Cancel' : tracker.configured ? 'Edit keys' : 'Add keys'}
          </button>
          {tracker.provider === 'mal' && tracker.connected && (
            <button
              onClick={() => importFavs.mutate()}
              disabled={importFavs.isPending}
              title="MyAnimeList's API cannot sync favourites; this copies them over once"
              className={buttonClass()}
            >
              {importFavs.isPending ? 'Importing…' : 'Import favourites'}
            </button>
          )}

          {tracker.connected &&
            (confirmingOut ? (
              <div className="flex items-center gap-1 rounded-md bg-base-950 p-1 ring-1 ring-base-700">
                <button
                  onClick={() => logout.mutate()}
                  disabled={logout.isPending}
                  className="rounded px-2 py-1 text-sm font-medium text-recap hover:bg-base-800 disabled:opacity-50"
                >
                  Disconnect
                </button>
                <button
                  onClick={() => setConfirmingOut(false)}
                  className="rounded px-2 py-1 text-sm text-base-400 hover:bg-base-800"
                >
                  Keep
                </button>
              </div>
            ) : (
              <button
                onClick={() => setConfirmingOut(true)}
                className={buttonClass()}
              >
                Disconnect
              </button>
            ))}
        </div>
      </Row>

      {importFavs.data && (
        <p className="px-3 pb-2 text-xs text-base-400">
          {importFavs.data.favourited} favourited of {importFavs.data.found} on MyAnimeList
          {importFavs.data.unmatched > 0 && `, ${importFavs.data.unmatched} not in the catalogue`}.
        </p>
      )}
      {importFavs.isError && (
        <p className="px-3 pb-2 text-xs text-recap">{(importFavs.error as Error).message}</p>
      )}

      {/* Neither service offers token revocation, so being explicit about what
          disconnecting does avoids a false sense of having revoked access. */}
      {confirmingOut && (
        <p className="px-3 pb-2 text-xs text-base-500">
          Forgets the account on this machine. Watch history stays. The
          authorisation remains on your {tracker.name} account until you remove
          it there.
        </p>
      )}

      {tracker.connected && tracker.provider === 'anilist' && <ImportRow />}

      {editing && (
        <div className="space-y-3 rounded-lg border border-base-850 bg-base-950/40 p-3">
          <p className="text-xs text-base-400">
            Create an API client at{' '}
            <a
              href={tracker.register}
              target="_blank"
              rel="noreferrer"
              className="text-accent-400 hover:underline"
            >
              {tracker.register.replace('https://', '')}
            </a>
            , then paste its keys here. Give it this redirect URL:
          </p>
          <CopyField value={tracker.redirect} />

          <Field label="Client ID" value={clientId} onChange={setClientId} placeholder="12345" />
          <Field
            label={tracker.secretRequired ? 'Client secret' : 'Client secret (optional)'}
            value={secret}
            onChange={setSecret}
            secret
            placeholder={tracker.hasSecret ? 'Saved — leave blank to keep' : ''}
          />

          <div className="flex items-center gap-2">
            <button
              onClick={() => save.mutate()}
              disabled={save.isPending || clientId.trim() === ''}
              className={buttonClass('primary')}
            >
              {save.isPending ? 'Saving…' : 'Save'}
            </button>
            {save.isError && <span className="text-xs text-red-400">Could not save</span>}
          </div>
        </div>
      )}
    </Section>
  )
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  secret,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  secret?: boolean
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs text-base-400">{label}</span>
      <input
        type={secret ? 'password' : 'text'}
        value={value}
        placeholder={placeholder}
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-md border border-base-800 bg-base-900 px-2.5 py-1.5 font-mono text-sm text-base-100 placeholder:text-base-600 focus:border-accent-500 focus:outline-none"
      />
    </label>
  )
}

function CopyField({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)

  return (
    <div className="flex items-center gap-2">
      <code className="min-w-0 flex-1 truncate rounded-md border border-base-800 bg-base-900 px-2.5 py-1.5 text-xs text-base-200">
        {value}
      </code>
      <button
        onClick={() => {
          navigator.clipboard.writeText(value)
          setCopied(true)
          window.setTimeout(() => setCopied(false), 1500)
        }}
        className="shrink-0 rounded-md bg-base-800 px-2.5 py-1.5 text-xs text-base-100 hover:bg-base-700"
      >
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

function ImportRow() {
  const qc = useQueryClient()
  const [mode, setMode] = useState<'merge' | 'replace'>('merge')
  const [confirming, setConfirming] = useState(false)

  const sync = useMutation({
    mutationFn: () => api.post(`/api/sync?mode=${mode}`),
    onSuccess: () => {
      setConfirming(false)
      qc.invalidateQueries({ queryKey: ['library'] })
    },
  })

  const result = sync.data as { entries?: number; removed?: number } | undefined

  return (
    <div className="space-y-2 rounded-lg border border-base-850 bg-base-950/40 p-3">
      <p className="text-xs text-base-400">Bring your AniList list into kuro.</p>

      <div className="flex gap-1 rounded-md bg-base-900 p-1">
        {(['merge', 'replace'] as const).map((option) => (
          <button
            key={option}
            onClick={() => {
              setMode(option)
              setConfirming(false)
            }}
            className={cx(
              'flex-1 rounded px-2 py-1 text-xs font-medium capitalize transition-colors',
              mode === option ? 'bg-base-750 text-white' : 'text-base-400 hover:text-base-200',
            )}
          >
            {option}
          </button>
        ))}
      </div>

      <p className="text-xs text-base-500">
        {mode === 'merge'
          ? 'Keeps anything AniList does not have and protects changes you have not pushed yet.'
          : 'Makes your list exactly what AniList holds. Entries only in kuro are deleted and unpushed changes are overwritten.'}
      </p>

      {mode === 'replace' && confirming ? (
        <div className="flex gap-2">
          <button
            onClick={() => sync.mutate()}
            disabled={sync.isPending}
            className={buttonClass('danger')}
          >
            {sync.isPending ? 'Replacing…' : 'Yes, replace'}
          </button>
          <button
            onClick={() => setConfirming(false)}
            className={buttonClass()}
          >
            Cancel
          </button>
        </div>
      ) : (
        <button
          onClick={() => (mode === 'replace' ? setConfirming(true) : sync.mutate())}
          disabled={sync.isPending}
          className={buttonClass()}
        >
          {sync.isPending ? 'Importing…' : mode === 'merge' ? 'Merge list' : 'Replace list'}
        </button>
      )}

      {result && (
        <p className="text-xs text-base-400">
          Imported {result.entries ?? 0} entries
          {result.removed ? `, removed ${result.removed}` : ''}.
        </p>
      )}
    </div>
  )
}

function LibraryTab() {
  const local = useQuery({
    queryKey: ['local'],
    queryFn: () => api.get<{ stats: Record<string, number>; roots: string[]; scanning: boolean }>('/api/local'),
    refetchInterval: (q) => (q.state.data?.scanning ? 2000 : false),
  })
  const [path, setPath] = useState('')
  const qc = useQueryClient()

  const save = useMutation({
    meta: { inline: true },
    mutationFn: (paths: string[]) => api.post('/api/local/paths', { paths }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['local'] }),
  })
  const scan = useMutation({
    mutationFn: () => api.post('/api/local/scan'),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['local'] }),
  })

  const roots = local.data?.roots ?? []

  return (
    <div className="space-y-4">
    <Section title="Local files" hint="Anime already on disk plays instantly, with no torrent involved.">
      <ul className="space-y-1">
        {roots.map((root) => (
          <li key={root} className="flex items-center justify-between gap-2 rounded-md bg-base-900 px-3 py-1.5">
            <span className="truncate text-sm text-base-200">{root}</span>
            <button
              onClick={() => save.mutate(roots.filter((r) => r !== root))}
              className="text-xs text-base-500 hover:text-recap"
            >
              Remove
            </button>
          </li>
        ))}
      </ul>

      <div className="flex gap-2">
        <input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="D:\Anime"
          className="min-w-0 flex-1 rounded-md border border-base-800 bg-base-900 px-3 py-1.5 text-sm text-base-100 placeholder:text-base-600"
        />
        <button
          onClick={() => {
            if (path.trim()) save.mutate([...roots, path.trim()])
            setPath('')
          }}
          className={buttonClass()}
        >
          Add
        </button>
      </div>

      {save.isError && <p className="text-xs text-recap">{(save.error as Error).message}</p>}

      <Row
        label={
          local.data
            ? `${local.data.stats.files} files · ${local.data.stats.matched} matched · ${local.data.stats.unmatched} unmatched`
            : 'No scan yet'
        }
      >
        <button
          onClick={() => scan.mutate()}
          disabled={local.data?.scanning || roots.length === 0}
          className="flex items-center gap-2 rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700 disabled:opacity-50"
        >
          {local.data?.scanning && <Spinner className="size-3.5" />}
          {local.data?.scanning ? 'Scanning…' : 'Scan now'}
        </button>
      </Row>
    </Section>

    <BackupSection />
    </div>
  )
}

function BackupSection() {
  const qc = useQueryClient()
  const [message, setMessage] = useState('')

  // A plain link so the browser saves the file; a navigation cannot carry the
  // auth header, so the token rides in the URL.
  const exportURL = (format: string) => withToken(`/api/library/export?format=${format}`)

  const load = useMutation({
    meta: { inline: true },
    mutationFn: (file: File) =>
      api.upload<{ entries: number; favourites: number; skipped: number }>(
        '/api/library/import',
        file,
      ),
    onSuccess: (rep) => {
      setMessage(
        `Imported ${rep.entries} ${rep.entries === 1 ? 'entry' : 'entries'}` +
          (rep.favourites ? `, ${rep.favourites} favourited` : '') +
          (rep.skipped ? `, ${rep.skipped} skipped` : '') +
          '. Connected trackers get them on the next sync.',
      )
      for (const key of ['library', 'continue', 'bookmarks', 'anime']) {
        void qc.invalidateQueries({ queryKey: [key] })
      }
    },
    onError: (err) => setMessage((err as Error).message),
  })

  return (
    <Section
      title="Backup"
      hint="Everything watched, rated and favourited, in one file. Importing merges: progress never moves backwards."
    >
      <Row label="Export" hint="JSON to import again, text to read">
        <div className="flex gap-2">
          <a
            href={exportURL('json')}
            download
            className={buttonClass()}
          >
            JSON
          </a>
          <a
            href={exportURL('txt')}
            download
            className={buttonClass()}
          >
            Text
          </a>
        </div>
      </Row>

      <Row label="Import" hint="A file exported here, or a MyAnimeList export (.xml or .xml.gz)">
        <label className="cursor-pointer rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700">
          {load.isPending ? 'Importing…' : 'Choose file'}
          <input
            type="file"
            accept=".json,.xml,.gz,application/json,text/xml,application/gzip"
            className="hidden"
            disabled={load.isPending}
            onChange={(e) => {
              const file = e.target.files?.[0]
              e.target.value = ''
              if (!file) return
              setMessage('')
              load.mutate(file)
            }}
          />
        </label>
      </Row>

      {message && (
        <p className={cx('text-xs', load.isError ? 'text-recap' : 'text-base-400')}>{message}</p>
      )}
    </Section>
  )
}

// Desktop alerts are a browser permission plus a local switch: the choice
// belongs to this browser, not the account.
function DesktopNotifyRow() {
  const supported = typeof Notification !== 'undefined'
  const [on, setOn] = useState(desktopNotifyWanted())
  const [permission, setPermission] = useState(supported ? Notification.permission : 'denied')

  const toggle = async (next: boolean) => {
    if (next && permission !== 'granted') {
      const got = await Notification.requestPermission()
      setPermission(got)
      if (got !== 'granted') return
    }
    try {
      localStorage.setItem(DESKTOP_NOTIFY_KEY, next ? '1' : '0')
    } catch {
      // Storage unavailable; the switch just will not stick.
    }
    setOn(next)
  }

  if (!supported) return null
  return (
    <Row
      label="Desktop alerts"
      hint={
        permission === 'denied'
          ? 'Blocked by the browser; allow notifications for this site to use it.'
          : 'Pop up on this device when a new episode appears, even with kuro in another tab.'
      }
    >
      <Switch label="Desktop alerts" on={on && permission === 'granted'} onChange={(v) => void toggle(v)} />
    </Row>
  )
}

function NotificationsTab() {
  const f = useFlag()
  if (f.loading) return <Skeleton className="h-40 w-full" />

  return (
    <Section title="Release notifications" hint="Checks for new episodes of anime you follow.">
      <Row label="Enabled">
        <Switch on={f.get('notify.enabled')} onChange={(v) => f.set('notify.enabled', String(v))} />
      </Row>
      <DesktopNotifyRow />
      <Row label="Notify for">
        <Select
          value={f.value('notify.releases')}
          onChange={(v) => f.set('notify.releases', v)}
        >
          <option value="sub">Subbed</option>
          <option value="dub">Dubbed</option>
          <option value="both">Both</option>
        </Select>
      </Row>
    </Section>
  )
}

function AccessTab() {
  const qc = useQueryClient()
  const access = useQuery({
    queryKey: ['access'],
    queryFn: () =>
      api.get<{
        reachable: boolean
        listening: string
        urls: string[]
        canSwitch: boolean
        host?: boolean
      }>('/api/access'),
  })

  // Moves the listener there and then, so the phone can be tried immediately.
  const setNetwork = useMutation({
    meta: { inline: true },
    mutationFn: (lan: boolean) => api.post('/api/access/network', { lan }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['access'] }),
  })

  return (
    <Section
      title="Watch on your phone or TV"
      hint="Opening this listens on every network this machine is on. The link carries a token; anything without it is refused."
    >
      {access.data?.canSwitch && (
        <Row
          label="Allow other devices"
          hint={
            setNetwork.isError
              ? (setNetwork.error as Error).message
              : `Listening on ${access.data.listening}`
          }
        >
          <Switch
            on={!!access.data.reachable}
            onChange={(v) => setNetwork.mutate(v)}
          />
        </Row>
      )}

      {access.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : access.isError ? (
        <p className="text-sm text-recap">{(access.error as Error).message}</p>
      ) : !access.data?.reachable ? (
        <p className="text-sm text-base-400">
          Only this machine can reach kuro right now.
        </p>
      ) : access.data.urls.length === 0 ? (
        <p className="text-sm text-base-300">
          This machine has no home-network address right now (not on Wi-Fi or Ethernet, or only on a
          VPN), so there is nothing a phone could open yet.
        </p>
      ) : (
        <div className="flex flex-wrap items-center gap-5">
          <img
            src="/api/access/qr.svg"
            alt="Pairing QR code"
            className="size-40 rounded-lg bg-white p-2"
          />
          <div className="min-w-0 flex-1">
            <p className="mb-1 text-xs text-base-500">Scan, or open this on the device:</p>
            {access.data.urls.map((url) => (
              <div key={url} className="flex items-center gap-2">
                <code className="min-w-0 truncate text-xs text-accent-400">{url}</code>
                <CopyButton text={url} />
              </div>
            ))}
          </div>
        </div>
      )}
      {access.data?.reachable && access.data.host && <FirewallPanel />}
      {access.data?.reachable && access.data.host && <RevokeDevices />}
    </Section>
  )
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      onClick={() =>
        void navigator.clipboard?.writeText(text).then(() => {
          setCopied(true)
          window.setTimeout(() => setCopied(false), 1500)
        })
      }
      className="shrink-0 rounded px-1.5 py-0.5 text-[11px] text-base-400 hover:bg-base-800 hover:text-white"
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}

// A new token signs every paired phone and TV out at once; they pair again
// with the new code.
function RevokeDevices() {
  const qc = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const rotate = useMutation({
    meta: { inline: true },
    mutationFn: () => api.post('/api/access/rotate'),
    onSuccess: () => {
      setConfirming(false)
      void qc.invalidateQueries({ queryKey: ['access'] })
    },
  })
  return (
    <Row
      label="Paired devices"
      hint={
        rotate.isSuccess
          ? 'Every device was signed out. Scan the new code to pair again.'
          : rotate.isError
            ? (rotate.error as Error).message
            : 'Sign out every phone and TV that has the link.'
      }
    >
      {confirming ? (
        <div className="flex gap-2">
          <button
            onClick={() => rotate.mutate()}
            disabled={rotate.isPending}
            className={buttonClass('danger')}
          >
            Sign them out
          </button>
          <button
            onClick={() => setConfirming(false)}
            className={buttonClass()}
          >
            Cancel
          </button>
        </div>
      ) : (
        <button
          onClick={() => setConfirming(true)}
          className={buttonClass()}
        >
          Sign out all devices
        </button>
      )}
    </Row>
  )
}

interface FirewallState {
  status: {
    supported: boolean
    networks: { name: string; interface: string; category: string }[]
    firewall?: string
    hint?: string
    command?: string
  }
  reachable: boolean
  blocked: boolean
  public: boolean
}

// Why a phone on the same Wi-Fi can or cannot get in, and the fix. Windows
// asks about a program once; a Cancel, or a network it calls Public, blocks
// every device afterwards without saying so.
function FirewallPanel() {
  const qc = useQueryClient()
  const [alsoPublic, setAlsoPublic] = useState(false)
  const fw = useQuery({
    queryKey: ['firewall'],
    queryFn: () => api.get<FirewallState>('/api/access/firewall'),
    refetchOnWindowFocus: true,
  })
  const allow = useMutation({
    meta: { inline: true },
    mutationFn: () => api.post<FirewallState>('/api/access/firewall', { public: alsoPublic }),
    onSuccess: (data) => qc.setQueryData(['firewall'], data),
  })
  const openSettings = useMutation({ mutationFn: () => api.post('/api/access/network-settings') })

  if (fw.isPending) return <Skeleton className="mt-4 h-16 w-full" />
  if (fw.isError || !fw.data) return null
  const { status, reachable, blocked } = fw.data
  const publicNets = status.networks.filter((n) => n.category === 'Public')

  // Linux and macOS: kuro only says what it found.
  if (!status.supported) {
    if (!status.firewall) return null
    return (
      <div className="mt-4 rounded-md border border-base-800 bg-base-950/60 p-3 text-sm">
        <p className="text-base-200">{status.hint}</p>
        {status.command && (
          <code className="mt-2 block rounded bg-base-900 px-2 py-1.5 text-xs break-all text-accent-300 select-all">
            {status.command}
          </code>
        )}
      </div>
    )
  }

  return (
    <div
      className={cx(
        'mt-4 space-y-2.5 rounded-md border p-3 text-sm',
        reachable ? 'border-accent-500/30 bg-accent-500/5' : 'border-recap/40 bg-recap/10',
      )}
    >
      {reachable ? (
        <p className="text-base-200">
          ✓ Windows Firewall lets devices on {status.networks.map((n) => n.name).join(', ')} connect.
        </p>
      ) : blocked ? (
        <p className="text-base-100">
          Windows Firewall is blocking kuro, probably from an earlier Cancel on its prompt. Allowing it
          fixes that.
        </p>
      ) : publicNets.length > 0 ? (
        <p className="text-base-100">
          Windows calls <strong>{publicNets.map((n) => n.name).join(', ')}</strong> a Public network and
          keeps other devices out. If it's your home Wi-Fi, set it to <strong>Private</strong>, then allow
          kuro below.
        </p>
      ) : (
        <p className="text-base-100">Windows Firewall isn't letting other devices reach kuro yet.</p>
      )}

      {!reachable && (
        <div className="flex flex-wrap items-center gap-2">
          {publicNets.length > 0 && (
            <button
              onClick={() => openSettings.mutate()}
              className={buttonClass('secondary', 'sm')}
            >
              Open network settings
            </button>
          )}
          <button
            onClick={() => allow.mutate()}
            disabled={allow.isPending}
            className={buttonClass('primary', 'sm')}
          >
            {allow.isPending ? 'Waiting for Windows…' : 'Allow through Windows Firewall'}
          </button>
          <button
            onClick={() => fw.refetch()}
            className="rounded-md px-2 py-1.5 text-xs text-base-400 hover:text-white"
          >
            Check again
          </button>
        </div>
      )}
      {!reachable && publicNets.length > 0 && (
        <label className="flex items-center gap-2 text-xs text-base-400">
          <input type="checkbox" checked={alsoPublic} onChange={(e) => setAlsoPublic(e.target.checked)} />
          Also allow on Public networks (cafés, hotels: anyone on them could try to connect; the link's
          token still keeps them out)
        </label>
      )}
      {allow.isError && <p className="text-xs text-recap">{(allow.error as Error).message}</p>}
      <p className="text-xs text-base-500">
        Windows will ask for permission. The rule only admits devices on the same local network. Still
        can't connect? Guest networks and routers with "client isolation" keep devices apart.
      </p>
    </div>
  )
}

interface UpdateStatus {
  current: string
  latest?: { version: string; url: string; notes?: string }
  available: boolean
  checkedAt?: string
  checkError?: string
  stage: 'idle' | 'downloading' | 'verifying' | 'restarting' | 'failed'
  bytes: number
  total: number
  error?: string
}

function AboutTab() {
  const qc = useQueryClient()
  const [waiting, setWaiting] = useState(false)
  const [stuck, setStuck] = useState(false)

  const status = useQuery({
    queryKey: ['update'],
    queryFn: () => api.get<UpdateStatus>('/api/update'),
    // A dev build has no updater; that is a plain answer, not an error to retry.
    retry: false,
    refetchInterval: (q) => {
      const stage = q.state.data?.stage
      return stage === 'downloading' || stage === 'verifying' || stage === 'restarting' ? 500 : false
    },
  })
  const check = useMutation({
    mutationFn: () => api.post<UpdateStatus>('/api/update/check'),
    onSuccess: (data) => qc.setQueryData(['update'], data),
  })
  const apply = useMutation({
    meta: { inline: true },
    mutationFn: () => api.post<UpdateStatus>('/api/update/apply'),
    onSuccess: (data) => qc.setQueryData(['update'], data),
  })

  const s = status.data
  const from = s?.current

  // Once the download lands the server hands over to the new binary: poll until
  // something answers with a different version, then reload onto it. The old
  // process may be gone before 'restarting' is ever seen, so a failed poll
  // after verifying counts too.
  const handingOver =
    s?.stage === 'restarting' || (status.isError && (s?.stage === 'verifying' || s?.stage === 'downloading'))
  useEffect(() => {
    if (!handingOver || waiting) return
    setWaiting(true)
    let ticks = 0
    const timer = window.setInterval(async () => {
      // A new version that never comes up (or a rollback) must not leave the
      // page spinning for good.
      if (++ticks === 90) setStuck(true)
      try {
        const health = await api.get<{ version?: string }>('/api/health')
        if (health.version && health.version !== from) window.location.reload()
      } catch {
        // Between the old process leaving and the new one binding.
      }
    }, 1000)
    return () => window.clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [handingOver])

  if (status.isPending) return <Skeleton className="h-40 w-full" />

  // Only a build without an updater (503 "updater unavailable") is a
  // development one; any other failure is said as what it is.
  if (!s && !(status.error instanceof ApiError && status.error.status === 503)) {
    return (
      <Section title="kuro">
        <p className="text-sm text-recap">{(status.error as Error | null)?.message ?? 'Could not read the version.'}</p>
      </Section>
    )
  }
  if (!s) {
    return (
      <div className="space-y-4">
        <Section title="kuro" hint="Development build — updates are not checked.">
          <p className="text-sm text-base-400">Built from source.</p>
        </Section>
        <ComponentsSection />
        <WhereSection />
      </div>
    )
  }

  const busy = s.stage === 'downloading' || s.stage === 'verifying' || s.stage === 'restarting'
  const percent = s.total > 0 ? Math.round((s.bytes / s.total) * 100) : 0

  const line = (() => {
    switch (s.stage) {
      case 'downloading':
        return s.total > 0 ? `Downloading… ${percent}%` : 'Downloading…'
      case 'verifying':
        return 'Verifying…'
      case 'restarting':
        return stuck
          ? 'The new version has not come up. Reload this page; if that fails, start kuro again (it falls back to the previous version).'
          : 'Restarting into the new version…'
      case 'failed':
        return s.error ?? 'Update failed'
      default:
        if (s.checkError) return `Could not check: ${s.checkError}`
        if (s.available && s.latest) return `${s.latest.version} is available`
        if (s.checkedAt) return 'Up to date'
        return 'Not checked yet'
    }
  })()

  return (
    <div className="space-y-4">
      <Section title={`kuro ${s.current}`} hint="Updates replace only kuro.exe. Downloads, settings and history stay.">
        <Row label={line} hint={s.checkedAt ? `Checked ${relativeTime(Date.parse(s.checkedAt) / 1000)}` : undefined}>
          <div className="flex gap-2">
            <button
              onClick={() => check.mutate()}
              disabled={busy || check.isPending}
              className={buttonClass()}
            >
              {check.isPending ? 'Checking…' : 'Check now'}
            </button>
            {s.available && (
              <button
                onClick={() => apply.mutate()}
                disabled={busy || apply.isPending}
                className={buttonClass('primary')}
              >
                {busy ? 'Updating…' : s.stage === 'failed' ? 'Try again' : `Update to ${s.latest?.version}`}
              </button>
            )}
          </div>
        </Row>
        {s.stage === 'downloading' && s.total > 0 && <ProgressBar value={percent} />}
        {apply.isError && <p className="text-xs text-recap">{(apply.error as Error).message}</p>}
        {s.available && s.latest?.notes && (
          <pre className="whitespace-pre-wrap rounded-lg bg-base-950/60 p-3 text-xs text-base-400">{s.latest.notes}</pre>
        )}
      </Section>

      <ComponentsSection />
      <WhereSection />
    </div>
  )
}

// The folders kuro uses, and the one the user can move: only the first-run
// nudge opens the setup page, so it has to be here too.
function WhereSection() {
  const setup = useSetup()
  if (!setup.data) return null
  return (
    <Section title="Where things go" hint="Folders kuro reads and writes.">
      <WhereThingsGo setup={setup.data} />
    </Section>
  )
}

// Skipped on the first run meant unreachable: only the nudge opens the setup
// page, and only for the torrent engine.
function ComponentsSection() {
  const qc = useQueryClient()
  const setup = useSetup({
    refetchInterval: (q) =>
      (q.state.data?.progress ?? []).some((p) => p.stage !== 'done' && p.stage !== 'failed')
        ? 1000
        : false,
  })

  const install = useMutation({
    meta: { inline: true },
    mutationFn: (name: string) => api.post(`/api/setup/install/${name}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['setup'] }),
  })

  if (setup.isPending || setup.isError || !setup.data) return null
  const components = setup.data.components ?? []
  const progressFor = (name: string) =>
    (setup.data.progress ?? []).find((p) => p.component === name)

  return (
    <Section
      title="Programs kuro uses"
      hint="Downloaded on demand. Installing one takes effect straight away."
    >
      {components.map((c) => {
        const progress = progressFor(c.name)
        return (
          <Row
            key={c.name}
            label={c.label}
            hint={
              c.present
                ? `Installed${c.version ? ` · ${c.version}` : ''}`
                : `${c.size}${c.required ? ' · required' : ' · optional'}`
            }
          >
            {c.present && progress?.stage !== 'failed' && (!c.latest || c.latest === c.version) ? (
              <span className="text-sm text-accent-400">✓</span>
            ) : (
              <ComponentState
                progress={progress}
                present={c.present}
                manual={c.manual}
                version={c.version}
                latest={c.latest}
                onInstall={() => install.mutate(c.name)}
                pending={install.isPending && install.variables === c.name}
                requestError={
                  install.isError && install.variables === c.name ? (install.error as Error).message : undefined
                }
              />
            )}
          </Row>
        )
      })}
      {install.isError && (
        <p className="px-3 pb-2 text-xs text-recap">{(install.error as Error).message}</p>
      )}
    </Section>
  )
}

function JobsTab() {
  const jobs = useQuery({
    queryKey: ['jobs'],
    queryFn: () => api.get<{ jobs: Array<Record<string, unknown>> }>('/api/jobs'),
    refetchInterval: 10_000,
  })
  const qc = useQueryClient()
  // Started, not awaited: the list shows it running at once, and a job that
  // was already running says so instead of looking like nothing happened.
  const run = useMutation({
    meta: { inline: true },
    mutationFn: (name: string) => api.post(`/api/jobs/${name}`),
    onSettled: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
  const busy = run.error instanceof ApiError && run.error.status === 409 ? run.variables : undefined

  return (
    <Section title="Background jobs" hint="Syncing, release checks and data mirrors.">
      {jobs.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : (
        <ul className="divide-y divide-base-850">
          {jobs.data?.jobs?.map((job) => (
            <li key={String(job.name)} className="flex items-center justify-between gap-3 py-2">
              <div className="min-w-0">
                <p className="text-sm text-base-200">{String(job.name)}</p>
                <p className="text-xs text-base-500">
                  every {String(job.every)} · {String(job.runs)} runs
                  {Number(job.failures) > 0 && ` · ${String(job.failures)} failed`}
                </p>
                {job.lastError ? (
                  <p className="mt-0.5 truncate text-xs text-recap">{String(job.lastError)}</p>
                ) : null}
                {busy === job.name && !job.running && (
                  <p className="mt-0.5 text-xs text-base-400">Already running.</p>
                )}
              </div>
              <button
                onClick={() => run.mutate(String(job.name))}
                disabled={Boolean(job.running)}
                className="shrink-0 rounded-md bg-base-800 px-3 py-1 text-xs text-base-100 hover:bg-base-700 disabled:opacity-50"
              >
                {job.running ? 'Running' : 'Run'}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}
