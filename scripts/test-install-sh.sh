#!/bin/sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
installer="$root/install.sh"

sh -n "$installer"

if grep -q 'INSTALL_ALLOW_CHECKSUM_ONLY' "$installer"; then
	echo 'Unix installer still exposes the obsolete checksum-only opt-in.' >&2
	exit 1
fi
if grep -q 'Sigstore/cosign verification is required' "$installer"; then
	echo 'Unix installer still blocks when Sigstore/cosign is unavailable.' >&2
	exit 1
fi
if ! grep -q 'SHA-256 checksum verified, continuing without signature verification' "$installer"; then
	echo 'Unix installer is missing the non-blocking checksum fallback warning.' >&2
	exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) echo 'skip: unsupported test host OS'; exit 0 ;;
esac
case "$(uname -m)" in
	arm64|aarch64) arch=arm64 ;;
	x86_64|amd64) arch=amd64 ;;
	*) echo 'skip: unsupported test host architecture'; exit 0 ;;
esac

version=v9.9.9
asset="codemcp_9.9.9_${os}_${arch}.tar.gz"
fixture="$tmp/fixture"
mkdir -p "$fixture"
cat >"$fixture/cm" <<'EOF'
#!/bin/sh
: >"$TEST_INSTALL_MARKER"
EOF
chmod +x "$fixture/cm"
archive="$tmp/$asset"
tar -czf "$archive" -C "$fixture" cm
if command -v sha256sum >/dev/null 2>&1; then
	hash="$(sha256sum "$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
	hash="$(shasum -a 256 "$archive" | awk '{print $1}')"
else
	echo 'skip: no SHA-256 utility available for installer policy test'
	exit 0
fi
checksums="$tmp/codemcp_checksums.txt"
printf '%s  %s\n' "$hash" "$asset" >"$checksums"
signature="$tmp/codemcp_checksums.txt.sigstore.json"
printf '{}\n' >"$signature"

make_fake_path() {
	fakebin="$1"
	mkdir -p "$fakebin"
	for command_name in uname mktemp rm awk tar sed head mkdir chmod cp gzip; do
		command_path="$(command -v "$command_name")"
		ln -s "$command_path" "$fakebin/$command_name"
	done
	if command -v sha256sum >/dev/null 2>&1; then
		ln -s "$(command -v sha256sum)" "$fakebin/sha256sum"
	else
		ln -s "$(command -v shasum)" "$fakebin/shasum"
	fi
	cat >"$fakebin/curl" <<'EOF'
#!/bin/sh
set -eu
out=''
url=''
while [ "$#" -gt 0 ]; do
	case "$1" in
		-o) shift; out="$1" ;;
		http://*|https://*) url="$1" ;;
	esac
	shift
done
[ -n "$out" ] && [ -n "$url" ] || exit 2
case "$url" in
	*.tar.gz) cp "$TEST_FIXTURE_ARCHIVE" "$out" ;;
	*codemcp_checksums.txt) cp "$TEST_FIXTURE_CHECKSUMS" "$out" ;;
	*.sigstore.json) cp "$TEST_FIXTURE_SIGNATURE" "$out" ;;
	*) exit 22 ;;
esac
EOF
	chmod +x "$fakebin/curl"
}

run_fallback_case() {
	mode="$1"
	fakebin="$tmp/bin-$mode"
	make_fake_path "$fakebin"
	if [ "$mode" = failed ]; then
		cat >"$fakebin/cosign" <<'EOF'
#!/bin/sh
exit 1
EOF
		chmod +x "$fakebin/cosign"
	fi
	marker="$tmp/installed-$mode"
	log="$tmp/install-$mode.log"
	if ! PATH="$fakebin" \
		HOME="$tmp/home-$mode" \
		CM_VERSION="$version" \
		CM_INSTALL_DIR="$tmp/home-$mode/.cm" \
		CM_BIN_DIR="$tmp/home-$mode/bin" \
		TEST_FIXTURE_ARCHIVE="$archive" \
		TEST_FIXTURE_CHECKSUMS="$checksums" \
		TEST_FIXTURE_SIGNATURE="$signature" \
		TEST_INSTALL_MARKER="$marker" \
		/bin/sh "$installer" >"$log" 2>&1; then
		echo "Unix installer failed during $mode cosign fallback case." >&2
		cat "$log" >&2
		exit 1
	fi
	[ -f "$marker" ] || {
		echo "Unix installer did not continue after $mode cosign verification." >&2
		cat "$log" >&2
		exit 1
	}
	grep -q 'SHA-256 checksum verified, continuing without signature verification' "$log" || {
		echo "Unix installer did not report checksum fallback for $mode cosign verification." >&2
		cat "$log" >&2
		exit 1
	}
}

run_fallback_case missing
run_fallback_case failed

echo 'Unix installer verification-policy tests passed.'
