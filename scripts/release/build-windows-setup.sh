#!/bin/sh
set -eu

binary=${1:-}
target=${2:-}
dist=${3:-dist}
version=${4:-0.0.0}

if [ -z "$target" ]; then
	echo "windows setup: target is required" >&2
	exit 2
fi

case "$target" in
	windows_amd64|windows_amd64_*) arch=amd64 ;;
	windows_*)
		echo "windows setup: unsupported Windows target $target" >&2
		exit 2
		;;
	*)
		# GoReleaser runs the hook for every target; native setup generation
		# is intentionally inert for non-Windows builds.
		exit 0
		;;
esac

if [ -z "$binary" ]; then
	echo "windows setup: binary path is required for $target" >&2
	exit 2
fi
case "$binary" in
	/*) ;;
	*)
		echo "windows setup: binary path must be absolute: $binary" >&2
		exit 2
		;;
esac
if [ "$(basename "$binary")" != "cm.exe" ]; then
	echo "windows setup: expected canonical cm.exe, got $binary" >&2
	exit 2
fi
if [ ! -f "$binary" ] || [ -L "$binary" ]; then
	echo "windows setup: binary must be a regular non-symlink file: $binary" >&2
	exit 2
fi
binary_bytes=$(wc -c <"$binary" | tr -d '[:space:]')
case "$binary_bytes" in
	''|*[!0-9]*)
		echo "windows setup: could not determine binary size" >&2
		exit 2
		;;
esac
if [ "$binary_bytes" -le 0 ] || [ "$binary_bytes" -gt 268435456 ]; then
	echo "windows setup: binary size is outside the allowed range: $binary_bytes" >&2
	exit 2
fi

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH='' cd -- "$script_dir/../.." && pwd)
template="$repo_root/installer/windows/codemcp.nsi"
if [ ! -f "$template" ] || [ -L "$template" ]; then
	echo "windows setup: NSIS template is missing or unsafe: $template" >&2
	exit 2
fi

mkdir -p "$dist"
dist=$(CDPATH='' cd -- "$dist" && pwd)
output="$dist/codemcp_windows_${arch}_setup.exe"
if [ -e "$output" ] || [ -L "$output" ]; then
	echo "windows setup: refusing to overwrite existing setup artifact: $output" >&2
	exit 2
fi

makensis=${MAKENSIS:-makensis}
if ! command -v "$makensis" >/dev/null 2>&1; then
	echo "windows setup: makensis is required for Windows targets" >&2
	exit 2
fi

tmpdir=$(mktemp -d "$dist/.codemcp-windows-${arch}-setup.XXXXXX")
cleanup() {
	rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM
staged="$tmpdir/setup.exe"

numeric_version=${version#v}
numeric_version=${numeric_version%%-*}
numeric_version=${numeric_version%%+*}
major=${numeric_version%%.*}
remainder=${numeric_version#*.}
if [ "$remainder" = "$numeric_version" ]; then
	echo "windows setup: invalid release version $version" >&2
	exit 2
fi
minor=${remainder%%.*}
patch=${remainder#*.}
if [ "$patch" = "$remainder" ] || [ -z "$patch" ] || [ "${patch#*.}" != "$patch" ]; then
	echo "windows setup: invalid release version $version" >&2
	exit 2
fi
for part in "$major" "$minor" "$patch"; do
	case "$part" in
		''|*[!0-9]*)
			echo "windows setup: invalid release version $version" >&2
			exit 2
			;;
	esac
done
setup_version="$major.$minor.$patch.0"

"$makensis" -V2 \
	"-DBINARY_PATH=$binary" \
	"-DOUTPUT_PATH=$staged" \
	"-DSETUP_ARCH=$arch" \
	"-DSETUP_VERSION=$setup_version" \
	"$template"

if [ ! -f "$staged" ] || [ -L "$staged" ]; then
	echo "windows setup: makensis did not emit a regular setup executable" >&2
	exit 2
fi
setup_bytes=$(wc -c <"$staged" | tr -d '[:space:]')
case "$setup_bytes" in
	''|*[!0-9]*)
		echo "windows setup: could not determine setup size" >&2
		exit 2
		;;
esac
if [ "$setup_bytes" -le 0 ] || [ "$setup_bytes" -gt 536870912 ]; then
	echo "windows setup: generated setup size is outside the allowed range: $setup_bytes" >&2
	exit 2
fi

mv "$staged" "$output"
printf '%s\n' "$output"
