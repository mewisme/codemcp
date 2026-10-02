#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
builder="$repo_root/scripts/release/build-windows-setup.sh"
template="$repo_root/installer/windows/codemcp.nsi"
powershell_smoke="$repo_root/scripts/installer/test-windows-setup.ps1"
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

cat >"$fakebin/upx" <<'EOF'
#!/bin/sh
set -eu
[ "$1" = "--best" ] || exit 20
[ -f "$2" ] || exit 21
printf 'packed:' >>"$2"
EOF
chmod +x "$fakebin/upx"

PATH="$fakebin:$PATH" sh "$repo_root/scripts/release/pack-release-binary.sh" "$tmp/amd64/cm.exe" windows_amd64_v1
before_arm64=$(cat "$tmp/arm64/cm.exe")
PATH="$fakebin:$PATH" sh "$repo_root/scripts/release/pack-release-binary.sh" "$tmp/arm64/cm.exe" windows_arm64_v8.0
[ "$(cat "$tmp/arm64/cm.exe")" = "$before_arm64" ] || fail "Windows arm64 binary was modified by release packing"
before_darwin=$(cat "$tmp/amd64/cm.exe")
PATH="$fakebin:$PATH" sh "$repo_root/scripts/release/pack-release-binary.sh" "$tmp/amd64/cm.exe" darwin_amd64_v1
[ "$(cat "$tmp/amd64/cm.exe")" = "$before_darwin" ] || fail "Darwin binary was modified by release packing"

cat >"$fakebin/makensis" <<'EOF'
#!/bin/sh
set -eu
output=
binary=
arch=
version=
script=
for arg in "$@"; do
	case "$arg" in
		-DOUTPUT_PATH=*) output=${arg#-DOUTPUT_PATH=} ;;
		-DBINARY_PATH=*) binary=${arg#-DBINARY_PATH=} ;;
		-DSETUP_ARCH=*) arch=${arg#-DSETUP_ARCH=} ;;
		-DSETUP_VERSION=*) version=${arg#-DSETUP_VERSION=} ;;
		*.nsi) script=$arg ;;
	esac
done
[ -n "$output" ] && [ -n "$binary" ] && [ -n "$arch" ] && [ -n "$version" ] && [ -n "$script" ] || exit 30
[ "$(basename "$binary")" = "cm.exe" ] || exit 31
[ -f "$binary" ] || exit 32
[ -f "$script" ] || exit 33
printf '%s|%s|%s|%s\n' "$arch" "$version" "$binary" "$script" >>"$TEST_MAKENSIS_LOG"
printf 'fake-nsis:%s:%s\n' "$arch" "$(cat "$binary")" >"$output"
EOF
chmod +x "$fakebin/makensis"

PATH="$fakebin:$PATH" TEST_MAKENSIS_LOG="$tmp/makensis.log" \
	sh "$builder" "$tmp/amd64/cm.exe" windows_amd64_v1 "$dist" "v1.2.3" >/dev/null
PATH="$fakebin:$PATH" TEST_MAKENSIS_LOG="$tmp/makensis.log" \
	sh "$builder" "$tmp/arm64/cm.exe" windows_arm64_v8.0 "$dist" "2.3.4-next" >/dev/null

[ -f "$dist/codemcp_windows_amd64_setup.exe" ] || fail "amd64 setup artifact missing"
[ -f "$dist/codemcp_windows_arm64_setup.exe" ] || fail "arm64 setup artifact missing"
[ "$(find "$dist" -maxdepth 1 -type f -name 'codemcp_windows_*_setup.exe' | wc -l | tr -d '[:space:]')" = "2" ] ||
	fail "wrapper did not emit exactly two stable setup artifacts"
grep -Fq "amd64|1.2.3.0|$tmp/amd64/cm.exe|$template" "$tmp/makensis.log" ||
	fail "amd64 setup did not embed the exact target cm.exe and release version"
grep -Fq "arm64|2.3.4.0|$tmp/arm64/cm.exe|$template" "$tmp/makensis.log" ||
	fail "arm64 setup did not embed the exact target cm.exe and release version"

mkdir -p "$dist/codemcp_windows_amd64_v1" "$dist/codemcp_windows_arm64_v8.0"
cp "$tmp/amd64/cm.exe" "$dist/codemcp_windows_amd64_v1/cm.exe"
cp "$tmp/arm64/cm.exe" "$dist/codemcp_windows_arm64_v8.0/cm.exe"
cat >"$fakebin/7z" <<'EOF'
#!/bin/sh
set -eu
output=
setup=
for arg in "$@"; do
	case "$arg" in
		-o*) output=${arg#-o} ;;
		*.exe) setup=$arg ;;
	esac
done
[ -n "$output" ] && [ -n "$setup" ] || exit 40
case "$(basename "$setup")" in
	codemcp_windows_amd64_setup.exe) arch=amd64 ;;
	codemcp_windows_arm64_setup.exe) arch=arm64 ;;
	*) exit 41 ;;
esac
if [ "${TEST_PAYLOAD_WRONG:-0}" = "1" ]; then
	case "$arch" in
		amd64) arch=arm64 ;;
		arm64) arch=amd64 ;;
	esac
fi
set -- "$TEST_DIST/codemcp_windows_${arch}_"*/cm.exe
[ "$#" -eq 1 ] && [ -f "$1" ] || exit 42
mkdir -p "$output"
cp "$1" "$output/cm.exe"
EOF
chmod +x "$fakebin/7z"

PATH="$fakebin:$PATH" TEST_DIST="$dist" sh "$repo_root/scripts/release/verify-windows-setup-payload.sh" "$dist" >/dev/null
if PATH="$fakebin:$PATH" TEST_DIST="$dist" TEST_PAYLOAD_WRONG=1 sh "$repo_root/scripts/release/verify-windows-setup-payload.sh" "$dist" >/dev/null 2>&1; then
	fail "payload verifier accepted the wrong architecture cm.exe"
fi

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
# GUI installers must be awaited explicitly by the PowerShell smoke before asserting managed output.
# shellcheck disable=SC2016
grep -Fq 'Start-Process -FilePath $setup' "$powershell_smoke" || fail "PowerShell setup smoke does not launch NSIS through Start-Process"
grep -Fq -- "-ArgumentList '/S' -Wait -PassThru" "$powershell_smoke" || fail "PowerShell setup smoke does not wait for the NSIS process"
if grep -Eiq 'WriteUninstaller|ProgramFiles|cgm|chatgpt-mcp' "$template"; then
	fail "setup contains a second uninstall/root path or retired executable identity"
fi

echo "Windows setup wrapper contract OK"
