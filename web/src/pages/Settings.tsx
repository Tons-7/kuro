import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { api, withToken } from '../lib/api'
import { bytes, cx, relativeTime } from '../lib/format'
import { usePrefs, useSetPref, useSetup } from '../lib/queries'
import { ANIME4K_MODES, ANIME4K_SIZES } from '../components/Anime4KDialog'
import { ComponentState } from '../components/ComponentState'
import { DESKTOP_NOTIFY_KEY, desktopNotifyWanted } from '../components/NotificationPanel'
import { ProgressBar, Segmented, Select, Skeleton, Spinner } from '../components/ui'

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

  return (
    <div className="space-y-5">
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

      <Segmented
        options={TABS.map((name) => ({ value: name, label: name }))}
        value={tab}
        onChange={setTab}
      />

      <div className="animate-fade-in">
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
  )
}

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section className="surface p-4">
      <h2 className="text-sm font-semibold text-base-100">{title}</h2>
      {hint && <p className="mt-0.5 mb-3 text-xs text-base-500">{hint}</p>}
      <div className={cx('space-y-3', !hint && 'mt-3')}>{children}</div>
    </section>
  )
}

function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <p className="text-sm text-base-200">{label}</p>
        {hint && <p className="text-xs text-base-500">{hint}</p>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function Switch({ on, onChange, label }: { on: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <button
      role="switch"
      aria-checked={on}
      aria-label={label}
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
        <Row label="Default player" hint="mpv is desktop only; a phone or TV needs the browser">
          <Select
            value={f.value('playback.player')}
            onChange={(v) => f.set('playback.player', v)}
          >
            <option value="browser">In browser</option>
            <option value="mpv">mpv</option>
          </Select>
        </Row>
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

function QualityTab() {
  const f = useFlag()
  const qc = useQueryClient()
  const cache = useQuery({ queryKey: ['cache'], queryFn: () => api.get<Record<string, number>>('/api/cache') })
  const sweep = useMutation({
    mutationFn: () => api.post('/api/cache/sweep'),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['cache'] }),
  })
  const orphans = useMutation({
    mutationFn: () => api.post<{ removed: number; freedBytes: number }>('/api/cache/orphans'),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['cache'] }),
  })

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
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
          >
            {sweep.isPending ? 'Sweeping…' : 'Sweep now'}
          </button>
        </Row>
        <Row
          label="Orphaned files"
          hint={
            orphans.data
              ? `Removed ${orphans.data.removed} · freed ${bytes(orphans.data.freedBytes)}`
              : 'Folders in the cache the torrent engine no longer knows about — left by a crash or a delete outside kuro. They escape the budget until removed.'
          }
        >
          <button
            onClick={() => orphans.mutate()}
            disabled={orphans.isPending}
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700 disabled:opacity-50"
          >
            {orphans.isPending ? 'Cleaning…' : 'Clean up'}
          </button>
        </Row>
        {orphans.isError && <p className="text-xs text-recap">{(orphans.error as Error).message}</p>}
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
    mutationFn: () =>
      api.post<{ found: number; matched: number; favourited: number; unmatched: number }>(
        '/api/mal/favourites/import',
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['bookmarks'] }),
  })

  const status = tracker.connected
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
              className="rounded-md bg-accent-500 px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-600"
            >
              {tracker.connected ? 'Reconnect' : 'Connect'}
            </a>
          )}
          <button
            onClick={() => setEditing((open) => !open)}
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
          >
            {editing ? 'Cancel' : tracker.configured ? 'Edit keys' : 'Add keys'}
          </button>
          {tracker.provider === 'mal' && tracker.connected && (
            <button
              onClick={() => importFavs.mutate()}
              disabled={importFavs.isPending}
              title="MyAnimeList's API cannot sync favourites; this copies them over once"
              className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700 disabled:opacity-50"
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
                className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-300 hover:bg-base-700 hover:text-white"
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
              className="rounded-md bg-accent-500 px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-600 disabled:opacity-50"
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
            className="rounded-md bg-red-500/90 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-500"
          >
            {sync.isPending ? 'Replacing…' : 'Yes, replace'}
          </button>
          <button
            onClick={() => setConfirming(false)}
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
          >
            Cancel
          </button>
        </div>
      ) : (
        <button
          onClick={() => (mode === 'replace' ? setConfirming(true) : sync.mutate())}
          disabled={sync.isPending}
          className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
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
          className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
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
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
          >
            JSON
          </a>
          <a
            href={exportURL('txt')}
            download
            className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700"
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
      }>('/api/access'),
  })

  // Moves the listener there and then, so the phone can be tried immediately.
  const setNetwork = useMutation({
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
      ) : !access.data?.reachable ? (
        <p className="text-sm text-base-400">
          Only this machine can reach kuro right now.
        </p>
      ) : (
        <div className="flex flex-wrap items-center gap-5">
          <img
            src="/api/access/qr.svg"
            alt="Pairing QR code"
            className="size-40 rounded-lg bg-white p-2"
          />
          <div className="min-w-0">
            <p className="mb-1 text-xs text-base-500">Scan, or open this on the device:</p>
            {access.data.urls.map((url) => (
              <code key={url} className="block truncate text-xs text-accent-400">{url}</code>
            ))}
          </div>
        </div>
      )}
    </Section>
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

  const status = useQuery({
    queryKey: ['update'],
    queryFn: () => api.get<UpdateStatus>('/api/update'),
    // A dev build has no updater; that is a plain answer, not an error to retry.
    retry: false,
    refetchInterval: (q) => {
      const stage = q.state.data?.stage
      return stage === 'downloading' || stage === 'verifying' ? 500 : false
    },
  })
  const check = useMutation({
    mutationFn: () => api.post<UpdateStatus>('/api/update/check'),
    onSuccess: (data) => qc.setQueryData(['update'], data),
  })
  const apply = useMutation({
    mutationFn: () => api.post<UpdateStatus>('/api/update/apply'),
    onSuccess: (data) => qc.setQueryData(['update'], data),
  })

  const s = status.data
  const from = s?.current

  // Once the download lands the server hands over to the new binary: poll until
  // something answers with a different version, then reload onto it.
  useEffect(() => {
    if (s?.stage !== 'restarting' || waiting) return
    setWaiting(true)
    const timer = window.setInterval(async () => {
      try {
        const health = await api.get<{ version?: string }>('/api/health')
        if (health.version && health.version !== from) window.location.reload()
      } catch {
        // Between the old process leaving and the new one binding.
      }
    }, 1000)
    return () => window.clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s?.stage])

  if (status.isPending) return <Skeleton className="h-40 w-full" />

  if (status.isError || !s) {
    return (
      <div className="space-y-4">
        <Section title="kuro" hint="Development build — updates are not checked.">
          <p className="text-sm text-base-400">Built from source.</p>
        </Section>
        <ComponentsSection />
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
        return 'Restarting into the new version…'
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
              className="rounded-md bg-base-800 px-3 py-1.5 text-sm text-base-100 hover:bg-base-700 disabled:opacity-50"
            >
              {check.isPending ? 'Checking…' : 'Check now'}
            </button>
            {s.available && (
              <button
                onClick={() => apply.mutate()}
                disabled={busy || apply.isPending}
                className="rounded-md bg-accent-500 px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-600 disabled:opacity-50"
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
    </div>
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
  const run = useMutation({ mutationFn: (name: string) => api.post(`/api/jobs/${name}`) })

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
