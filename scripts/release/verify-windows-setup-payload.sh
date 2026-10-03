#!/bin/sh
set -eu

staging=${1:-.release-staging/windows-setup}
dist=${2:-dist}

fail() {
	echo "windows setup payload verification: $*" >&2
	exit 1
}

if [ ! -d "$staging" ] || [ -L "$staging" ]; then
	fail "staging directory is unavailable or unsafe: $staging"
fi
if [ ! -d "$dist" ] || [ -L "$dist" ]; then
	fail "distribution directory is unavailable or unsafe: $dist"
fi

staging=$(CDPATH='' cd -- "$staging" && pwd)
dist=$(CDPATH='' cd -- "$dist" && pwd)
tmp=$(mktemp -d "$dist/.windows-setup-provenance.XXXXXX")
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

arch=amd64
setup="$staging/codemcp_windows_${arch}_setup.exe"
if [ ! -f "$setup" ] || [ -L "$setup" ]; then
	fail "canonical $arch setup is missing or unsafe"
fi

digest="$staging/codemcp_windows_${arch}_cm.sha256"
if [ ! -f "$digest" ] || [ -L "$digest" ]; then
	fail "canonical $arch payload digest is missing or unsafe"
fi

expected_hash=
digest_name=
digest_extra=
IFS=' ' read -r expected_hash digest_name digest_extra <"$digest" || fail "payload digest is unreadable"
if [ -n "$digest_extra" ] || [ "$digest_name" != "cm.exe" ]; then
	fail "payload digest must contain exactly one sha256 entry for cm.exe"
fi
if [ "${#expected_hash}" -ne 64 ]; then
	fail "payload digest has an invalid sha256 length"
fi
case "$expected_hash" in
	*[!0-9a-f]*)
		fail "payload digest is not lowercase sha256"
		;;
esac

archive="$dist/codemcp_windows_${arch}.zip"
if [ ! -f "$archive" ] || [ -L "$archive" ]; then
	fail "canonical windows/$arch archive is missing or unsafe"
fi
if ! command -v unzip >/dev/null 2>&1; then
	fail "unzip is required to verify the canonical Windows archive"
fi
unzip -q "$archive" -d "$tmp/archive"

payload_count=$(find "$tmp/archive" -type f -name cm.exe -print | wc -l | tr -d '[:space:]')
if [ "$payload_count" != "1" ]; then
	fail "canonical windows/$arch archive must contain exactly one regular cm.exe"
fi
payload=$(find "$tmp/archive" -type f -name cm.exe -print | sed -n '1p')
if [ ! -f "$payload" ] || [ -L "$payload" ]; then
	fail "canonical windows/$arch archive contains an unsafe cm.exe"
fi
actual_hash=$(sha256sum "$payload" | awk '{print $1}')
if [ "$actual_hash" != "$expected_hash" ]; then
	fail "staged setup payload digest does not match the canonical windows/$arch archive binary"
fi

checksums="$dist/codemcp_checksums.txt"
if [ ! -f "$checksums" ] || [ -L "$checksums" ]; then
	fail "canonical checksum manifest is missing or unsafe"
fi
setup_name=$(basename "$setup")
checksum_count=$(awk -v name="$setup_name" '$2 == name { count++ } END { print count + 0 }' "$checksums")
if [ "$checksum_count" != "1" ]; then
	fail "checksum manifest contains $checksum_count entries for $setup_name, want 1"
fi

echo "Windows setup provenance matches the canonical GoReleaser archive"
