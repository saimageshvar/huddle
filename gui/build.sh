#!/bin/sh
# Builds Huddle.app: the GUI and, beside it, the Foundation-only hook helper (starts in ~4 ms vs ~40 ms with SwiftUI linked).
set -e
cd "$(dirname "$0")"
A=Huddle.app/Contents
mkdir -p $A/MacOS
swiftc -parse-as-library -O Shared/Wire.swift App/*.swift -o $A/MacOS/Huddle
swiftc -O Shared/Wire.swift Hook/main.swift -o $A/MacOS/huddle-hook
cat > $A/Info.plist <<P
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>Huddle</string>
<key>CFBundleIdentifier</key><string>dev.huddle.gui</string>
<key>CFBundleName</key><string>Huddle</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
</dict></plist>
P
