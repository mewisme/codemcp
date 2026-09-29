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
contract="$(cd "$root" && go run ./scripts/release-layout-contract --version "$version" --os "$os" --arch "$arch")"
contract_value() {
	printf '%s\n' "$contract" | awk -F= -v key="$1" '$1 == key { sub(/^[^=]*=/, ""); print; exit }'
}
asset="$(contract_value asset)"
binary_name="$(contract_value binary)"
checksum_name="$(contract_value checksum)"
signature_name="$(contract_value signature)"
[ "$binary_name" = "cm" ] || {
	echo "canonical release contract returned unexpected Unix binary '$binary_name'." >&2
	exit 1
}
[ -n "$asset" ] && [ -n "$checksum_name" ] && [ -n "$signature_name" ] || {
	echo 'canonical release contract is incomplete.' >&2
	exit 1
}
archive="$tmp/$asset"
(
	cd "$root"
	go run ./scripts/release-archive-fixture --format tar --case valid --output "$archive"
)

archive_hash() {
	path="$1"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$path" | awk '{print $1}'
	else
		shasum -a 256 "$path" | awk '{print $1}'
	fi
}
if command -v sha256sum >/dev/null 2>&1; then
	hash="$(archive_hash "$archive")"
elif command -v shasum >/dev/null 2>&1; then
	hash="$(archive_hash "$archive")"
else
	echo 'skip: no SHA-256 utility available for installer policy test'
	exit 0
fi
checksums="$tmp/$checksum_name"
printf '%s  %s\n' "$hash" "$asset" >"$checksums"
signature="$tmp/$signature_name"
printf '{}\n' >"$signature"

make_fake_path() {
	fakebin="$1"
	mkdir -p "$fakebin"
	for command_name in uname mktemp rm awk tar sed head mkdir chmod cp gzip wc tr dd; do
		command_path="$(command -v "$command_name")"
		ln -sf "$command_path" "$fakebin/$command_name"
	done
	if command -v sha256sum >/dev/null 2>&1; then
		ln -sf "$(command -v sha256sum)" "$fakebin/sha256sum"
	else
		ln -sf "$(command -v shasum)" "$fakebin/shasum"
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
	*/"$TEST_FIXTURE_ASSET") cp "$TEST_FIXTURE_ARCHIVE" "$out" ;;
	*/"$TEST_CHECKSUM_NAME") cp "$TEST_FIXTURE_CHECKSUMS" "$out" ;;
	*/"$TEST_SIGNATURE_NAME") [ -n "${TEST_SIGNATURE_REQUEST_MARKER:-}" ] && : >"$TEST_SIGNATURE_REQUEST_MARKER"; cp "$TEST_FIXTURE_SIGNATURE" "$out" ;;
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
		TEST_FIXTURE_ASSET="$asset" \
		TEST_CHECKSUM_NAME="$checksum_name" \
		TEST_SIGNATURE_NAME="$signature_name" \
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

bad_checksums="$tmp/bad-$checksum_name"
printf '%064d  %s\n' 0 "$asset" >"$bad_checksums"
checksum_marker="$tmp/signature-requested-after-bad-checksum"
checksum_log="$tmp/install-bad-checksum.log"
checksum_fakebin="$tmp/bin-bad-checksum"
make_fake_path "$checksum_fakebin"
if PATH="$checksum_fakebin" \
	HOME="$tmp/home-bad-checksum" \
	CM_VERSION="$version" \
	CM_INSTALL_DIR="$tmp/home-bad-checksum/.cm" \
	CM_BIN_DIR="$tmp/home-bad-checksum/bin" \
	TEST_FIXTURE_ARCHIVE="$archive" \
	TEST_FIXTURE_CHECKSUMS="$bad_checksums" \
	TEST_FIXTURE_SIGNATURE="$signature" \
	TEST_FIXTURE_ASSET="$asset" \
	TEST_CHECKSUM_NAME="$checksum_name" \
	TEST_SIGNATURE_NAME="$signature_name" \
	TEST_SIGNATURE_REQUEST_MARKER="$checksum_marker" \
	TEST_INSTALL_MARKER="$tmp/installed-bad-checksum" \
	/bin/sh "$installer" >"$checksum_log" 2>&1; then
	echo 'Unix installer accepted an invalid SHA-256 checksum.' >&2
	exit 1
fi
grep -q 'checksum verification failed' "$checksum_log" || {
	echo 'Unix installer did not report checksum verification failure.' >&2
	cat "$checksum_log" >&2
	exit 1
}
[ ! -e "$checksum_marker" ] || {
	echo 'Unix installer attempted optional signature verification before mandatory checksum succeeded.' >&2
	exit 1
}

run_unsafe_archive_case() {
	fixture_case="$1"
	case_archive="$tmp/$fixture_case-$asset"
	(
		cd "$root"
		go run ./scripts/release-archive-fixture --format tar --case "$fixture_case" --output "$case_archive"
	)
	case_checksums="$tmp/$fixture_case-$checksum_name"
	printf '%s  %s\n' "$(archive_hash "$case_archive")" "$asset" >"$case_checksums"
	case_marker="$tmp/installed-unsafe-$fixture_case"
	case_log="$tmp/install-unsafe-$fixture_case.log"
	case_fakebin="$tmp/bin-unsafe-$fixture_case"
	case_install_root="$tmp/home-unsafe-$fixture_case/.cm"
	mkdir -p "$case_install_root/current"
	printf 'existing-canonical-install\n' >"$case_install_root/current/cm"
	make_fake_path "$case_fakebin"
	if PATH="$case_fakebin" \
		HOME="$tmp/home-unsafe-$fixture_case" \
		CM_VERSION="$version" \
		CM_INSTALL_DIR="$case_install_root" \
		CM_BIN_DIR="$tmp/home-unsafe-$fixture_case/bin" \
		TEST_FIXTURE_ARCHIVE="$case_archive" \
		TEST_FIXTURE_CHECKSUMS="$case_checksums" \
		TEST_FIXTURE_SIGNATURE="$signature" \
		TEST_FIXTURE_ASSET="$asset" \
		TEST_CHECKSUM_NAME="$checksum_name" \
		TEST_SIGNATURE_NAME="$signature_name" \
		TEST_INSTALL_MARKER="$case_marker" \
		/bin/sh "$installer" >"$case_log" 2>&1; then
		echo "Unix installer accepted unsafe archive fixture '$fixture_case'." >&2
		cat "$case_log" >&2
		exit 1
	fi
	[ ! -e "$case_marker" ] || {
		echo "Unix installer invoked the downloaded binary for unsafe fixture '$fixture_case'." >&2
		exit 1
	}
	grep -qx 'existing-canonical-install' "$case_install_root/current/cm" || {
		echo "Unix bootstrap failure modified the existing canonical install for fixture '$fixture_case'." >&2
		exit 1
	}
}

for fixture_case in traversal absolute drive duplicate missing symlink nonregular empty aliased; do
	run_unsafe_archive_case "$fixture_case"
done

if grep -Fq 'rm -rf "$INSTALL_DIR"' "$installer"; then
	echo 'Unix installer uninstall still recursively removes the shared CodeMCP root.' >&2
	exit 1
fi
grep -Fq '"$candidate" _service uninstall' "$installer" || {
	echo 'Unix installer uninstall does not delegate ownership checks to cm uninstall.' >&2
	exit 1
}
grep -Fq 'refusing to remove shared state under $INSTALL_DIR automatically' "$installer" || {
	echo 'Unix installer uninstall is missing the fail-closed shared-state guard.' >&2
	exit 1
}

echo 'Unix installer verification-policy tests passed.'
