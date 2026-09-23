#!/bin/sh
# Regenerates docs/images/*.png and demo.gif from docs/demo.tape. Everything on
# screen is the built-in demo workspace, so no real issue or project can leak
# into the README.
#
# Needs VHS: brew install vhs
set -eu
cd "$(dirname "$0")/.."

command -v vhs >/dev/null 2>&1 || { echo "needs VHS: brew install vhs" >&2; exit 1; }
sh scripts/build.sh
mkdir -p docs/images
vhs docs/demo.tape
ls -1 docs/images
