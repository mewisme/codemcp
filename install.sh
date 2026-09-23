#!/bin/sh
#
# chatgpt-mcp bootstrap installer for macOS / Linux.
#
# curl -fsSL https://get.mewis.me/chatgpt-mcp.sh | sh
# curl -fsSL https://get.mewis.me/chatgpt-mcp.sh | sh -s -- --no-alias
# curl -fsSL https://get.mewis.me/chatgpt-mcp.sh | sh -s -- --uninstall
#
# Environment:
#   CHATGPT_MCP_VERSION           release tag (default: latest)
#   CHATGPT_MCP_INSTALL_DIR       bundle location (default: ~/.chatgpt-mcp)
#   CHATGPT_MCP_BIN_DIR           command location (default: ~/.local/bin)
#   INSTALL_ALLOW_CHECKSUM_ONLY   set to 1 to proceed when Sigstore/cosign
#                                 verification is unavailable (loud warning)
set -eu

REPO="mewisme/codemcp"
INSTALL_DIR="${CHATGPT_MCP_INSTALL_DIR:-$HOME/.chatgpt-mcp}"
BIN_DIR="${CHATGPT_MCP_BIN_DIR:-$HOME/.local/bin}"
NO_ALIAS=0
OIDC_ISSUER="https://token.actions.githubusercontent.com"
SIGNATURE_NAME="checksums.txt.sigstore.json"

for arg in "$@"; do
	case "$arg" in
		--no-alias) NO_ALIAS=1 ;;
		--uninstall)
			rm -f "$BIN_DIR/chatgpt-mcp" "$BIN_DIR/cgm" "$BIN_DIR/cmcp"
			rm -rf "$INSTALL_DIR"
			echo "chatgpt-mcp uninstalled from $INSTALL_DIR"
			exit 0
			;;
		*) echo "chatgpt-mcp: unknown installer option '$arg'." >&2; exit 1 ;;
	esac
done

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) echo "chatgpt-mcp: unsupported OS '$os'." >&2; exit 1 ;;
esac
case "$arch" in
	arm64|aarch64) arch="arm64" ;;
	x86_64|amd64) arch="amd64" ;;
	*) echo "chatgpt-mcp: unsupported architecture '$arch'." >&2; exit 1 ;;
esac

version="${CHATGPT_MCP_VERSION:-}"
if [ -z "$version" ]; then
	version="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed -n 's#.*/releases/tag/##p')"
fi
if [ -z "$version" ]; then
	version="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
fi
[ -n "$version" ] || {
	echo "chatgpt-mcp: could not resolve latest version; set CHATGPT_MCP_VERSION (e.g. v0.1.0)." >&2
	exit 1
}
case "$version" in v*) ;; *) version="v$version" ;; esac
ver="${version#v}"
asset="chatgpt-mcp_${ver}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$version/$asset"
checksums_url="https://github.com/$REPO/releases/download/$version/checksums.txt"
signature_url="https://github.com/$REPO/releases/download/$version/$SIGNATURE_NAME"
cert_identity="https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$version"

