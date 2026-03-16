#!/usr/bin/env bash
# build_unix.sh — Build LLaMA Runner on macOS or Linux.
#
# macOS requirements:
#   - Xcode Command Line Tools: xcode-select --install
#   - Go 1.21+: https://go.dev/dl/
#
# Linux requirements:
#   - Go 1.21+: https://go.dev/dl/
#   - gcc
#   - GTK3 + WebKit2GTK dev headers:
#       Ubuntu/Debian: sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev
#                      (try libwebkit2gtk-4.1-dev on Ubuntu 23.04+)
#       Fedora:        sudo dnf install gtk3-devel webkit2gtk4.0-devel
#       Arch:          sudo pacman -S webkit2gtk
set -e

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
esac

export CGO_ENABLED=1
export GOOS="${OS}"
export GOARCH="${ARCH}"

go mod tidy

if [ "$OS" = "darwin" ]; then
    # macOS GUI apps must be placed inside a .app bundle to work correctly
    # with Cocoa's main-thread requirement and Dock integration.
    APP="llama-runner.app"
    BINARY="${APP}/Contents/MacOS/llama-runner"
    mkdir -p "${APP}/Contents/MacOS"
    mkdir -p "${APP}/Contents/Resources"

    echo "Building ${APP} (darwin/${ARCH})..."
    go build -ldflags="-s -w" -o "${BINARY}" .

    # Write a minimal Info.plist so macOS recognises it as a proper app bundle.
    cat > "${APP}/Contents/Info.plist" << 'XML'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>     <string>llama-runner</string>
    <key>CFBundleIdentifier</key>     <string>io.github.llama-runner</string>
    <key>CFBundleName</key>           <string>LLaMA Runner</string>
    <key>CFBundleVersion</key>        <string>1.0.0</string>
    <key>CFBundleShortVersionString</key> <string>1.0.0</string>
    <key>NSHighResolutionCapable</key> <true/>
    <key>NSRequiresAquaSystemAppearance</key> <false/>
</dict>
</plist>
XML

    echo "Build complete: ${APP}"
    echo "To run: open ${APP}"
    echo "Place llama-server (no extension) in the lib/ folder next to ${APP}."
else
    OUTPUT="llama-runner-${OS}-${ARCH}"
    echo "Building ${OUTPUT} (${OS}/${ARCH})..."
    go build -ldflags="-s -w" -o "${OUTPUT}" .
    echo "Build complete: ${OUTPUT}"
    echo "Place the llama-server binary in the lib/ folder next to ${OUTPUT}."
fi
