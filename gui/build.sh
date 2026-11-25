#!/bin/sh
set -e
cd "$(dirname "$0")"
A=Huddle.app/Contents
mkdir -p $A/MacOS
swiftc -parse-as-library -O HuddleGUI.swift -o $A/MacOS/Huddle
cat > $A/Info.plist <<P
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>Huddle</string>
<key>CFBundleIdentifier</key><string>dev.huddle.gui</string>
<key>CFBundleName</key><string>Huddle</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
P
