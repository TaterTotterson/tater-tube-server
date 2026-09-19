#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
INFO_PLIST="${PROJECT_DIR}/Resources/Info.plist"
APP_DIR="${PROJECT_DIR}/build/Tater Tube Server.app"
RELEASES_DIR="${PROJECT_DIR}/releases"
CODESIGN_IDENTITY="${TATER_CODESIGN_IDENTITY:--}"
VERSION="${TATER_TUBE_VERSION:-$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "${INFO_PLIST}")}"
VERSION="${VERSION#v}"
VERSION_LABEL="v${VERSION}"
DMG_NAME="Tater-Tube-Server-${VERSION_LABEL}.dmg"
FINAL_DMG="${PROJECT_DIR}/build/${DMG_NAME}"
RELEASE_DMG="${RELEASES_DIR}/${DMG_NAME}"
STAGING_DIR="${PROJECT_DIR}/build/dmg-staging"

"${SCRIPT_DIR}/build_app.sh"

rm -rf "${STAGING_DIR}" "${FINAL_DMG}"
mkdir -p "${STAGING_DIR}"
ditto "${APP_DIR}" "${STAGING_DIR}/Tater Tube Server.app"
ln -s /Applications "${STAGING_DIR}/Applications"

hdiutil create \
  -volname "Install Tater Tube Server ${VERSION_LABEL}" \
  -srcfolder "${STAGING_DIR}" \
  -fs HFS+ \
  -format UDZO \
  -imagekey zlib-level=9 \
  -ov \
  "${FINAL_DMG}" >/dev/null
hdiutil verify "${FINAL_DMG}" >/dev/null

if [[ "${CODESIGN_IDENTITY}" != "-" ]]; then
  codesign --force --timestamp --sign "${CODESIGN_IDENTITY}" "${FINAL_DMG}"
  codesign --verify --verbose=2 "${FINAL_DMG}"
fi

"${SCRIPT_DIR}/notarize_artifact.sh" "${FINAL_DMG}"
if [[ "${TATER_NOTARIZE:-0}" == "1" ]]; then
  xcrun stapler staple "${FINAL_DMG}"
  xcrun stapler validate "${FINAL_DMG}"
fi

mkdir -p "${RELEASES_DIR}"
cp "${FINAL_DMG}" "${RELEASE_DMG}"
echo "Built ${FINAL_DMG}"
echo "Copied ${RELEASE_DMG}"
