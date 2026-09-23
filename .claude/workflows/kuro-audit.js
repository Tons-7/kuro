export const meta = {
  name: 'kuro-audit',
  description: 'Audit kuro backend + frontend for bugs, improvements and UI/UX gaps, adversarially verify each finding',
  phases: [
    { title: 'Find', detail: 'one auditor per subsystem, plus UI/UX reviewers' },
    { title: 'Verify', detail: 'skeptics try to refute every finding; high/critical get two lenses' },
    { title: 'Critic', detail: 'look for under-covered areas and audit them too' },
  ],
}

const CONTEXT = `kuro is a Windows-first anime app at D:\\kuro. Go backend (internal/, cmd/kuro) serving a React SPA (web/src, embedded into kuro.exe at build). It searches public torrent indexers, scores releases (internal/score), downloads through an rqbit sidecar (HTTP API on 127.0.0.1:3030, rqbit persists its own session), plays via ffmpeg->HLS in the browser or via mpv/VLC, and syncs progress to AniList/MAL. SQLite store in internal/store; migrations in internal/db/migrations (applied migrations are never edited, fixes go in new files). Owner's design rules: highest quality always; a labelled single episode beats a season pack; per-show preferences override the global ones; code comments must be terse. Recent incident: a second kuro instance shared rqbit's session and its cache sweeper deleted the user's downloaded files, so anything that can delete or orphan user files deserves extra scrutiny.`

const RULES = `Hard rules:
- READ-ONLY. Do not create, modify or delete any file under D:\\kuro. No git commit/push/checkout/stash/reset.
- Do NOT run kuro.exe, rqbit, npm build, the e2e harness (web/scripts/*.mjs) or live tests (KURO_LIVE). Never contact 127.0.0.1:4321 or 127.0.0.1:3030: that is the user's live app and torrent engine.
- Allowed: reading files, grep, git log/blame/show, go vet, and running EXISTING unit tests for one package (go test ./internal/<pkg> -run <Name>).
- Be concrete. Every item needs the exact file and line, a concrete trigger -> outcome, and evidence quoted or traced from the current code. No speculative "might" items without a traced path.`

const BUG_LENS = 'Real bugs (wrong result, crash, race, leak, data loss), missing error handling that causes a user-visible failure, security holes, performance problems with a concrete cost, and simplifications that remove real complexity. Not style nits. kind is "bug" for defects, "improvement" for performance/simplification/robustness.'
const FE_BUG_LENS = 'Real frontend bugs: stale or wrong data shown, state that desyncs from the server, effects that leak or double-fire, races between requests, broken navigation or URL state, unhandled errors that leave the UI stuck, and performance issues with a concrete cost. kind is "bug" or "improvement".'
const UI_LENS = 'UI/UX improvements a user would notice: missing or confusing feedback (loading, empty, error, success states), unclear labels or affordances, hierarchy/layout problems, narrow-width breakage (<=400px), accessibility (keyboard access, focus visibility, labels, contrast), destructive actions without confirmation or undo, features that are hard to discover. You are reading code, not seeing the rendered page: ground every claim in the markup/classes you read. Each item names the exact component file and line and the concrete change. kind must be "ui". Severity: high = broken or confusing for most users, medium = noticeable friction, low = polish.'

