#!/usr/bin/env bash
# Builds web/dist, then the binary embedding it: build.ps1 for macOS/Linux.
# Usage: [SKIP_WEB=1] [GOOS=linux GOARCH=arm64] [VERSION=2026.08.27] scripts/build.sh (unstamped: "dev")
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ "${SKIP_WEB:-}" != "1" ]; then
  echo "==> frontend"
  ( cd "$root/web"
    [ -d node_modules ] || npm install
    # -b, not --noEmit, which checks nothing on the root solution tsconfig.
    npx tsgo -b --force
    npx vite build )
fi

echo "==> binary"
# A cross-compile gets its own name, as build.ps1 gives it, so it never lands on this machine's ./kuro.
goos="${GOOS:-$(go env GOHOSTOS)}"
goarch="${GOARCH:-$(go env GOHOSTARCH)}"
name="kuro"
if [ "$goos" != "$(go env GOHOSTOS)" ] || [ "$goarch" != "$(go env GOHOSTARCH)" ]; then
  name="kuro-$goos-$goarch"
fi
[ "$goos" = "windows" ] && name="$name.exe"
out="${OUT:-$root/$name}"
# go -C resolves a relative path against the repo; report the path it really writes.
case $out in /*) ;; *) out="$PWD/$out" ;; esac

# Trimming symbols roughly halves the binary; CGO off keeps it one static file.
ldflags="-s -w"
[ -n "${VERSION:-}" ] && ldflags="$ldflags -X kuro/internal/update.Version=$VERSION"
CGO_ENABLED=0 go -C "$root" build -trimpath -ldflags "$ldflags" -o "$out" ./cmd/kuro
echo "    $out"
