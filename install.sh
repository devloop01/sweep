#!/bin/sh
# Install the prebuilt sweep binary, without Go.
#
#   curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh -s -- v0.2.0
#
# Installs to ~/.local/bin; set SWEEP_INSTALL_DIR to change it. Files curl
# downloads aren't quarantined by macOS, so Gatekeeper doesn't block the binary.
set -eu

REPO=devloop01/sweep
ASSET=sweep-darwin-arm64
VERSION=${1:-}
DIR=${SWEEP_INSTALL_DIR:-"$HOME/.local/bin"}

fail() { echo "install.sh: $*" >&2; exit 1; }

case $VERSION in
  "") URL=https://github.com/$REPO/releases/latest/download ;;
  v[0-9]*) URL=https://github.com/$REPO/releases/download/$VERSION ;;
  *) fail "expected a release tag like v0.2.0, got '$VERSION'." ;;
esac
[ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] ||
  fail "sweep only runs on Apple Silicon Macs (this is $(uname -s) $(uname -m))."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading ${VERSION:-the latest release} of $REPO ..."
for f in "$ASSET" SHA256SUMS; do
  curl -fsSL -o "$TMP/$f" "$URL/$f" ||
    fail "couldn't download $URL/$f. Does that release exist?"
done

(cd "$TMP" && shasum -a 256 -c SHA256SUMS >/dev/null) ||
  fail "checksum mismatch: the download is corrupt. Nothing was installed."

chmod +x "$TMP/$ASSET"
mkdir -p "$DIR"
mv -f "$TMP/$ASSET" "$DIR/sweep"
echo "Installed $("$DIR/sweep" --version) to $DIR/sweep"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *)
    echo
    echo "$DIR isn't on your PATH yet. Add it, then open a new terminal:"
    echo "  zsh:  echo 'export PATH=\"$DIR:\$PATH\"' >> ~/.zprofile"
    echo "  fish: fish_add_path $DIR"
    ;;
esac
