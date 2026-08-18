#!/usr/bin/env bash
# End-to-end packaging proof for THIS platform (DoD 10): build the real Go binary,
# assemble the npm tree with build.mjs, `npm pack` the wrapper + this platform's
# package, install the tarballs with --ignore-scripts, and run `pgbot --version`
# through the installed wrapper. Proves the install needs no lifecycle scripts and
# that the wrapper resolves + execs the binary. No registry, no publish.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
version="0.0.0-smoke"

goos="$(cd "$root" && go env GOOS)"
goarch="$(cd "$root" && go env GOARCH)"
exe="pgbot"
[ "$goos" = "windows" ] && exe="pgbot.exe"

echo "→ building real binary ($goos/$goarch)"
mkdir -p "$work/dist"
( cd "$root" && CGO_ENABLED=0 go build -o "$work/dist/$exe" ./cmd/pgbot )
cat > "$work/dist/artifacts.json" <<JSON
[{"type":"Binary","path":"$work/dist/$exe","goos":"$goos","goarch":"$goarch"}]
JSON

echo "→ assembling npm tree"
DIST="$work/dist" OUT="$work/staging" node "$root/npm/build.mjs" "$version"

platkey="$(ls "$work/staging/@pgbot")"
echo "→ npm pack ($platkey + wrapper)"
mkdir -p "$work/tarballs"
( cd "$work/staging/@pgbot/$platkey" && npm pack --pack-destination "$work/tarballs" >/dev/null 2>&1 )
( cd "$work/staging/pgbot" && npm pack --pack-destination "$work/tarballs" >/dev/null 2>&1 )

echo "→ install with --ignore-scripts"
proj="$work/proj"; mkdir -p "$proj"
( cd "$proj" && npm init -y >/dev/null 2>&1 )
# Install both tarballs explicitly; the wrapper's other-platform optionalDeps are
# skipped (not published) but that is not fatal — that's the whole point.
( cd "$proj" && npm install --ignore-scripts --no-save --no-audit --no-fund \
    "$work/tarballs"/pgbot-*.tgz "$work/tarballs"/pgbot-"$platkey"-*.tgz >/dev/null 2>&1 )

echo "→ run pgbot --version through the installed wrapper"
out="$("$proj/node_modules/.bin/pgbot" --version)"
echo "   $out"
case "$out" in
  "pgbot version"*) echo "✓ pack-smoke OK ($platkey, --ignore-scripts)";;
  *) echo "✗ unexpected --version output: $out"; exit 1;;
esac
