#!/usr/bin/env bash
# Runs add_currency.go. If Go isn't installed, downloads a private copy into
# the user cache once (no admin rights or system install needed).
set -euo pipefail
cd "$(dirname "$0")"

GO_VERSION=1.24.6

# Official SHA-256 checksums from https://go.dev/dl/?mode=json&include=all
go_sha256() {
  case "$1" in
    darwin-amd64) echo 4a8d7a32052f223e71faab424a69430455b27b3fff5f4e651f9d97c3e51a8746 ;;
    darwin-arm64) echo 4e29202c49573b953be7cc3500e1f8d9e66ddd12faa8cf0939a4951411e09a2a ;;
    linux-amd64) echo bbca37cc395c974ffa4893ee35819ad23ebb27426df87af92e93a9ec66ef8712 ;;
    linux-arm64) echo 124ea6033a8bf98aa9fbab53e58d134905262d45a022af3a90b73320f3c3afd5 ;;
    *) return 1 ;;
  esac
}

sha256_of() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    sha256sum "$1" | cut -d' ' -f1
  fi
}

if ! command -v go >/dev/null 2>&1; then
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/sygna-bridge-assembly/go$GO_VERSION"
  if [ ! -x "$cache/go/bin/go" ]; then
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    case "$arch" in
      x86_64 | amd64) arch=amd64 ;;
      arm64 | aarch64) arch=arm64 ;;
      *) echo "Unsupported architecture: $arch — please install Go $GO_VERSION manually" >&2; exit 1 ;;
    esac
    if ! want=$(go_sha256 "$os-$arch"); then
      echo "Unsupported platform: $os-$arch — please install Go $GO_VERSION manually" >&2
      exit 1
    fi

    echo "Go not found; downloading Go $GO_VERSION to $cache (one-time)..." >&2
    mkdir -p "$(dirname "$cache")"
    tmp=$(mktemp -d "$(dirname "$cache")/.go-download.XXXXXX")
    archive="$tmp/go.tar.gz"
    curl -fsSL -o "$archive" "https://go.dev/dl/go$GO_VERSION.$os-$arch.tar.gz"
    got=$(sha256_of "$archive")
    if [ "$got" != "$want" ]; then
      rm -rf "$tmp"
      echo "Checksum mismatch for Go $GO_VERSION download (got $got), aborting" >&2
      exit 1
    fi
    mkdir "$tmp/extract"
    tar -xzf "$archive" -C "$tmp/extract"
    # Publish atomically. If another run finished first, keep its copy and
    # never delete a toolchain that may be in use.
    if mkdir "$cache" 2>/dev/null; then
      mv "$tmp/extract/go" "$cache/go"
    else
      for _ in $(seq 1 60); do
        [ -x "$cache/go/bin/go" ] && break
        sleep 1
      done
    fi
    rm -rf "$tmp"
    if [ ! -x "$cache/go/bin/go" ]; then
      echo "Go install in $cache is incomplete; delete that directory and retry" >&2
      exit 1
    fi
  fi
  export PATH="$cache/go/bin:$PATH"
  export GOTOOLCHAIN=local
fi

exec go run add_currency.go "$@"
