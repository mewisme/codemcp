#!/bin/sh
set -eu

binary=${1:-}
target=${2:-}

case "$target" in
	linux_amd64*|linux_arm64*|windows_amd64*) ;;
	windows_arm64*|darwin_*) exit 0 ;;
	*)
		echo "release pack: unsupported target $target" >&2
		exit 2
		;;
esac

if [ -z "$binary" ] || [ ! -f "$binary" ] || [ -L "$binary" ]; then
	echo "release pack: binary must be a regular non-symlink file: $binary" >&2
	exit 2
fi

upx=${UPX:-upx}
if ! command -v "$upx" >/dev/null 2>&1; then
	echo "release pack: UPX executable is required: $upx" >&2
	exit 2
fi

"$upx" --best "$binary"
