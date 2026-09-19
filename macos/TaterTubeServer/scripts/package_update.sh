#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
INFO_PLIST="${PROJECT_DIR}/Resources/Info.plist"
APP_DIR="${PROJECT_DIR}/build/Tater Tube Server.app"
RELEASES_DIR="${PROJECT_DIR}/releases"
VERSION="${TATER_TUBE_VERSION:-$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "${INFO_PLIST}")}"
VERSION="${VERSION#v}"
BUILD_NUMBER="${TATER_TUBE_BUILD:-$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "${INFO_PLIST}")}"
VERSION_LABEL="v${VERSION}"
ZIP_NAME="Tater-Tube-Server-${VERSION_LABEL}.zip"
ZIP_PATH="${PROJECT_DIR}/build/${ZIP_NAME}"
RELEASE_ZIP="${RELEASES_DIR}/${ZIP_NAME}"
MANIFEST_PATH="${PROJECT_DIR}/build/update-manifest.json"
REPO_MANIFEST_PATH="${PROJECT_DIR}/update-manifest.json"
DOWNLOAD_URL="${1:-https://github.com/TaterTotterson/tater-tube-server/releases/download/${VERSION_LABEL}/${ZIP_NAME}}"

"${SCRIPT_DIR}/build_app.sh"

mkdir -p "${RELEASES_DIR}"
rm -f "${ZIP_PATH}" "${RELEASE_ZIP}" "${MANIFEST_PATH}"
ditto -c -k --keepParent "${APP_DIR}" "${ZIP_PATH}"
"${SCRIPT_DIR}/notarize_artifact.sh" "${ZIP_PATH}"
if [[ "${TATER_NOTARIZE:-0}" == "1" ]]; then
  xcrun stapler staple "${APP_DIR}"
  xcrun stapler validate "${APP_DIR}"
  rm -f "${ZIP_PATH}"
  ditto -c -k --keepParent "${APP_DIR}" "${ZIP_PATH}"
fi
cp "${ZIP_PATH}" "${RELEASE_ZIP}"
SHA256="$(shasum -a 256 "${ZIP_PATH}" | awk '{print $1}')"

/usr/bin/python3 - "${MANIFEST_PATH}" "${VERSION}" "${BUILD_NUMBER}" "${DOWNLOAD_URL}" "${SHA256}" <<'PY'
import json
import sys

path, version, build, url, sha256 = sys.argv[1:]
with open(path, "w", encoding="utf-8") as handle:
    json.dump({
        "version": version,
        "build": int(build),
        "url": url,
        "sha256": sha256,
        "notes": f"Tater Tube Server macOS update v{version}.",
    }, handle, indent=2)
    handle.write("\n")
PY
cp "${MANIFEST_PATH}" "${REPO_MANIFEST_PATH}"

echo "Packaged ${ZIP_PATH}"
echo "Copied ${RELEASE_ZIP}"
echo "Updated ${REPO_MANIFEST_PATH}"
