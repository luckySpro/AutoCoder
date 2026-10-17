#!/bin/sh
# 把桌面版二进制打成 macOS 应用：scripts/macapp.sh <二进制> <版本> <输出目录>
# 只显示菜单栏图标，不占 Dock（LSUIElement）。需要在 macOS 上运行（用到 iconutil）。
set -eu
bin=$1; ver=$2; out=$3
app="$out/AutoCoder.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin" "$app/Contents/MacOS/autocoder"
chmod +x "$app/Contents/MacOS/autocoder"
tmp=$(mktemp -d)
go run scripts/icongen.go "$tmp/AutoCoder.iconset"
iconutil -c icns -o "$app/Contents/Resources/AutoCoder.icns" "$tmp/AutoCoder.iconset"
rm -rf "$tmp"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>AutoCoder</string>
  <key>CFBundleDisplayName</key><string>AutoCoder</string>
  <key>CFBundleIdentifier</key><string>io.github.luckyspro.autocoder</string>
  <key>CFBundleExecutable</key><string>autocoder</string>
  <key>CFBundleIconFile</key><string>AutoCoder</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${ver#v}</string>
  <key>CFBundleVersion</key><string>${ver#v}</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
echo "$app"
