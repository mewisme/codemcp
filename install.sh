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
CHECKSUM_NAME="codemcp_checksums.txt"
SIGNATURE_NAME="${CHECKSUM_NAME}.sigstore.json"

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
ver="${version#v}"
asset="codemcp_${ver}_${os}_${arch}.tar.gz"
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

sigstore_ok=0
sigstore_reason=""
if curl -fsSL "$signature_url" -o "$signature"; then
	if command -v cosign >/dev/null 2>&1; then
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
		sigstore_reason="cosign is not installed or not on PATH"
	fi
else
	sigstore_reason="could not download $SIGNATURE_NAME from $signature_url"
fi
if [ "$sigstore_ok" -eq 0 ]; then
	echo "WARNING: $sigstore_reason; SHA-256 checksum verified, continuing without signature verification." >&2
fi

listing="$tmp/listing.txt"
tar -tzf "$archive" >"$listing"
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

bin_listing="$(tar -tvzf "$archive" cm 2>/dev/null | head -n1 || true)"
[ -n "$bin_listing" ] || {
	echo "cm: archive is missing member cm" >&2
	exit 1
}
case "$bin_listing" in
	l*|L*)
		echo "cm: refusing to extract symlink member cm" >&2
		exit 1
		;;
	-*) ;;
	*)
		echo "cm: refusing non-regular archive member cm" >&2
		exit 1
		;;
esac

extract="$tmp/extract"
mkdir -p "$extract"
tar -xzf "$archive" -C "$extract" cm
binary="$extract/cm"
[ -e "$binary" ] || {
	echo "cm: binary missing from archive." >&2
	exit 1
}
[ ! -L "$binary" ] || {
	echo "cm: refusing symlink binary path." >&2
	exit 1
}
[ -f "$binary" ] || {
	echo "cm: binary missing from archive." >&2
	exit 1
}
chmod +x "$binary"

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
