import { parseNotes, spans } from '../lib/notes'

export interface Notes {
  version: string
  notes: string
  published?: string
}

/** Each release's notes under its version, the newest open; older ones fold so a long jump stays readable. */
export function ReleaseNotesList({ notes }: { notes: Notes[] }) {
  const shown = notes.filter((n) => n.notes.trim())
  if (shown.length === 0) return null
  return (
    <div className="space-y-2">
      {shown.map((n, i) => (
        <details key={n.version} open={i === 0} className="group rounded-lg bg-base-950/60 ring-1 ring-white/[0.05]">
          <summary className="flex cursor-pointer list-none items-baseline gap-2 px-3 py-2 text-sm font-medium text-base-100 [&::-webkit-details-marker]:hidden">
            <span aria-hidden className="text-base-500 transition-transform group-open:rotate-90">
              ›
            </span>
            kuro {n.version}
            {n.published && <span className="text-xs font-normal text-base-500">{day(n.published)}</span>}
          </summary>
          <div className="px-3 pb-3">
            <ReleaseNotes text={n.notes} />
          </div>
        </details>
      ))}
    </div>
  )
}

function day(iso: string) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}

function Line({ text }: { text: string }) {
  return spans(text).map((s, i) =>
    s.kind === 'bold' ? (
      <strong key={i} className="font-semibold text-base-100">
        {s.text}
      </strong>
    ) : s.kind === 'code' ? (
      <code key={i} className="rounded bg-base-800 px-1 font-mono text-[0.85em] text-base-200">
        {s.text}
      </code>
    ) : (
      s.text
    ),
  )
}

export function ReleaseNotes({ text }: { text: string }) {
  return (
    <div className="space-y-2 text-sm leading-relaxed text-base-300">
      {parseNotes(text).map((b, i) => {
        if (b.kind === 'h') {
          return (
            <h4 key={i} className="pt-1 text-sm font-semibold text-white">
              <Line text={b.text} />
            </h4>
          )
        }
        if (b.kind === 'p') {
          return (
            <p key={i}>
              <Line text={b.text} />
            </p>
          )
        }
        const List = b.kind === 'ul' ? 'ul' : 'ol'
        return (
          <List key={i} className={`${b.kind === 'ul' ? 'list-disc' : 'list-decimal'} space-y-1 pl-5 marker:text-base-600`}>
            {b.items.map((item, j) => (
              <li key={j}>
                <Line text={item} />
              </li>
            ))}
          </List>
        )
      })}
    </div>
  )
}
