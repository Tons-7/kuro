# kuro web

The single-page app kuro serves. React 19, TanStack Query, Tailwind v4, built
with Vite. `npm run build` writes `dist/`, which `embed.go` bakes into the Go
binary, so a frontend change needs a fresh `go build` too.

```sh
npm run dev                 # Vite on its own port, proxying /api to a running kuro
npm run typecheck           # tsgo -b --force
npm run lint                # oxlint
npm test                    # browser checks against an isolated scratch kuro
```

`src/pages` are the routes, `src/components` shared UI, `src/player` the
browser player (hls.js, subtitle renderer, Anime4K), `src/lib` the API client,
queries and formatting.

Browser checks live in `scripts/` and run through `scripts/run-player-check.mjs
<check>`, which builds kuro and starts it in `%TEMP%\kuro-e2e` on port 4399 with
its own data, cache and peer port. `npm run release-check` runs the Go suite and
all of them that need no release sites. Never point a check at a running kuro.
