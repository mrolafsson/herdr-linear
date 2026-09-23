#!/bin/sh
# Produces ./bin/herdr-linear. herdr runs this as the manifest's [[build]] step
# after `herdr plugin install` clones the repo; you can also run it by hand
# from anywhere.
#
# With Go installed it builds the checked-out source. Without Go it downloads
# the prebuilt binary for this exact version from the GitHub release.
set -eu
cd "$(dirname "$0")/.."

REPO="mrolafsson/herdr-linear"
mkdir -p bin

if command -v go >/dev/null 2>&1; then
	echo "herdr-linear: building from source…" >&2
	exec go build -trimpath -ldflags "-s -w" -o bin/herdr-linear .
fi

# The release that matches this checkout: the manifest's version.
version=$(sed -n 's/^version *= *"\(.*\)"/\1/p' herdr-plugin.toml | head -n 1)
case "$(uname -m)" in
	arm64 | aarch64) arch=arm64 ;;
	x86_64 | amd64) arch=amd64 ;;
	*) echo "herdr-linear: no prebuilt binary for $(uname -m); install Go and retry" >&2; exit 1 ;;
esac
url="https://github.com/$REPO/releases/download/v$version/herdr-linear_${version}_darwin_$arch.tar.gz"

echo "herdr-linear: no Go toolchain; downloading v$version for darwin/$arch…" >&2
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if ! curl -fsSL "$url" -o "$tmp/release.tar.gz"; then
	echo "herdr-linear: download failed ($url). Install Go (https://go.dev/dl) and retry." >&2
	exit 1
fi
tar -xzf "$tmp/release.tar.gz" -C "$tmp"
mv "$tmp/herdr-linear" bin/herdr-linear
chmod +x bin/herdr-linear
