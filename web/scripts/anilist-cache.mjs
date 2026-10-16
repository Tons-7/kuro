// Caching AniList stand-in for the checks: clean answers kept a day in %TEMP%/kuro-e2e-anilist.
//   node scripts/anilist-cache.mjs <port>     (run-player-check starts and stops it)
import { createServer } from 'node:http'
import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'

const UPSTREAM = 'https://graphql.anilist.co'
const DAY = 24 * 60 * 60 * 1000
const dir = join(tmpdir(), 'kuro-e2e-anilist')
mkdirSync(dir, { recursive: true })

const cached = (file) => {
  try {
    if (Date.now() - statSync(file).mtimeMs < DAY) return readFileSync(file)
  } catch {
    // Not kept yet.
  }
  return null
}

const server = createServer(async (req, res) => {
  const chunks = []
  for await (const c of req) chunks.push(c)
  const body = Buffer.concat(chunks)
  // A signed-in answer is that viewer's alone, so the token is part of the key.
  const key = createHash('sha1').update(req.headers.authorization ?? '').update(body).digest('hex')
  const file = join(dir, `${key}.json`)

  const hit = req.method === 'POST' ? cached(file) : null
  if (hit) {
    res.writeHead(200, { 'Content-Type': 'application/json' }).end(hit)
    return
  }

  try {
    const headers = { 'Content-Type': 'application/json', Accept: 'application/json' }
    for (const h of ['authorization', 'referer', 'user-agent']) if (req.headers[h]) headers[h] = req.headers[h]
    const up = await fetch(UPSTREAM + (req.url === '/' ? '' : req.url), {
      method: req.method,
      headers,
      body: req.method === 'POST' ? body : undefined,
    })
    const answer = Buffer.from(await up.arrayBuffer())
    const pass = {}
    for (const h of ['content-type', 'retry-after', 'x-ratelimit-remaining', 'x-ratelimit-reset']) {
      if (up.headers.get(h)) pass[h] = up.headers.get(h)
    }
    if (req.method === 'POST' && up.status === 200 && !answer.includes('"errors"')) {
      // Renamed into place: another lane's proxy may be reading the same file.
      const tmp = `${file}.${process.pid}.tmp`
      writeFileSync(tmp, answer)
      renameSync(tmp, file)
    }
    res.writeHead(up.status, pass).end(answer)
  } catch (err) {
    res.writeHead(502, { 'Content-Type': 'application/json' }).end(JSON.stringify({ errors: [{ message: String(err) }] }))
  }
})

server.listen(Number(process.argv[2]), '127.0.0.1')