const AREAS = [
  { name: 'playback', files: 'internal/library/play.go, prefetch.go, queue.go, watcher.go; internal/server/play.go, watch.go', focus: 'starting playback, trying release candidates in turn, prefetching the next episode, the download queue, the watcher that auto-downloads new episodes', lens: BUG_LENS },
  { name: 'release-finding', files: 'internal/library/episode.go, identity.go, corpus.go; internal/score/; internal/parse/; internal/match/; internal/corpus/', focus: 'searching indexers for an episode, parsing release names, matching releases to the right show/cour/episode, and ranking them', lens: BUG_LENS + ' Include misparses and misranks: think of real release naming shapes that would parse or rank wrong.' },
  { name: 'downloads-lifecycle', files: 'internal/library/cache.go; internal/store/cache.go, queue.go; internal/torrent/; internal/server/download.go and the downloads/cache handlers', focus: 'download records, the kept tier, the cache-budget sweeper, auto-delete, orphan cleanup, reconciling with rqbit across restarts, the rqbit supervisor', lens: BUG_LENS + ' Anything that can delete or orphan a user file, or lose a download record, is at least high severity.' },
  { name: 'store-sql', files: 'internal/store/ (library.go, playback.go, episodes.go, franchise.go, prefs.go, sync.go, notify.go, local.go, history.go, tracker.go, corpus.go); internal/db/ and internal/db/migrations/', focus: 'SQL correctness, transactions, NULL handling, pagination totals, indexes, migration safety', lens: BUG_LENS },
  { name: 'http-api-security', files: 'internal/server/ (server.go, access.go, setup.go, prefs.go, local.go, browse.go, discover.go, random.go, schedule.go, malextra.go, corpus.go and the other handlers)', focus: 'HTTP handlers: input validation, error handling, the LAN/remote access model and its tokens, path handling for local files, requests forged by other websites against the local server, information leaks', lens: BUG_LENS + ' For security, state concretely who can reach the endpoint and what they can do with it.' },
  { name: 'streaming-transcode', files: 'internal/server/stream.go; internal/transcode/; internal/player/', focus: 'ffmpeg HLS sessions, segment serving, seek restarts, subtitle and font extraction, thumbnails, external players (mpv, VLC) and their control channels', lens: BUG_LENS + ' Look hard at process and goroutine lifecycles, leaks, and races between concurrent requests.' },
  { name: 'trackers-metadata', files: 'internal/anilist/; internal/mal/; internal/metadata/; internal/library/sync.go, enrich.go, import.go, mal.go; internal/store/tracker.go', focus: 'AniList/MAL auth and token refresh, list import and two-way sync, rate limiting, metadata enrichment', lens: BUG_LENS },
  { name: 'platform-startup', files: 'cmd/kuro/main.go; internal/config/; internal/deps/; internal/update/; internal/proc/; internal/jobs/; internal/indexer/', focus: 'startup order, config loading, first-run dependency downloads, the self-updater (download, checksum, swap), background jobs, indexer HTTP/RSS parsing', lens: BUG_LENS + ' For the updater and dependency installer, check integrity verification and failure recovery.' },
  { name: 'player-ui-logic', files: 'web/src/player/ (Player.tsx, hooks.ts and the rest); web/src/pages/Watch.tsx', focus: 'the browser player: HLS lifecycle, effects and listeners, keyboard shortcuts, fullscreen, auto-next, subtitles, progress reporting', lens: FE_BUG_LENS },
  { name: 'pages-logic', files: 'web/src/pages/ (everything except Watch.tsx)', focus: 'page logic: data fetching, query keys and invalidation, URL state, mutations, error and loading handling', lens: FE_BUG_LENS },
  { name: 'components-lib', files: 'web/src/components/; web/src/lib/', focus: 'shared components and the API/query layer', lens: FE_BUG_LENS },
  { name: 'ui-discovery', files: 'web/src/pages/Home.tsx, Browse.tsx, Anime.tsx and the Library/Schedule/Recent pages; web/src/components/ PosterCard, HoverInfo, Rail, CharacterRail, Hero, Sidebar, EpisodeList, the search in Layout.tsx', focus: 'browsing and discovery screens', lens: UI_LENS },
  { name: 'ui-watch-manage', files: 'web/src/pages/Watch.tsx and the player controls in web/src/player/; Manage.tsx (Downloads); Settings.tsx; History.tsx; the Setup page; web/src/components/NotificationPanel.tsx; Layout.tsx header and nav', focus: 'watching, downloads, settings, history, setup and the app chrome', lens: UI_LENS },
  { name: 'ui-consistency', files: 'all of web/src, starting with web/src/components/ui.tsx and the CSS/theme files', focus: 'cross-cutting design consistency', lens: UI_LENS + ' Focus on inconsistencies ACROSS screens: competing button/badge/card styles, spacing and type-scale drift, colour/contrast tokens, icon usage, repeated markup that should be one component, and patterns handled well on one screen but badly on another.' },
]

