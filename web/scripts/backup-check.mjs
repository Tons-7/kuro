// Browser check of the Settings additions: the library backup (export links,
// import through the file chooser), the components section, and the keep
// control on Downloads. Run through run-player-check.mjs with
// CHECK=backup-check.mjs.
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { chromium } from 'playwright'

const BASE = process.env.KURO_URL ?? 'http://127.0.0.1:4399'
const shots = process.env.SHOTS ?? '.'

let failures = 0
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`)
  if (!ok) failures++
}
const api = async (path, init) => {
  const res = await fetch(BASE + path, init)
  let body
  try {
    body = await res.json()
  } catch {
    body = undefined
  }
  return { ok: res.ok, status: res.status, body, headers: res.headers }
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })

// ---------------------------------------------------------------- backup
await page.goto(`${BASE}/settings?tab=Library`)
await page.getByText('Backup').waitFor({ timeout: 10_000 })

const jsonLink = page.locator('a', { hasText: 'JSON' })
const href = await jsonLink.getAttribute('href')
check(href?.startsWith('/api/library/export?format=json'), 'export link points at the API', href ?? '')
const exported = await api('/api/library/export')
check(
  exported.ok && exported.body?.kind === 'kuro.library',
  'export answers with a library file',
  `${exported.body?.entries?.length ?? 0} entries`,
)
check(
  String(exported.headers.get('content-disposition')).includes('attachment'),
  'export is served as a download',
)

// Import a file: one entry the seeded catalogue knows, one it does not.
const known = exported.body?.entries?.[0]?.animeId ?? 127230
const file = join(tmpdir(), 'kuro-backup-check.json')
writeFileSync(
  file,
  JSON.stringify({
    kind: 'kuro.library',
    version: 1,
    entries: [
      { animeId: known, progress: 1, status: 'CURRENT' },
      { animeId: 0, title: 'no id' },
    ],
  }),
)
await page.locator('input[type=file]').setInputFiles(file)
await page.getByText(/Imported 1 entry/).waitFor({ timeout: 10_000 })
check(true, 'import through the chooser reports what it applied')
const after = await api('/api/library/export')
check(
  (after.body?.entries ?? []).some((e) => e.animeId === known && e.progress >= 1),
  'the imported entry is in the library',
)
await page.screenshot({ path: join(shots, 'backup.png') })

// ---------------------------------------------------------------- components
await page.goto(`${BASE}/settings?tab=About`)
await page.getByText('Programs kuro uses').waitFor({ timeout: 10_000 })
const rows = await page.locator('text=Torrent engine').count()
check(rows === 1, 'components are listed in Settings, not only on the setup page')
await page.screenshot({ path: join(shots, 'components.png') })

// ---------------------------------------------------------------- keep control
const downloads = await api('/api/downloads')
const first = downloads.body?.items?.[0]
if (first) {
  await page.goto(`${BASE}/downloads`)
  const button = page.locator('button', { hasText: /^(✓ Kept|Keep)$/ }).first()
  await button.waitFor({ timeout: 10_000 })
  const before = (await button.textContent())?.trim()
  await button.click()
  await page.waitForTimeout(500)
  const afterClick = (await button.textContent())?.trim()
  check(before !== afterClick, 'the keep control flips on one click', `${before} -> ${afterClick}`)
  const row = (await api('/api/downloads')).body?.items?.find((d) => d.infoHash === first.infoHash)
  check(row && row.kept === (afterClick === '✓ Kept'), 'and the server agrees')
  await button.click()
  await page.waitForTimeout(500)
  check(((await button.textContent())?.trim() ?? '') === before, 'and back again')
} else {
  console.log('     (no downloads seeded; keep control not exercised)')
}

await browser.close()
console.log(failures ? `\n${failures} check(s) failed` : '\nall checks passed')
process.exit(failures ? 1 : 0)
