#!/bin/sh
# pgbot installer. Detects OS/arch, downloads the release tarball, verifies the
# release — cosign signature on checksums.txt (when cosign is present), then the
# tarball's SHA256 against it — before making anything executable, then installs.
#
#   curl -fsSL https://pgbot.dev/install | sh
#
# Env:
#   PGBOT_VERSION            version to install (default: latest)
#   PGBOT_INSTALL_DIR        install directory (default: /usr/local/bin; no sudo if writable)
#   PGBOT_REQUIRE_SIGNATURE  set to 1 to REQUIRE cosign signature verification
#                            (missing cosign, missing artifacts, or a failed check → hard error)
set -eu

REPO="pgrundev/pgbot"
INSTALL_DIR="${PGBOT_INSTALL_DIR:-/usr/local/bin}"

say()  { printf 'pgbot-install: %s\n' "$1" >&2; }
die()  { say "error: $1"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux|darwin) ;;
  *) die "unsupported OS: $os (build from source: go install github.com/$REPO/cmd/pgbot@latest)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) die "unsupported architecture: $arch" ;;
esac

have curl || have wget || die "need curl or wget"
have sha256sum || have shasum || die "need sha256sum or shasum"

fetch() { # url dest
  if have curl; then curl -fsSL "$1" -o "$2"; else wget -qO "$2" "$1"; fi
}

version="${PGBOT_VERSION:-}"
if [ -z "$version" ]; then
  api="https://api.github.com/repos/$REPO/releases/latest"
  version="$(fetch "$api" /dev/stdout | grep -m1 '"tag_name"' | cut -d'"' -f4)"
  [ -n "$version" ] || die "could not determine latest version; set PGBOT_VERSION"
fi
ver="${version#v}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

base="https://github.com/$REPO/releases/download/$version"
tarball="pgbot_${ver}_${os}_${arch}.tar.gz"

say "downloading $tarball ($version)"
fetch "$base/$tarball" "$tmp/$tarball" || die "download failed"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "checksums download failed"

# --- signature verification --------------------------------------------------
# The SHA256 check below only proves the tarball matches checksums.txt — but an
# attacker who can replace the tarball can replace checksums.txt alongside it.
# cosign verifies that checksums.txt was signed by pgbot's release workflow
# (keyless, GitHub Actions OIDC), which closes that gap. Do this BEFORE trusting
# checksums.txt for the hash comparison.
require_sig="${PGBOT_REQUIRE_SIGNATURE:-0}"
if have cosign; then
  if fetch "$base/checksums.txt.sig" "$tmp/checksums.txt.sig" 2>/dev/null &&
     fetch "$base/checksums.txt.pem" "$tmp/checksums.txt.pem" 2>/dev/null; then
    say "verifying signature (cosign)"
    if cosign verify-blob \
         --certificate "$tmp/checksums.txt.pem" \
         --signature "$tmp/checksums.txt.sig" \
         --certificate-identity-regexp "^https://github.com/${REPO}/" \
         --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
         "$tmp/checksums.txt" >/dev/null 2>&1; then
      say "signature OK"
    else
      die "signature verification FAILED — refusing to install"
    fi
  else
    [ "$require_sig" = "1" ] && die "signature artifacts missing for $version (PGBOT_REQUIRE_SIGNATURE=1)"
    say "warning: signature artifacts not found for $version — proceeding with checksum only"
  fi
elif [ "$require_sig" = "1" ]; then
  die "cosign not found but PGBOT_REQUIRE_SIGNATURE=1 — install it: https://docs.sigstore.dev/cosign/installation"
else
  say "note: cosign not found — skipping signature verification (checksum still enforced)."
  say "      install cosign, or set PGBOT_REQUIRE_SIGNATURE=1, to require it."
fi

say "verifying checksum"
expected="$(grep " $tarball\$" "$tmp/checksums.txt" | awk '{print $1}')"
[ -n "$expected" ] || die "no checksum listed for $tarball"
if have sha256sum; then
  actual="$(sha256sum "$tmp/$tarball" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$tarball" | awk '{print $1}')"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch — refusing to install (expected $expected, got $actual)"

tar -xzf "$tmp/$tarball" -C "$tmp"
[ -f "$tmp/pgbot" ] || die "binary not found in archive"
chmod +x "$tmp/pgbot"

if [ -w "$INSTALL_DIR" ]; then
  mv "$tmp/pgbot" "$INSTALL_DIR/pgbot"
elif have sudo; then
  say "installing to $INSTALL_DIR (needs sudo)"
  sudo mv "$tmp/pgbot" "$INSTALL_DIR/pgbot"
else
  die "$INSTALL_DIR is not writable and sudo is unavailable; set PGBOT_INSTALL_DIR to a writable path"
fi

say "installed pgbot $version to $INSTALL_DIR/pgbot"
"$INSTALL_DIR/pgbot" --version || true
