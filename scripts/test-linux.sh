#!/bin/sh
# Runs the tests on Linux, in Docker, with a real Secret Service (GNOME
# Keyring on a private D-Bus session), so the keyring round trip runs too.
# From a Mac: sh scripts/test-linux.sh
set -eu
cd "$(dirname "$0")/.."
go_version=$(sed -n 's/^go //p' go.mod)
exec docker run --rm -e COUNT="${COUNT:-1}" -e TESTARGS="${TESTARGS:-}" -v "$PWD":/src -w /src "golang:$go_version" sh -c '
	set -eu
	apt-get update -qq >/dev/null
	DEBIAN_FRONTEND=noninteractive apt-get install -y -qq gnome-keyring dbus >/dev/null
	export HERDR_LINEAR_KEYRING_TEST=1
	dbus-run-session -- sh -c "
		printf test | gnome-keyring-daemon --unlock --components=secrets >/dev/null
		go vet ./... && go test -race -count=${COUNT:-1} ${TESTARGS:-} ./... && sh scripts/test-build.sh
	"
'
