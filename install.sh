#!/bin/sh
# Installs herdr-linear for the herdr TUI, the Herdr app, or both.
#
#   sh install.sh          whichever of the two is on this computer (both: both)
#   sh install.sh --tui    the herdr plugin (herdr plugin install)
#   sh install.sh --app    the Herdr app's Linear mod
#
# From anywhere, in one line:
#   curl -fsSL https://raw.githubusercontent.com/mrolafsson/herdr-linear/main/install.sh | sh -s -- --app
#
# The app's mod needs nothing else installed: it offers to sign you in to
# Linear, and fetches the herdr-linear it's part of onto each machine it works
# on the first time, checked against the release's checksums.
set -eu
REPO=mrolafsson/herdr-linear

tui() {
	command -v herdr >/dev/null 2>&1 || { echo "herdr isn't installed: https://herdr.dev" >&2; return 1; }
	herdr plugin install "$REPO"
}

# The app's command: on the PATH, or inside the app bundle (installed from the
# .dmg, it isn't put on the PATH). Prints it, or nothing.
app_cli() {
	command -v herdr-app 2>/dev/null && return
	for c in /Applications/*.app/Contents/MacOS/herdr-app "$HOME"/Applications/*.app/Contents/MacOS/herdr-app \
		/Applications/*.app/Contents/Resources/herdr-app "$HOME"/Applications/*.app/Contents/Resources/herdr-app; do
		[ -x "$c" ] && { printf '%s\n' "$c"; return; }
	done
	return 0 # none: not a failure, under set -e
}

# Whether the app is on this computer: its command, or its home folder (made
# the first time it runs).
has_app() {
	[ -n "$(app_cli)" ] || [ -d "${HERDR_APP_HOME:-$HOME/.config/herdr-app}" ]
}

app() {
	has_app || { echo "the Herdr app isn't installed: https://herdr.dev" >&2; return 1; }
	home=${HERDR_APP_HOME:-$HOME/.config/herdr-app}
	cli=$(app_cli)
	here=$(cd "$(dirname "$0")" 2>/dev/null && pwd || true)
	src="$here/app"
	if [ ! -d "$src/mods/linear" ]; then
		# Run through curl: the latest release's copy.
		tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
		[ -n "$tag" ] || { echo "couldn't find herdr-linear's latest release" >&2; return 1; }
		tmp=$(mktemp -d)
		trap 'rm -rf "$tmp"' EXIT
		curl -fsSL "https://github.com/$REPO/archive/refs/tags/$tag.tar.gz" | tar -xz -C "$tmp"
		src=$(echo "$tmp"/*/app)
		[ -d "$src/mods/linear" ] || { echo "herdr-linear $tag has no app mod: it's in 0.14.0 and later" >&2; return 1; }
	fi
	mkdir -p "$home/mods/linear"
	cp -R "$src/mods/linear/." "$home/mods/linear/"
	keys "$src/keys.json" "$home/keys.json"
	if [ -n "$cli" ]; then
		"$cli" check "$home/mods/linear"
	fi
	echo "Linear is installed: ⌘L opens it."
}

# Adds the mod's keys to the person's keys.json: its sections and keys, never
# replacing a key already bound to something else (those are listed instead).
keys() {
	if [ ! -f "$2" ]; then
		cp "$1" "$2"
		return
	fi
	if ! command -v python3 >/dev/null 2>&1; then
		echo "Add these to $2 by hand:" >&2
		cat "$1" >&2
		return
	fi
	python3 - "$1" "$2" <<'PY'
import json, sys
new, path = json.load(open(sys.argv[1])), sys.argv[2]
have = json.load(open(path))
kept = []
for section, binds in new.items():
    mine = have.setdefault(section, {})
    for key, cmd in binds.items():
        if key in mine and mine[key] != cmd:
            kept.append(f"{section} {key}: yours ({mine[key]}) kept, Linear's would be {cmd}")
        else:
            mine[key] = cmd
json.dump(have, open(path + ".tmp", "w"), indent=2, ensure_ascii=False)
import os; os.replace(path + ".tmp", path)
for k in kept: print("keys.json:", k, file=sys.stderr)
PY
}

case "${1:-}" in
--tui) tui ;;
--app) app ;;
"")
	did=
	command -v herdr >/dev/null 2>&1 && { tui; did=1; }
	has_app && { app; did=1; }
	[ -n "$did" ] || { echo "Neither herdr nor the Herdr app is installed: https://herdr.dev" >&2; exit 1; }
	;;
*) echo "usage: sh install.sh [--tui | --app]" >&2; exit 2 ;;
esac
