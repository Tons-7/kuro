export type Block =
  | { kind: 'h'; text: string }
  | { kind: 'p'; text: string }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[] }

// The markdown release notes use: headings, bullets, numbered lists and paragraphs. Nothing becomes HTML.
export function parseNotes(text: string): Block[] {
  const blocks: Block[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length) blocks.push({ kind: 'p', text: para.join(' ') })
    para = []
  }
  for (const raw of text.replace(/\r\n?/g, '\n').split('\n')) {
    const line = raw.trim()
    const heading = /^#{1,6}\s+(.*)$/.exec(line)
    const item = /^[-*+]\s+(.*)$/.exec(line) ?? /^\d+[.)]\s+(.*)$/.exec(line)
    const last = blocks.at(-1)
    if (!line) {
      flush()
    } else if (heading) {
      flush()
      blocks.push({ kind: 'h', text: heading[1] })
    } else if (item) {
      flush()
      const kind = /^[-*+]/.test(line) ? 'ul' : 'ol'
      if (last && last.kind === kind) last.items.push(item[1])
      else blocks.push({ kind, items: [item[1]] })
    } else if (para.length === 0 && raw.startsWith(' ') && last && (last.kind === 'ul' || last.kind === 'ol')) {
      // A wrapped line carries on the item above it, as markdown reads it.
      last.items[last.items.length - 1] += ` ${line}`
    } else {
      para.push(line)
    }
  }
  flush()
  return blocks
}

export type Span = { kind: 'text' | 'bold' | 'code'; text: string }

const INLINE = /(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)\s]+\))/g

/** Bold and code within a line. A link keeps only its words: the notes never send anyone to the repository. */
export function spans(text: string): Span[] {
  const out: Span[] = []
  for (const part of text.split(INLINE)) {
    if (!part) continue
    const link = /^\[([^\]]+)\]\([^)\s]+\)$/.exec(part)
    if (link) out.push({ kind: 'text', text: link[1] })
    else if (/^\*\*[^*]+\*\*$/.test(part)) out.push({ kind: 'bold', text: part.slice(2, -2) })
    else if (/^`[^`]+`$/.test(part)) out.push({ kind: 'code', text: part.slice(1, -1) })
    else out.push({ kind: 'text', text: part })
  }
  return out
}
