#!/bin/sh
#
# CodeMCP bootstrap installer for macOS / Linux.
#
# curl -fsSL https://get.mewis.me/codemcp.sh | sh
# curl -fsSL https://get.mewis.me/codemcp.sh | sh -s -- --uninstall
#
# Environment:
#   CM_VERSION           release tag (default: latest)
#   CM_INSTALL_DIR       bundle location (default: ~/.cm)
#   CM_BIN_DIR           command location (default: ~/.local/bin)
set -eu

REPO="mewisme/codemcp"
INSTALL_DIR="${CM_INSTALL_DIR:-$HOME/.cm}"
BIN_DIR="${CM_BIN_DIR:-$HOME/.local/bin}"
OIDC_ISSUER="https://token.actions.githubusercontent.com"
PACKAGE_NAME="codemcp"
CHECKSUM_NAME="${PACKAGE_NAME}_checksums.txt"
SIGNATURE_NAME="${CHECKSUM_NAME}.sigstore.json"
BINARY_NAME="cm"
MAX_ARCHIVE_ENTRIES=4096
MAX_BINARY_BYTES=268435456

for arg in "$@"; do
	case "$arg" in
		--uninstall)
			candidate="$BIN_DIR/cm"
			if [ ! -x "$candidate" ] && [ -x "$INSTALL_DIR/current/cm" ]; then
				candidate="$INSTALL_DIR/current/cm"
			fi
			if [ ! -x "$candidate" ]; then
				echo "cm: installed CodeMCP executable not found; refusing to remove shared state under $INSTALL_DIR automatically." >&2
				exit 1
			fi
			"$candidate" _service uninstall
			rmdir "$INSTALL_DIR/state" 2>/dev/null || true
			rmdir "$INSTALL_DIR" 2>/dev/null || true
			echo "CodeMCP uninstalled from $INSTALL_DIR"
			exit 0
			;;
		*) echo "cm: unknown installer option '$arg'." >&2; exit 1 ;;
	esac
done

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) echo "cm: unsupported OS '$os'." >&2; exit 1 ;;
esac
case "$arch" in
	arm64|aarch64) arch="arm64" ;;
	x86_64|amd64) arch="amd64" ;;
	*) echo "cm: unsupported architecture '$arch'." >&2; exit 1 ;;
esac
case "$os/$arch" in
	linux/amd64|linux/arm64|darwin/amd64) ;;
	*)
		echo "cm: unsupported release platform '$os/$arch'; supported: linux/amd64, linux/arm64, darwin/amd64." >&2
		exit 1
		;;
esac

version="${CM_VERSION:-}"
if [ -z "$version" ]; then
	version="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed -n 's#.*/releases/tag/##p')"
fi
if [ -z "$version" ]; then
	version="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
fi
[ -n "$version" ] || {
	echo "cm: could not resolve latest version; set CM_VERSION (e.g. v0.1.0)." >&2
	exit 1
}
case "$version" in v*) ;; *) version="v$version" ;; esac
asset="${PACKAGE_NAME}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$version/$asset"
checksums_url="https://github.com/$REPO/releases/download/$version/$CHECKSUM_NAME"
signature_url="https://github.com/$REPO/releases/download/$version/$SIGNATURE_NAME"
cert_identity="https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$version"

echo "Installing CodeMCP $version ($os/$arch)..."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
archive="$tmp/$asset"
checksums="$tmp/$CHECKSUM_NAME"
signature="$tmp/$SIGNATURE_NAME"
curl -fsSL "$url" -o "$archive" || {
	echo "cm: download failed: $url" >&2
	exit 1
}
curl -fsSL "$checksums_url" -o "$checksums" || {
	echo "cm: checksum download failed: $checksums_url" >&2
	exit 1
}
expected="$(awk -v asset="$asset" '$2 == asset { print $1; exit }' "$checksums")"
[ -n "$expected" ] || {
	echo "cm: checksum missing for $asset" >&2
	exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
	actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
else
	echo "cm: sha256sum or shasum is required to verify the release archive" >&2
	exit 1
fi
[ "$actual" = "$expected" ] || {
	echo "cm: checksum verification failed for $asset" >&2
	exit 1
}

if command -v cosign >/dev/null 2>&1; then
	sigstore_ok=0
	sigstore_reason=""
	if curl -fsSL "$signature_url" -o "$signature"; then
		if cosign verify-blob \
			--bundle="$signature" \
			--certificate-identity="$cert_identity" \
			--certificate-oidc-issuer="$OIDC_ISSUER" \
			"$checksums"; then
			sigstore_ok=1
			echo "Sigstore signature verified for $CHECKSUM_NAME."
		else
			sigstore_reason="Sigstore/cosign verification failed for $SIGNATURE_NAME"
		fi
	else
		sigstore_reason="could not download $SIGNATURE_NAME from $signature_url"
	fi
	if [ "$sigstore_ok" -eq 0 ]; then
		echo "WARNING: $sigstore_reason; SHA-256 checksum verified, continuing without signature verification." >&2
	fi
fi

listing="$tmp/listing.txt"
tar -tzf "$archive" >"$listing"
verbose_listing="$tmp/listing.verbose.txt"
tar -tvzf "$archive" >"$verbose_listing"
entry_count="$(wc -l <"$listing" | tr -d '[:space:]')"
case "$entry_count" in
	''|*[!0-9]*) echo "cm: invalid archive entry count." >&2; exit 1 ;;
