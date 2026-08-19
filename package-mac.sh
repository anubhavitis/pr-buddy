#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$root"

dist="$root/dist"
ext="$dist/pr-buddy-extension"
host="$dist/pr-buddy-host-darwin-arm64"
zip="$dist/pr-buddy-extension.zip"

# Clear only what this script writes, so a package-linux.sh run survives.
rm -rf "$ext" "$host" "$zip"
mkdir -p "$ext"

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
  -o "$host" ./cmd/pr-buddy-host

cd "$root/browser"
npm install
npm run compile
cd "$root"

cp browser/manifest.json browser/popup.html "$ext/"
mkdir -p "$ext/icons"
for f in browser/icons/icon16.png browser/icons/icon32.png browser/icons/icon48.png browser/icons/icon128.png; do
  cp "$f" "$ext/icons/"
done
cp -R browser/out "$ext/out"

# Load unpacked needs a folder; zip that folder, not a flat dump.
rm -f "$zip"
(cd "$dist" && zip -r -X "pr-buddy-extension.zip" pr-buddy-extension)

if ! file "$host" | grep -q "arm64"; then
  echo "refusing: host is not arm64: $(file "$host")" >&2
  exit 1
fi
if unzip -l "$zip" | grep -E 'node_modules|/src/'; then
  echo "refusing: extension zip contains sources" >&2
  exit 1
fi

cat <<EOF
Packed:
  $host
  $zip

User:
  1. chmod +x pr-buddy-host-darwin-arm64 && ./pr-buddy-host-darwin-arm64
  2. Unzip pr-buddy-extension.zip
  3. Chrome → chrome://extensions → Developer mode → Load unpacked
     → select pr-buddy-extension/
EOF
