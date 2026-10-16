// Release notes as the About tab reads them, run straight by Node: node scripts/notes-check.mjs
const { parseNotes, spans } = await import(new URL('../src/lib/notes.ts', import.meta.url))

let failures = 0
const check = (got, want, label) => {
  const [g, w] = [JSON.stringify(got), JSON.stringify(want)]
  if (g === w) return console.log(`ok   ${label}`)
  failures++
  console.log(`FAIL ${label}: got ${g}, want ${w}`)
}

check(
  parseNotes('## Downloads\r\n- one\n- two\n  carried on\n\nA closing line\nwrapped.'),
  [
    { kind: 'h', text: 'Downloads' },
    { kind: 'ul', items: ['one', 'two carried on'] },
    { kind: 'p', text: 'A closing line wrapped.' },
  ],
  'headings, bullets with a wrapped line, a paragraph',
)
check(
  parseNotes('1. first\n2) second\n* star'),
  [
    { kind: 'ol', items: ['first', 'second'] },
    { kind: 'ul', items: ['star'] },
  ],
  'numbered and starred lists stay apart',
)
check(parseNotes(''), [], 'empty notes are nothing')
check(
  spans('Use **Keep all** or `F`, as [the downloads page](https://github.com/Tons-7/kuro/pull/1) says.'),
  [
    { kind: 'text', text: 'Use ' },
    { kind: 'bold', text: 'Keep all' },
    { kind: 'text', text: ' or ' },
    { kind: 'code', text: 'F' },
    { kind: 'text', text: ', as ' },
    { kind: 'text', text: 'the downloads page' },
    { kind: 'text', text: ' says.' },
  ],
  'bold and code; a link keeps only its words, no address',
)
check(
  spans('<b>x</b> <a href="https://github.com">y</a>'),
  [{ kind: 'text', text: '<b>x</b> <a href="https://github.com">y</a>' }],
  'raw HTML stays plain text, never markup',
)

console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
