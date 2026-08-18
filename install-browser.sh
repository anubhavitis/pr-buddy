#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$root"
go build -o pr-buddy-host ./cmd/pr-buddy-host
cd browser
npm install
npm run compile
cat <<EOF
Built:
  $root/pr-buddy-host
  $root/browser  (unpacked Chrome extension)

1. Start the host (leave it running):
     $root/pr-buddy-host

2. Chrome → chrome://extensions → Developer mode → Load unpacked
   → select $root/browser

3. Open a github.com pull request Files tab.
EOF