echo "Installing chatgpt-mcp $version ($os/$arch)..."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
archive="$tmp/$asset"
checksums="$tmp/checksums.txt"
signature="$tmp/$SIGNATURE_NAME"
curl -fsSL "$url" -o "$archive" || {
	echo "chatgpt-mcp: download failed: $url" >&2
	exit 1
}
curl -fsSL "$checksums_url" -o "$checksums" || {
	echo "chatgpt-mcp: checksum download failed: $checksums_url" >&2
	exit 1
}
expected="$(awk -v asset="$asset" '$2 == asset { print $1; exit }' "$checksums")"
[ -n "$expected" ] || {
	echo "chatgpt-mcp: checksum missing for $asset" >&2
	exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
	actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
else
	echo "chatgpt-mcp: sha256sum or shasum is required to verify the release archive" >&2
	exit 1
fi
[ "$actual" = "$expected" ] || {
	echo "chatgpt-mcp: checksum verification failed for $asset" >&2
	exit 1
}

sigstore_ok=0
signature_available=0
if curl -fsSL "$signature_url" -o "$signature"; then
	signature_available=1
fi
if [ "$signature_available" -eq 1 ] && command -v cosign >/dev/null 2>&1; then
	if cosign verify-blob \
		--bundle="$signature" \
		--certificate-identity="$cert_identity" \
		--certificate-oidc-issuer="$OIDC_ISSUER" \
		"$checksums"; then
		sigstore_ok=1
		echo "Sigstore signature verified for checksums.txt."
	else
		echo "chatgpt-mcp: Sigstore/cosign verification failed for $SIGNATURE_NAME" >&2
		exit 1
	fi
fi
if [ "$sigstore_ok" -eq 0 ]; then
	if [ "${INSTALL_ALLOW_CHECKSUM_ONLY:-}" = "1" ]; then
		echo "WARNING: Sigstore/cosign verification unavailable; proceeding with checksum-only install because INSTALL_ALLOW_CHECKSUM_ONLY=1." >&2
		echo "WARNING: Install cosign and ensure $SIGNATURE_NAME is published for full release integrity." >&2
	else
		echo "chatgpt-mcp: Sigstore/cosign verification is required but unavailable." >&2
		if [ "$signature_available" -eq 0 ]; then
			echo "chatgpt-mcp: could not download $SIGNATURE_NAME from $signature_url" >&2
		elif ! command -v cosign >/dev/null 2>&1; then
			echo "chatgpt-mcp: cosign is not installed or not on PATH" >&2
		fi
		echo "chatgpt-mcp: install cosign, or set INSTALL_ALLOW_CHECKSUM_ONLY=1 to proceed with checksum-only verification." >&2
		exit 1
	fi
fi

listing="$tmp/listing.txt"
tar -tzf "$archive" >"$listing"
while IFS= read -r member || [ -n "$member" ]; do
	[ -n "$member" ] || continue
	normalized="$(printf '%s' "$member" | sed 's/\\/\//g')"
	case "$normalized" in
		''|'.'|'./')
			echo "chatgpt-mcp: unsafe archive path '$member'" >&2
			exit 1
			;;
		/*|*:* )
			echo "chatgpt-mcp: unsafe archive path '$member'" >&2
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
			echo "chatgpt-mcp: unsafe archive path '$member'" >&2
			exit 1
		fi
	done
done <"$listing"

bin_listing="$(tar -tvzf "$archive" chatgpt-mcp 2>/dev/null | head -n1 || true)"
[ -n "$bin_listing" ] || {
	echo "chatgpt-mcp: archive is missing member chatgpt-mcp" >&2
	exit 1
}
case "$bin_listing" in
	l*|L*)
		echo "chatgpt-mcp: refusing to extract symlink member chatgpt-mcp" >&2
		exit 1
		;;
	-*) ;;
	*)
		echo "chatgpt-mcp: refusing non-regular archive member chatgpt-mcp" >&2
		exit 1
		;;
esac

extract="$tmp/extract"
mkdir -p "$extract"
tar -xzf "$archive" -C "$extract" chatgpt-mcp
binary="$extract/chatgpt-mcp"
[ -e "$binary" ] || {
	echo "chatgpt-mcp: binary missing from archive." >&2
	exit 1
}
[ ! -L "$binary" ] || {
	echo "chatgpt-mcp: refusing symlink binary path." >&2
	exit 1
}
[ -f "$binary" ] || {
	echo "chatgpt-mcp: binary missing from archive." >&2
	exit 1
}
chmod +x "$binary"

if [ "$NO_ALIAS" -eq 1 ]; then
	"$binary" install --no-alias
else
	"$binary" install
fi

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
if [ "$NO_ALIAS" -eq 1 ]; then
	echo "Done. Run: chatgpt-mcp --help"
else
	echo "Done. Run: chatgpt-mcp --help or cgm --help"
fi
