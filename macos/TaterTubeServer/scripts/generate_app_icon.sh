#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
REPO_ROOT="$(cd "${PROJECT_DIR}/../.." && pwd -P)"
SOURCE_IMAGE="${TATER_TUBE_SERVER_ICON_SOURCE:-${REPO_ROOT}/frontend/public/tater-tube-server-icon.png}"
ICONSET_DIR="${PROJECT_DIR}/build/TaterTubeServerIcon.iconset"
OUTPUT_ICON="${PROJECT_DIR}/build/TaterTubeServerIcon.icns"

if [[ ! -f "${SOURCE_IMAGE}" ]]; then
  echo "Missing server icon source: ${SOURCE_IMAGE}" >&2
  exit 1
fi

rm -rf "${ICONSET_DIR}" "${OUTPUT_ICON}"
mkdir -p "${ICONSET_DIR}"

make_icon() {
  local pixels="$1"
  local output="$2"
  sips -z "${pixels}" "${pixels}" "${SOURCE_IMAGE}" --out "${ICONSET_DIR}/${output}" >/dev/null
}

make_icon 16 icon_16x16.png
make_icon 32 icon_16x16@2x.png
make_icon 32 icon_32x32.png
make_icon 64 icon_32x32@2x.png
make_icon 128 icon_128x128.png
make_icon 256 icon_128x128@2x.png
make_icon 256 icon_256x256.png
make_icon 512 icon_256x256@2x.png
make_icon 512 icon_512x512.png
make_icon 1024 icon_512x512@2x.png

iconutil -c icns "${ICONSET_DIR}" -o "${OUTPUT_ICON}"
echo "Generated ${OUTPUT_ICON}"
