#!/usr/bin/env bash
# Builds the frontend and then the binary that embeds it, the macOS/Linux
# counterpart to build.ps1. The single-page app is compiled into web/dist and
# embedded by web/embed.go, so the frontend must come first.
#
#   scripts/build.sh              # this platform
#   SKIP_WEB=1 scripts/build.sh   # reuse web/dist, Go only
#   GOOS=linux GOARCH=arm64 scripts/build.sh
#   VERSION=2026.08.27 scripts/build.sh   # stamped; unstamped is "dev", never self-updates
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ "${SKIP_WEB:-}" != "1" ]; then
  echo "==> frontend"
  ( cd "$root/web"
    [ -d node_modules ] || npm install
    # -b, not --noEmit: the root tsconfig is a solution file with no files of
    # its own, so --noEmit checks nothing.
    npx tsgo -b --force
    npx vite build )
fi

echo "==> binary"
name="kuro"
[ "${GOOS:-$(go env GOOS)}" = "windows" ] && name="kuro.exe"
out="${OUT:-$root/$name}"

# Trimming the symbol table roughly halves the binary; nothing in production
# needs a Go stack trace. CGO off keeps it a single static file.
ldflags="-s -w"
[ -n "${VERSION:-}" ] && ldflags="$ldflags -X kuro/internal/update.Version=$VERSION"
CGO_ENABLED=0 go -C "$root" build -trimpath -ldflags "$ldflags" -o "$out" ./cmd/kuro
echo "    $out"
