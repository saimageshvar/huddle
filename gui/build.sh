#!/bin/sh
# Builds Huddle.app: the GUI and, beside it, the Foundation-only hook helper (starts in ~4 ms vs ~40 ms with SwiftUI linked).
set -e
cd "$(dirname "$0")"
A=Huddle.app/Contents
mkdir -p $A/MacOS
swiftc -parse-as-library -O Shared/Wire.swift App/*.swift -o $A/MacOS/Huddle
swiftc -O Shared/Wire.swift Hook/*.swift -o $A/MacOS/huddle-hook
mkdir -p $A/Resources
I=$(mktemp -d)/AppIcon.iconset; mkdir -p $I; R=Resources/icon
cp $R/huddle-icon-16.png $I/icon_16x16.png; cp $R/huddle-icon-32.png $I/icon_16x16@2x.png; cp $R/huddle-icon-32.png $I/icon_32x32.png
cp $R/huddle-icon-64.png $I/icon_32x32@2x.png; cp $R/huddle-icon-128.png $I/icon_128x128.png
sips -z 256 256 $R/huddle-icon-1024.png --out $I/icon_128x128@2x.png >/dev/null; cp $I/icon_128x128@2x.png $I/icon_256x256.png
sips -z 512 512 $R/huddle-icon-1024.png --out $I/icon_256x256@2x.png >/dev/null; cp $I/icon_256x256@2x.png $I/icon_512x512.png
cp $R/huddle-icon-1024.png $I/icon_512x512@2x.png
iconutil -c icns $I -o $A/Resources/AppIcon.icns
cat > $A/Info.plist <<P
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>Huddle</string>
<key>CFBundleIdentifier</key><string>dev.huddle.gui</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>CFBundleName</key><string>Huddle</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
</dict></plist>
P
