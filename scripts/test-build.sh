#!/bin/sh
# Tests scripts/build.sh's no-Go path (downloading a release) without a
# release or the network: a stand-in curl serves a local fake release. Checks
# a good archive installs just the binary, and a tampered one is refused.
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
t=$(mktemp -d "${TMPDIR:-/tmp}/herdr-linear-test.XXXXXX")
trap 'rm -rf "$t"' EXIT
mkdir -p "$t/repo/scripts" "$t/stub" "$t/release"
cp "$here/scripts/build.sh" "$t/repo/scripts/"
cp "$here/herdr-plugin.toml" "$t/repo/"

version=$(sed -n 's/^version *= *"\(.*\)"/\1/p' "$t/repo/herdr-plugin.toml" | head -n 1)
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "skip: no release for $(uname -s)"; exit 0 ;; esac
case "$(uname -m)" in arm64 | aarch64) arch=arm64 ;; x86_64 | amd64) arch=amd64 ;; *) echo "skip: no release for $(uname -m)"; exit 0 ;; esac
archive="herdr-linear_${version}_${os}_$arch.tar.gz"
printf '#!/bin/sh\necho stand-in binary\n' > "$t/release/herdr-linear"
chmod +x "$t/release/herdr-linear"
echo "not the binary" > "$t/release/extra-file"
sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
(cd "$t/release" && tar -czf "$archive" herdr-linear extra-file && sum "$archive" > checksums.txt)

# The stand-in curl answers a URL with the release file of the same name.
cat > "$t/stub/curl" <<EOF
#!/bin/sh
url=""; out=""
while [ \$# -gt 0 ]; do case "\$1" in -o) out="\$2"; shift ;; https://*) url="\$1" ;; esac; shift; done
cp "$t/release/\$(basename "\$url")" "\$out"
EOF
chmod +x "$t/stub/curl"
# The stand-in curl first on PATH; the download path forced, as Go may be in
# /usr/bin.
nogo="$t/stub:/usr/bin:/bin"
export HERDR_LINEAR_PREBUILT=1

if ! PATH="$nogo" sh "$t/repo/scripts/build.sh" 2>"$t/log"; then
	echo "FAIL: build.sh refused a good release:"; cat "$t/log"; exit 1
fi
[ "$("$t/repo/bin/herdr-linear")" = "stand-in binary" ] || { echo "FAIL: good release not installed"; exit 1; }
[ "$(ls "$t/repo/bin")" = "herdr-linear" ] || { echo "FAIL: extracted more than the binary"; exit 1; }
echo "ok: a release matching checksums.txt installs, binary only"

rm -rf "$t/repo/bin"
echo tampered >> "$t/release/$archive"
if PATH="$nogo" sh "$t/repo/scripts/build.sh" 2>/dev/null; then
	echo "FAIL: a tampered release was installed"; exit 1
fi
[ ! -e "$t/repo/bin/herdr-linear" ] || { echo "FAIL: tampered binary left behind"; exit 1; }
echo "ok: a release not matching checksums.txt is refused"