esac
[ "$entry_count" -le "$MAX_ARCHIVE_ENTRIES" ] || {
	echo "cm: release archive exceeds $MAX_ARCHIVE_ENTRIES entry limit" >&2
	exit 1
}
while IFS= read -r member || [ -n "$member" ]; do
	[ -n "$member" ] || continue
	normalized="$(printf '%s' "$member" | sed 's/\\/\//g')"
	case "$normalized" in
		''|'.'|'./')
			echo "cm: unsafe archive path '$member'" >&2
			exit 1
			;;
		/*|*:* )
			echo "cm: unsafe archive path '$member'" >&2
			exit 1
			;;
	esac
	oldifs="$IFS"
	IFS=/
	# shellcheck disable=SC2086
	set -- $normalized
	IFS="$oldifs"
	for segment in "$@"; do
		if [ "$segment" = ".." ]; then
			echo "cm: unsafe archive path '$member'" >&2
			exit 1
		fi
	done
done <"$listing"

while IFS= read -r member_info || [ -n "$member_info" ]; do
	[ -n "$member_info" ] || continue
	case "$member_info" in
		-*|d*) ;;
		*)
			echo "cm: release archive contains unsupported entry type" >&2
			exit 1
			;;
	esac
done <"$verbose_listing"

canonical_count="$(awk -v binary="$BINARY_NAME" '$0 == binary { count++ } END { print count+0 }' "$listing")"
[ "$canonical_count" -eq 1 ] || {
	if [ "$canonical_count" -eq 0 ]; then
		echo "cm: archive is missing member $BINARY_NAME" >&2
	else
		echo "cm: release archive contains duplicate $BINARY_NAME" >&2
	fi
	exit 1
}
bin_listing="$(tar -tvzf "$archive" "$BINARY_NAME" 2>/dev/null || true)"
[ -n "$bin_listing" ] || {
	echo "cm: archive is missing member $BINARY_NAME" >&2
	exit 1
}
bin_listing_count="$(printf '%s\n' "$bin_listing" | awk 'NF { count++ } END { print count+0 }')"
[ "$bin_listing_count" -eq 1 ] || {
	echo "cm: release archive contains duplicate $BINARY_NAME" >&2
	exit 1
}
case "$bin_listing" in
	l*|L*)
		echo "cm: refusing to extract symlink member $BINARY_NAME" >&2
		exit 1
		;;
	-*) ;;
	*)
		echo "cm: refusing non-regular archive member $BINARY_NAME" >&2
		exit 1
		;;
esac

extract="$tmp/extract"
mkdir -p "$extract"
binary="$extract/$BINARY_NAME"
stream_bytes="$(tar -xOzf "$archive" "$BINARY_NAME" | head -c $((MAX_BINARY_BYTES + 1)) | wc -c | tr -d '[:space:]')"
case "$stream_bytes" in
	''|*[!0-9]*) echo "cm: invalid release binary stream size." >&2; exit 1 ;;
esac
if [ "$stream_bytes" -le 0 ] || [ "$stream_bytes" -gt "$MAX_BINARY_BYTES" ]; then
	echo "cm: release binary has invalid size $stream_bytes" >&2
	exit 1
fi
tar -xzf "$archive" -C "$extract" "$BINARY_NAME"
if [ ! -f "$binary" ] || [ -L "$binary" ]; then
	echo "cm: extracted release binary is not a regular file" >&2
	exit 1
fi
binary_bytes="$(wc -c <"$binary" | tr -d '[:space:]')"
case "$binary_bytes" in
	''|*[!0-9]*) echo "cm: invalid release binary size." >&2; exit 1 ;;
esac
if [ "$binary_bytes" -le 0 ] || [ "$binary_bytes" -gt "$MAX_BINARY_BYTES" ]; then
	echo "cm: release binary has invalid size $binary_bytes" >&2
	exit 1
fi
[ "$binary_bytes" = "$stream_bytes" ] || {
	echo "cm: extracted release binary size mismatch: got $binary_bytes, expected $stream_bytes" >&2
	exit 1
}
chmod +x "$binary"

set +e
("$binary" --version >/dev/null 2>&1) 2>/dev/null
startup_status=$?
set -e
if [ "$startup_status" -ne 0 ]; then
	echo "cm: downloaded CodeMCP binary failed its startup self-check (exit $startup_status)." >&2
	case "$startup_status" in
		135) echo "cm: process terminated by SIGBUS (signal 7)." >&2 ;;
		139) echo "cm: process terminated by SIGSEGV (signal 11)." >&2 ;;
	esac
	if [ "$os" = "linux" ]; then
		host="$(uname -srmo 2>/dev/null || uname -a 2>/dev/null || true)"
		[ -z "$host" ] || echo "cm: host: $host" >&2
	fi
	exit "$startup_status"
fi

"$binary" install

on_path=0
oldifs="$IFS"
IFS=:
for dir in $PATH; do
	[ "$dir" = "$BIN_DIR" ] && on_path=1 && break
done
IFS="$oldifs"
if [ "$on_path" -eq 0 ]; then
	echo ""
	echo "$BIN_DIR is not on your PATH. Add it:"
	echo "  export PATH=\"$BIN_DIR:\$PATH\""
fi

echo ""
echo "Done. Run: cm --help"
