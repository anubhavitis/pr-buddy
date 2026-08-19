#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$root"

dist="$root/dist"
mkdir -p "$dist"

# Host only: the extension zip is the same bytes on every platform, so
# package-mac.sh builds it once.
for arch in amd64 arm64; do
  host="$dist/pr-buddy-host-linux-$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" \
    -o "$host" ./cmd/pr-buddy-host
  # A silently-wrong GOARCH would ship a binary nobody can run; catch it here.
  case "$arch" in
    amd64) want="x86-64" ;;
    arm64) want="ARM aarch64" ;;
  esac
  if ! file "$host" | grep -q "$want"; then
    echo "refusing: $host is not $want: $(file "$host")" >&2
    exit 1
  fi
done

cat <<EOF
Packed:
  $dist/pr-buddy-host-linux-amd64
  $dist/pr-buddy-host-linux-arm64

User:
  1. chmod +x pr-buddy-host-linux-amd64 && ./pr-buddy-host-linux-amd64
  2. Unzip pr-buddy-extension.zip
  3. Chrome → chrome://extensions → Developer mode → Load unpacked
     → select pr-buddy-extension/
EOF
