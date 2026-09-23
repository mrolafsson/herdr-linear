#!/bin/sh
# Regenerates docs/images/*.png from the demo workspace, so no real issue or
# project can end up in the README.
#
# A test drives the demo picker through each screen and saves exactly what it
# draws; freeze turns those into PNGs. No browser, server or port involved.
#
# Needs freeze: brew install charmbracelet/tap/freeze
set -eu
cd "$(dirname "$0")/.."

command -v freeze >/dev/null 2>&1 || { echo "needs freeze: brew install charmbracelet/tap/freeze" >&2; exit 1; }

screens=$(mktemp -d)
trap 'rm -rf "$screens"' EXIT
SCREENS_DIR="$screens" go test -count=1 -run '^TestWriteDemoScreens$' . >/dev/null

mkdir -p docs/images
for f in "$screens"/*.ansi; do
	name=$(basename "$f" .ansi)
	freeze --execute "cat $f" --window \
		--font.family "JetBrains Mono" --font.size 14 --line-height 1.3 \
		--padding 20,24 --margin 0 --border.radius 10 \
		--background "#1e1e2e" --output "docs/images/$name.png" >/dev/null
	# freeze renders at 2x; 1800px wide is still sharp on a Retina README.
	sips --resampleWidth 1800 "docs/images/$name.png" >/dev/null
	echo "docs/images/$name.png"
done
