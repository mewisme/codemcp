#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
builder="$repo_root/scripts/build-windows-setup.sh"
template="$repo_root/installer/windows/codemcp.nsi"
tmp=$(mktemp -d)
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

fail() {
	echo "windows setup test: $*" >&2
	exit 1
}

fakebin="$tmp/bin"
dist="$tmp/dist"
mkdir -p "$fakebin" "$dist" "$tmp/amd64" "$tmp/arm64"
printf 'amd64-cm\n' >"$tmp/amd64/cm.exe"
printf 'arm64-cm\n' >"$tmp/arm64/cm.exe"
chmod +x "$tmp/amd64/cm.exe" "$tmp/arm64/cm.exe"

cat >"$fakebin/makensis" <<'EOF'
#!/bin/sh
set -eu
output=
binary=
arch=
script=
for arg in "$@"; do
	case "$arg" in
		-DOUTPUT_PATH=*) output=${arg#-DOUTPUT_PATH=} ;;
		-DBINARY_PATH=*) binary=${arg#-DBINARY_PATH=} ;;
		-DSETUP_ARCH=*) arch=${arg#-DSETUP_ARCH=} ;;
		*.nsi) script=$arg ;;
	esac
done
[ -n "$output" ] && [ -n "$binary" ] && [ -n "$arch" ] && [ -n "$script" ] || exit 30
[ "$(basename "$binary")" = "cm.exe" ] || exit 31
[ -f "$binary" ] || exit 32
[ -f "$script" ] || exit 33
printf '%s|%s|%s\n' "$arch" "$binary" "$script" >>"$TEST_MAKENSIS_LOG"
printf 'fake-nsis:%s:%s\n' "$arch" "$(cat "$binary")" >"$output"
EOF
chmod +x "$fakebin/makensis"

PATH="$fakebin:$PATH" TEST_MAKENSIS_LOG="$tmp/makensis.log" \
	sh "$builder" "$tmp/amd64/cm.exe" windows_amd64_v1 "$dist" >/dev/null
PATH="$fakebin:$PATH" TEST_MAKENSIS_LOG="$tmp/makensis.log" \
	sh "$builder" "$tmp/arm64/cm.exe" windows_arm64_v8.0 "$dist" >/dev/null

[ -f "$dist/codemcp_windows_amd64_setup.exe" ] || fail "amd64 setup artifact missing"
[ -f "$dist/codemcp_windows_arm64_setup.exe" ] || fail "arm64 setup artifact missing"
[ "$(find "$dist" -maxdepth 1 -type f -name 'codemcp_windows_*_setup.exe' | wc -l | tr -d '[:space:]')" = "2" ] ||
	fail "wrapper did not emit exactly two stable setup artifacts"
grep -Fq "amd64|$tmp/amd64/cm.exe|$template" "$tmp/makensis.log" ||
	fail "amd64 setup did not embed the exact target cm.exe"
grep -Fq "arm64|$tmp/arm64/cm.exe|$template" "$tmp/makensis.log" ||
	fail "arm64 setup did not embed the exact target cm.exe"

before=$(wc -l <"$tmp/makensis.log" | tr -d '[:space:]')
PATH="/usr/bin:/bin" MAKENSIS=missing-makensis \
	sh "$builder" "$tmp/amd64/cm.exe" linux_amd64_v1 "$tmp/nonwindows" >/dev/null
after=$(wc -l <"$tmp/makensis.log" | tr -d '[:space:]')
[ "$before" = "$after" ] || fail "non-Windows target invoked makensis"

if PATH="$fakebin:$PATH" TEST_MAKENSIS_LOG="$tmp/makensis.log" \
	sh "$builder" "$tmp/amd64/cm.exe" windows_386 "$tmp/unsupported" >/dev/null 2>&1; then
	fail "unsupported Windows architecture was accepted"
fi

printf 'wrong\n' >"$tmp/amd64/not-cm.exe"
if PATH="$fakebin:$PATH" TEST_MAKENSIS_LOG="$tmp/makensis.log" \
	sh "$builder" "$tmp/amd64/not-cm.exe" windows_amd64_v1 "$tmp/wrong-name" >/dev/null 2>&1; then
	fail "non-canonical embedded binary name was accepted"
fi

ln -s "$tmp/amd64/cm.exe" "$tmp/amd64/cm-link.exe"
if PATH="$fakebin:$PATH" TEST_MAKENSIS_LOG="$tmp/makensis.log" \
	sh "$builder" "$tmp/amd64/cm-link.exe" windows_amd64_v1 "$tmp/symlink" >/dev/null 2>&1; then
	fail "symlink embedded binary was accepted"
fi

# Assertions below intentionally match literal NSIS variables.
grep -Fq 'RequestExecutionLevel user' "$template" || fail "setup is not user-scoped"
# shellcheck disable=SC2016
grep -Fq 'SetOutPath "$PLUGINSDIR"' "$template" || fail "setup does not use temporary plugin extraction"
# shellcheck disable=SC2016
grep -Fq 'File /oname=cm.exe "${BINARY_PATH}"' "$template" || fail "setup does not embed only canonical cm.exe"
# shellcheck disable=SC2016
grep -Fq 'ExecWait '\''"$PLUGINSDIR\cm.exe" install'\''' "$template" || fail "setup does not invoke canonical install"
grep -Fq 'HKCU "Environment" "Path"' "$template" || fail "setup PATH registration is not user-scoped"
# shellcheck disable=SC2016
grep -Fq '$PROFILE\.cm' "$template" || fail "setup PATH registration drifted from canonical Windows layout"
if grep -Eiq 'WriteUninstaller|ProgramFiles|cgm|chatgpt-mcp' "$template"; then
	fail "setup contains a second uninstall/root path or retired executable identity"
fi

echo "Windows setup wrapper contract OK"