const FINDINGS = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          title: { type: 'string' },
          kind: { type: 'string', enum: ['bug', 'improvement', 'ui'] },
          category: { type: 'string' },
          severity: { type: 'string', enum: ['critical', 'high', 'medium', 'low'] },
          file: { type: 'string' },
          line: { type: 'integer' },
          scenario: { type: 'string' },
          evidence: { type: 'string' },
          fix: { type: 'string' },
        },
        required: ['title', 'kind', 'category', 'severity', 'file', 'line', 'scenario', 'evidence', 'fix'],
      },
    },
    omitted: { type: 'integer' },
  },
  required: ['findings', 'omitted'],
}

const VERDICT = {
  type: 'object',
  properties: {
    verdict: { type: 'string', enum: ['confirmed', 'refuted', 'uncertain'] },
    severity: { type: 'string', enum: ['critical', 'high', 'medium', 'low'] },
    reasoning: { type: 'string' },
    line: { type: 'integer' },
  },
  required: ['verdict', 'severity', 'reasoning'],
}

const GAPS = {
  type: 'object',
  properties: {
    gaps: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          name: { type: 'string' },
          files: { type: 'string' },
          focus: { type: 'string' },
          ui: { type: 'boolean' },
        },
        required: ['name', 'files', 'focus', 'ui'],
      },
    },
  },
  required: ['gaps'],
}

const finderPrompt = (a) => `${CONTEXT}

You are auditing the "${a.name}" area of kuro: ${a.focus}.
Files: ${a.files}

Look for: ${a.lens}

${RULES}

Report the most important items first, at most 15. Put the number of lower-value items you left out in "omitted".`

const LENS_TEXT = {
  correctness: 'Your job is to REFUTE it if you can. Read the cited code and whatever surrounds and calls it. Refute if the scenario cannot happen (a guard, caller or invariant prevents it), if the code does not say what the report claims, or if it is already handled elsewhere. Confirm only if you can trace it in the current code. If you genuinely cannot decide, answer uncertain.',
  trace: 'Independently trace it end to end: start from the user action, request or scheduled job that would trigger it, follow the exact calls and state, and name the precise line where the outcome goes wrong. If any step of the path is blocked, refute it.',
  ui: 'Check this UI/UX suggestion against the current frontend code. Refute if the claim about the current UI is inaccurate (the element, state, label or style is actually present or handled), if it is already implemented elsewhere, or if it is too vague to act on. Confirm if the gap is real and the change is concrete. Judge accuracy and actionability, not taste.',
}

const verifyPrompt = (f, lens) => `${CONTEXT}

Verify this reported ${f.kind} in kuro. ${LENS_TEXT[lens]}
Give the severity you would assign, and the corrected line if the cited one is off.

${RULES}

Reported item:
${JSON.stringify(f, null, 2)}`

const verifyAll = (res, a) => {
  if (!res) return []
  if (res.omitted) log(`${a.name}: ${res.omitted} lower-value item(s) left out by the finder`)
  log(`${a.name}: ${res.findings.length} to verify`)
  return parallel(res.findings.map((f, i) => () => {
    const lenses = f.kind === 'ui' ? ['ui']
      : (f.severity === 'critical' || f.severity === 'high') ? ['correctness', 'trace'] : ['correctness']
    return parallel(lenses.map((l) => () =>
      agent(verifyPrompt(f, l), { label: `verify:${a.name}#${i + 1}:${l}`, phase: 'Verify', schema: VERDICT, effort: 'high' })))
      .then((vs) => ({ ...f, area: a.name, verdicts: vs.filter(Boolean) }))
  }))
}

