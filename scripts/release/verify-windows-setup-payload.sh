#!/bin/sh
set -eu

dist=${1:-dist}
sevenzip=${SEVENZIP:-}

fail() {
	echo "windows setup payload verification: $*" >&2
	exit 1
}

if [ ! -d "$dist" ] || [ -L "$dist" ]; then
	fail "distribution directory is unavailable or unsafe: $dist"
fi
if [ -z "$sevenzip" ]; then
	for candidate in 7z 7zz 7za; do
		if command -v "$candidate" >/dev/null 2>&1; then
			sevenzip=$candidate
			break
		fi
	done
fi
if [ -z "$sevenzip" ]; then
	fail "7-Zip executable is required (tried 7z, 7zz, 7za)"
fi
if ! command -v "$sevenzip" >/dev/null 2>&1; then
	fail "7-Zip executable is required: $sevenzip"
fi

dist=$(CDPATH='' cd -- "$dist" && pwd)
tmp=$(mktemp -d "$dist/.windows-setup-payload.XXXXXX")
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

for arch in amd64 arm64; do
	setup="$dist/codemcp_windows_${arch}_setup.exe"
	if [ ! -f "$setup" ] || [ -L "$setup" ]; then
		fail "canonical $arch setup is missing or unsafe"
	fi

	set -- "$dist/codemcp_windows_${arch}_"*/cm.exe
	if [ "$#" -ne 1 ] || [ ! -f "$1" ] || [ -L "$1" ]; then
		fail "expected exactly one regular GoReleaser source cm.exe for windows/$arch"
	fi
	source_binary=$1

	extract="$tmp/$arch"
	mkdir -p "$extract"
	"$sevenzip" x -y "-o$extract" "$setup" >/dev/null

	payloads=$(find "$extract" -type f -name cm.exe -print)
	count=$(printf '%s\n' "$payloads" | sed '/^$/d' | wc -l | tr -d '[:space:]')
	if [ "$count" != "1" ]; then
		fail "setup for windows/$arch contains $count extracted cm.exe payloads, want 1"
	fi
	payload=$(printf '%s\n' "$payloads" | sed -n '1p')
	if [ -L "$payload" ] || [ ! -s "$payload" ]; then
		fail "setup for windows/$arch contains an unsafe or empty cm.exe payload"
	fi
	if ! cmp -s "$source_binary" "$payload"; then
		fail "setup for windows/$arch does not embed the exact GoReleaser cm.exe"
	fi
done

echo "Windows setup payloads match GoReleaser source binaries"
