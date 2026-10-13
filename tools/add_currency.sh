#!/usr/bin/env bash
# Runs add_currency.go. If Go isn't installed, downloads a private copy into
# the user cache once (no admin rights or system install needed).
set -euo pipefail
cd "$(dirname "$0")"

GO_VERSION=1.24.6

if ! command -v go >/dev/null 2>&1; then
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/sygna-bridge-assembly/go$GO_VERSION"
  if [ ! -x "$cache/go/bin/go" ]; then
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    case "$arch" in
      x86_64) arch=amd64 ;;
      aarch64) arch=arm64 ;;
    esac
    echo "Go not found; downloading Go $GO_VERSION to $cache (one-time)..." >&2
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    curl -fsSL "https://go.dev/dl/go$GO_VERSION.$os-$arch.tar.gz" | tar -xz -C "$tmp"
    mkdir -p "$cache"
    rm -rf "$cache/go"
    mv "$tmp/go" "$cache/go"
  fi
  export PATH="$cache/go/bin:$PATH"
  export GOTOOLCHAIN=local
fi

exec go run add_currency.go "$@"