const ORDER = ['low', 'medium', 'high', 'critical']
const classify = (f) => {
  const v = f.verdicts || []
  const yes = v.filter((x) => x.verdict === 'confirmed')
  const no = v.filter((x) => x.verdict === 'refuted')
  let status = 'refuted'
  if (yes.length && !no.length) status = 'confirmed'
  else if (yes.length && no.length) status = 'disputed'
  else if (!no.length && v.length) status = 'uncertain'
  const severity = yes.length
    ? yes.map((x) => x.severity).sort((p, q) => ORDER.indexOf(p) - ORDER.indexOf(q))[0]
    : f.severity
  return { ...f, status, severity, reportedSeverity: f.severity }
}

const norm = (p) => String(p || '').replace(/\\/g, '/').replace(/^.*?(internal\/|cmd\/|web\/)/, '$1').toLowerCase()
const dedup = (items) => {
  const kept = []
  const sorted = [...items].sort((p, q) => ORDER.indexOf(q.severity) - ORDER.indexOf(p.severity))
  for (const f of sorted) {
    const dup = kept.find((k) => norm(k.file) === norm(f.file) && Math.abs((k.line || 0) - (f.line || 0)) <= 3 && k.kind === f.kind)
    if (dup) { dup.alsoFoundBy = [...(dup.alsoFoundBy || []), f.area]; continue }
    kept.push(f)
  }
  return kept
}

phase('Find')
const round1 = await pipeline(
  AREAS,
  (a) => agent(finderPrompt(a), { label: `find:${a.name}`, phase: 'Find', schema: FINDINGS }),
  (res, a) => verifyAll(res, a),
)
let all = round1.filter(Boolean).flat().filter(Boolean)
log(`round 1: ${all.length} items verified`)

phase('Critic')
const summary = all.map((f) => `${f.area} | ${norm(f.file)}:${f.line} | ${f.title}`).join('\n')
const critic = await agent(`${CONTEXT}

An audit of kuro just ran over these areas:
${AREAS.map((a) => `- ${a.name}: ${a.files}`).join('\n')}

It produced these items (area | file:line | title):
${summary}

Find what the audit under-covered. Run \`git ls-files internal cmd web/src\` and compare: which source files or risk types got little or no scrutiny (files in no area, subsystems with suspiciously few findings, whole classes of issue nobody looked for)? Return up to 4 gaps worth a dedicated auditor, each with the files, the focus, and ui=true if it is a UI/UX review. Return an empty list if coverage is genuinely adequate.

${RULES}`, { label: 'critic', phase: 'Critic', schema: GAPS })

const gaps = (critic?.gaps || []).slice(0, 4).map((g) => ({
  name: `gap-${g.name}`, files: g.files, focus: g.focus, lens: g.ui ? UI_LENS : BUG_LENS,
}))
if (gaps.length) {
  log(`critic found ${gaps.length} gap(s): ${gaps.map((g) => g.name).join(', ')}`)
  const round2 = await pipeline(
    gaps,
    (a) => agent(finderPrompt(a), { label: `find:${a.name}`, phase: 'Find', schema: FINDINGS }),
    (res, a) => verifyAll(res, a),
  )
  all = all.concat(round2.filter(Boolean).flat().filter(Boolean))
}

const judged = all.map(classify)
const confirmed = dedup(judged.filter((f) => f.status === 'confirmed'))
const disputed = judged.filter((f) => f.status === 'disputed' || f.status === 'uncertain')
const refuted = judged.filter((f) => f.status === 'refuted')
log(`confirmed ${confirmed.length} (after dedup), disputed/uncertain ${disputed.length}, refuted ${refuted.length}`)

return {
  confirmed,
  disputed,
  refuted: refuted.map((f) => ({ area: f.area, file: f.file, line: f.line, title: f.title, why: (f.verdicts || []).map((v) => v.reasoning).join(' | ').slice(0, 400) })),
  gaps: gaps.map((g) => g.name),
}
