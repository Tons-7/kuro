export function clockTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '0:00'
  const total = Math.floor(seconds)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}

export function bytes(n: number): string {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1)
  return `${(n / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

/** "in 2h 14m" / "3d ago" — the schedule and episode lists both want this. */
export function relativeTime(unix: number): string {
  const delta = unix * 1000 - Date.now()
  const abs = Math.abs(delta)
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour

  let text: string
  if (abs < hour) text = `${Math.max(1, Math.round(abs / minute))}m`
  else if (abs < day) {
    const h = Math.floor(abs / hour)
    const m = Math.round((abs % hour) / minute)
    text = m > 0 ? `${h}h ${m}m` : `${h}h`
  } else text = `${Math.round(abs / day)}d`

  return delta >= 0 ? `in ${text}` : `${text} ago`
}

export function airTime(unix: number): string {
  return new Date(unix * 1000).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** AniList gives each anime a dominant cover colour; it makes a good accent. */
export function tint(color?: string | null, alpha = 1): string | undefined {
  if (!color || !/^#[0-9a-f]{6}$/i.test(color)) return undefined
  if (alpha >= 1) return color
  const n = parseInt(color.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ')
}

// ffmpeg reports ISO 639-2/B, which Intl does not accept for the languages
// where the bibliographic and terminological codes differ — "ger" rather than
// "deu". Only the ones that actually turn up on anime releases are listed.
const BIBLIOGRAPHIC: Record<string, string> = {
  ger: 'de', fre: 'fr', dut: 'nl', gre: 'el', chi: 'zh', cze: 'cs',
  ice: 'is', mac: 'mk', mao: 'mi', may: 'ms', per: 'fa', rum: 'ro',
  slo: 'sk', tib: 'bo', wel: 'cy', arm: 'hy', baq: 'eu', bur: 'my', geo: 'ka',
}

/**
 * The language a subtitle track is in, spelled out. Release groups label every
 * track with their own name — Erai-raws calls all of them "CR" — so a picker
 * showing titles offers several identical rows and no way to choose.
 */
export function languageName(code?: string | null): string | undefined {
  const raw = code?.trim().toLowerCase()
  if (!raw || raw === 'und') return undefined

  const tag = BIBLIOGRAPHIC[raw] ?? raw
  try {
    const name = new Intl.DisplayNames(['en'], { type: 'language' }).of(tag)
    // Intl echoes the input back when it does not recognise it.
    if (name && name.toLowerCase() !== tag) return name
  } catch {
    // An malformed code throws rather than returning; fall through to the code.
  }
  return raw.toUpperCase()
}
