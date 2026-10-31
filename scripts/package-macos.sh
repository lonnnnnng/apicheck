#!/usr/bin/env zsh
set -euo pipefail

root_dir="${0:A:h:h}"
app_name="API Checker"
app_version="1.5.0"
bundle_id="local.apicheck.desktop"
build_dir="${root_dir}/build/macos"
stage_dir="${build_dir}/stage"
app_path="${build_dir}/${app_name}.app"
iconset_dir="${build_dir}/AppIcon.iconset"
icon_png="${build_dir}/appicon-1024.png"
icon_icns="${app_path}/Contents/Resources/AppIcon.icns"
dmg_path="${build_dir}/${app_name}.dmg"
zip_path="${build_dir}/${app_name}-macOS.zip"

"${root_dir}/scripts/build-desktop.sh"

rm -rf "${build_dir}"
mkdir -p "${app_path}/Contents/MacOS" "${app_path}/Contents/Resources" "${iconset_dir}"

# 作者 long: Quick Look 会把 SVG 栅格化到白色画布上，Dock 里会出现违和的白方块；
# 这里用项目内置 Go 生成器直接输出带透明安全区的 PNG，再交给 iconutil 打包成 icns。
go run "${root_dir}/scripts/icongen" -out "${icon_png}" -size 1024

sips -z 16 16 "${icon_png}" --out "${iconset_dir}/icon_16x16.png" >/dev/null
sips -z 32 32 "${icon_png}" --out "${iconset_dir}/icon_16x16@2x.png" >/dev/null
sips -z 32 32 "${icon_png}" --out "${iconset_dir}/icon_32x32.png" >/dev/null
sips -z 64 64 "${icon_png}" --out "${iconset_dir}/icon_32x32@2x.png" >/dev/null
sips -z 128 128 "${icon_png}" --out "${iconset_dir}/icon_128x128.png" >/dev/null
sips -z 256 256 "${icon_png}" --out "${iconset_dir}/icon_128x128@2x.png" >/dev/null
sips -z 256 256 "${icon_png}" --out "${iconset_dir}/icon_256x256.png" >/dev/null
sips -z 512 512 "${icon_png}" --out "${iconset_dir}/icon_256x256@2x.png" >/dev/null
sips -z 512 512 "${icon_png}" --out "${iconset_dir}/icon_512x512.png" >/dev/null
sips -z 1024 1024 "${icon_png}" --out "${iconset_dir}/icon_512x512@2x.png" >/dev/null
iconutil -c icns "${iconset_dir}" -o "${icon_icns}"

cp "${root_dir}/apicheck-desktop" "${app_path}/Contents/MacOS/apicheck-desktop"
chmod +x "${app_path}/Contents/MacOS/apicheck-desktop"

# 作者 long: 这里生成标准 macOS bundle 元数据，Finder、Dock 和后续 dmg 安装都会读取这些字段。
cat > "${app_path}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>${app_name}</string>
  <key>CFBundleDisplayName</key>
  <string>${app_name}</string>
  <key>CFBundleIdentifier</key>
  <string>${bundle_id}</string>
  <key>CFBundleVersion</key>
  <string>${app_version}</string>
  <key>CFBundleShortVersionString</key>
  <string>${app_version}</string>
  <key>CFBundleExecutable</key>
  <string>apicheck-desktop</string>
  <key>CFBundleIconFile</key>
  <string>AppIcon</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>LSMinimumSystemVersion</key>
  <string>12.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST
printf 'APPL????' > "${app_path}/Contents/PkgInfo"

codesign --force --deep --sign - "${app_path}" >/dev/null

mkdir -p "${stage_dir}"
cp -R "${app_path}" "${stage_dir}/${app_name}.app"
ln -s /Applications "${stage_dir}/Applications"
hdiutil create -volname "${app_name}" -srcfolder "${stage_dir}" -ov -format UDZO "${dmg_path}" >/dev/null
ditto -c -k --sequesterRsrc --keepParent "${app_path}" "${zip_path}"

printf 'APP_PATH=%s\n' "${app_path}"
printf 'DMG_PATH=%s\n' "${dmg_path}"
printf 'ZIP_PATH=%s\n' "${zip_path}"
