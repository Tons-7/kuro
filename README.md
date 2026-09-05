# kuro

Self-hosted anime streaming. Search a show, press play, and it streams from a
torrent while it downloads — no waiting for a complete file. Progress syncs to
AniList and MyAnimeList as you watch.

Everything runs on your own machine. One binary, one config file.

## What it does

- **Streams while downloading.** Seeking into a part downloaded region works;
  the rest is fetched in the background so the next episode is already there.
- **Plays anywhere.** In the browser by default, so a phone or TV on the same
  network works the same as the laptop. mpv is available as an external player.
- **Knows 34,141 anime**, including the ~11,700 that exist on MyAnimeList but
  not AniList.
- **Marks filler and recap episodes**, so a rewatch can skip them.
- **Skips openings and endings**, if you ask it to. Nothing is automatic by
  default.
- **Plays files you already have.** Point it at a folder and matched episodes
  play instantly with no torrent involved.
- **Tracks progress** on AniList and MyAnimeList, in both directions.

## Running it

Three things are needed before the first run: the external tools, the torrent
sites to search, and an AniList application so it can talk to your list.

```powershell
# Fetch rqbit, ffmpeg, mpv and the Anime4K shaders into bin/
./scripts/fetch-deps.ps1

# Build the frontend and the binary that embeds it
./scripts/build.ps1

./kuro.exe
```

Then open <http://localhost:4321>.

On **macOS and Linux** build with `scripts/build.sh` and run `./kuro`. The
first-run screen downloads what it can (rqbit, and ffmpeg on Linux) into `bin/`;
what has no clean prebuilt binary — ffmpeg on macOS, and mpv everywhere but
Windows — is a package-manager install kuro then finds on `PATH`
(`brew install ffmpeg mpv`, `apt install ffmpeg mpv`). mpv is only the optional
desktop player; the browser player needs just ffmpeg and rqbit.

`config.toml` is written beside the binary on first run. kuro ships with no
torrent sites; add one block per site and restart. `type` is the feed format
the site serves, `nyaa` or `tokyotosho`; `adult = true` marks a site searched
only for titles the catalogue marks adult. The first site listed decides the
record kept for a torrent several carry.

```toml
[[indexer]]
type = "nyaa"
url  = "https://…"

[[indexer]]
type = "tokyotosho"
url  = "https://…"
```

To connect AniList,
register an application at <https://anilist.co/settings/developer> with the
redirect URL `http://localhost:4321/callback` and paste the client id and
secret in. MyAnimeList is optional and works the same way, at
<https://myanimelist.net/apiconfig> with `http://localhost:4321/mal/callback`.

`/api/setup` reports which components are installed and what each is for.

### Updating

A packaged build checks GitHub releases on launch and every few hours, and
offers a newer version under **Settings → About**. Only `kuro.exe` is
replaced; `bin/`, the cache, `config.toml` and the database stay. Builds from
`build.ps1` without `-Version` are `dev` and never update themselves.

To publish a release: `./scripts/package.ps1 -Version 2026.08.27 -Publish`
(needs `gh auth login` once). It attaches both zips and `SHA256SUMS.txt`,
which the updater verifies before touching anything.

### Watching on a phone or TV

Set `addr = "0.0.0.0:4321"` in `config.toml` and restart. Anything off this
machine then needs a token: **Settings → Access** shows a QR code to scan.
Loopback stays open so nothing has to be configured to watch on the machine
itself.

## Legal

kuro is a media player and BitTorrent client. It hosts no content and ships
with none. What you search for, download and share is your responsibility;
make sure it is legal where you live. BitTorrent uploads while it downloads.

## Layout

```
cmd/kuro          entrypoint and wiring
internal/
  anilist         AniList GraphQL: search, browse, schedule, list sync
  mal             MyAnimeList API v2, OAuth with PKCE
  corpus          the title database, from manami and animeApi
  match           filename to anime matching
  parse           release name parsing
  score           release ranking
  indexer         torrent search
  torrent         rqbit supervisor and HTTP streaming
  transcode       ffmpeg, HLS, subtitle and font extraction
  player          mpv over JSON IPC, Anime4K shader chains
  library         the domain: playback, sync, scanning, notifications
  store           every SQL statement
  db              connection setup and migrations
  server          HTTP handlers
  jobs            recurring background work
web               the single-page app, embedded into the binary
scripts           dependency fetch and build
```

Handlers contain no SQL and no external calls; `store` owns every statement;
`library` owns the decisions. The frontend is compiled into `web/dist` and
embedded by `web/embed.go`, which is why the build script does the frontend
first.

## Development

```powershell
go test ./...                    # the whole suite
./scripts/build.ps1 -SkipWeb     # rebuild the binary only
./scripts/build.ps1 -Target linux/amd64
```

```powershell
cd web
npm run dev                      # hot reload, proxying /api to :4321
npm run typecheck                # tsgo
npm run lint
npm run verify                   # loads every screen in a real browser
```

The dev server proxies `/api` to a running `kuro.exe`, so start the binary
first and edit the frontend against it. `npm run verify` needs the binary
running too; it reports console errors, failed requests and blank screens, and
writes a screenshot of each page.

Type checking uses `tsgo`, the native TypeScript compiler, rather than `tsc`.
