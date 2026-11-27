#!/bin/sh
# Installs Huddle (the macOS GUI and its Claude Code hook). Builds on your Mac with the Command Line Tools, so nothing is downloaded unsigned.
#   curl -fsSL https://raw.githubusercontent.com/saimageshvar/huddle/master/install.sh | sh
# Env: HUDDLE_REPO (owner/name), HUDDLE_REF (branch or tag), HUDDLE_APP_DIR (default ~/Applications), HUDDLE_SRC (a local checkout, skips the download)
set -e
[ "$(uname -s)" = Darwin ] || { echo "Huddle is macOS only." >&2; exit 1; }
command -v swiftc >/dev/null 2>&1 || { echo "swiftc not found. Run: xcode-select --install, then run this again." >&2; exit 1; }

REPO=${HUDDLE_REPO:-saimageshvar/huddle}
REF=${HUDDLE_REF:-master}
DEST=${HUDDLE_APP_DIR:-$HOME/Applications}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

if [ -n "$HUDDLE_SRC" ]; then
  SRC=$HUDDLE_SRC
else
  echo "Downloading $REPO@$REF ..."
  curl -fsSL "https://github.com/$REPO/archive/$REF.tar.gz" | tar xz -C "$WORK" --strip-components=1
  SRC=$WORK
fi

echo "Building Huddle.app (about a minute) ..."
BUILD=$WORK/build
mkdir -p "$BUILD"
cp -R "$SRC/gui/." "$BUILD/"
rm -rf "$BUILD/Huddle.app"
sh "$BUILD/build.sh" >/dev/null

mkdir -p "$DEST"
pkill -x Huddle 2>/dev/null || true
rm -rf "$DEST/Huddle.app"
cp -R "$BUILD/Huddle.app" "$DEST/Huddle.app"
echo "Installed $DEST/Huddle.app"

"$DEST/Huddle.app/Contents/MacOS/huddle-hook" setup
