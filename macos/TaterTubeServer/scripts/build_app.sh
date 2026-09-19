#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
REPO_ROOT="$(cd "${PROJECT_DIR}/../.." && pwd -P)"
APP_NAME="Tater Tube Server"
APP_DIR="${PROJECT_DIR}/build/${APP_NAME}.app"
CONTENTS_DIR="${APP_DIR}/Contents"
MACOS_DIR="${CONTENTS_DIR}/MacOS"
RESOURCES_DIR="${CONTENTS_DIR}/Resources"
SERVER_DIR="${RESOURCES_DIR}/Server"
FFMPEG_BIN_DIR="${RESOURCES_DIR}/FFmpeg/bin"
FFMPEG_LIB_DIR="${RESOURCES_DIR}/FFmpeg/lib"
INFO_PLIST_SOURCE="${PROJECT_DIR}/Resources/Info.plist"
ENTITLEMENTS="${TATER_TUBE_SERVER_ENTITLEMENTS:-${PROJECT_DIR}/Resources/TaterTubeServer.entitlements}"
CODESIGN_IDENTITY="${TATER_CODESIGN_IDENTITY:--}"
MACOS_DEPLOYMENT_TARGET="${TATER_TUBE_SERVER_MACOS_DEPLOYMENT_TARGET:-14.0}"

plist_value() {
  /usr/libexec/PlistBuddy -c "Print :$1" "${INFO_PLIST_SOURCE}"
}

VERSION="${TATER_TUBE_VERSION:-$(plist_value CFBundleShortVersionString)}"
VERSION="${VERSION#v}"
BUILD_NUMBER="${TATER_TUBE_BUILD:-$(plist_value CFBundleVersion)}"
GIT_COMMIT="${TATER_TUBE_COMMIT:-$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUILD_TIMESTAMP="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

export MACOSX_DEPLOYMENT_TARGET="${MACOS_DEPLOYMENT_TARGET}"

swift build -c release --package-path "${PROJECT_DIR}"
SWIFT_BIN_DIR="$(swift build -c release --package-path "${PROJECT_DIR}" --show-bin-path)"
"${SCRIPT_DIR}/generate_app_icon.sh"

rm -rf "${APP_DIR}"
mkdir -p "${MACOS_DIR}" "${SERVER_DIR}" "${FFMPEG_BIN_DIR}" "${FFMPEG_LIB_DIR}"

cp "${SWIFT_BIN_DIR}/TaterTubeServer" "${MACOS_DIR}/TaterTubeServer"
cp "${INFO_PLIST_SOURCE}" "${CONTENTS_DIR}/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString ${VERSION}" "${CONTENTS_DIR}/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion ${BUILD_NUMBER}" "${CONTENTS_DIR}/Info.plist"
/usr/libexec/PlistBuddy -c "Set :LSMinimumSystemVersion ${MACOS_DEPLOYMENT_TARGET}" "${CONTENTS_DIR}/Info.plist"
cp "${PROJECT_DIR}/build/TaterTubeServerIcon.icns" "${RESOURCES_DIR}/TaterTubeServerIcon.icns"
cp "${REPO_ROOT}/frontend/public/tater-tube-server-icon.png" "${RESOURCES_DIR}/TaterTubeServerMenuBar.png"

if [[ -n "${TATER_TUBE_SERVER_BINARY:-}" ]]; then
  if [[ ! -x "${TATER_TUBE_SERVER_BINARY}" ]]; then
    echo "TATER_TUBE_SERVER_BINARY is not executable: ${TATER_TUBE_SERVER_BINARY}" >&2
    exit 1
  fi
  cp "${TATER_TUBE_SERVER_BINARY}" "${SERVER_DIR}/tater-tube-server"
else
  if [[ ! -f "${REPO_ROOT}/frontend/dist/index.html" ]]; then
    echo "frontend/dist is missing. Build the frontend before packaging the macOS app." >&2
    exit 1
  fi
  (
    cd "${REPO_ROOT}"
    CGO_ENABLED=1 go build \
      -trimpath \
      -tags=cli \
      -ldflags="-s -w -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Version=${VERSION}' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.GitCommit=${GIT_COMMIT}' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Timestamp=${BUILD_TIMESTAMP}'" \
      -o "${SERVER_DIR}/tater-tube-server" \
      ./cmd/tater-tube-server/main.go
  )
fi
chmod 755 "${SERVER_DIR}/tater-tube-server"

bundle_ffmpeg() {
  local ffmpeg_path="${TATER_FFMPEG_PATH:-$(command -v ffmpeg || true)}"
  local ffprobe_path="${TATER_FFPROBE_PATH:-$(command -v ffprobe || true)}"
  if [[ -z "${ffmpeg_path}" || -z "${ffprobe_path}" ]]; then
    echo "FFmpeg and ffprobe are required. Install them or set TATER_FFMPEG_PATH and TATER_FFPROBE_PATH." >&2
    exit 1
  fi

  cp -L "${ffmpeg_path}" "${FFMPEG_BIN_DIR}/ffmpeg"
  cp -L "${ffprobe_path}" "${FFMPEG_BIN_DIR}/ffprobe"
  chmod 755 "${FFMPEG_BIN_DIR}/ffmpeg" "${FFMPEG_BIN_DIR}/ffprobe"

  local -a pending=("${FFMPEG_BIN_DIR}/ffmpeg" "${FFMPEG_BIN_DIR}/ffprobe")
  local index=0
  # Bash 3.2 treats an empty array expansion as unset when nounset is active.
  local -a inspected=("__none__")
  local -a source_names=("__none__")
  local -a source_paths=("__none__")

  while (( index < ${#pending[@]} )); do
    local payload="${pending[$index]}"
    index=$((index + 1))
    local already_inspected=0
    local inspected_payload
    for inspected_payload in "${inspected[@]}"; do
      if [[ "${inspected_payload}" == "${payload}" ]]; then
        already_inspected=1
        break
      fi
    done
    [[ "${already_inspected}" == "1" ]] && continue
    inspected+=("${payload}")

    while IFS= read -r dependency; do
      case "${dependency}" in
        /System/*|/usr/lib/*|@*) continue ;;
      esac
      [[ -f "${dependency}" ]] || {
        echo "Missing FFmpeg dependency: ${dependency}" >&2
        exit 1
      }
      local name
      name="$(basename "${dependency}")"
      local destination="${FFMPEG_LIB_DIR}/${name}"
      local prior_source=""
      local source_index
      for ((source_index = 0; source_index < ${#source_names[@]}; source_index++)); do
        if [[ "${source_names[$source_index]}" == "${name}" ]]; then
          prior_source="${source_paths[$source_index]}"
          break
        fi
      done
      if [[ -n "${prior_source}" && "${prior_source}" != "${dependency}" ]] && ! cmp -s "${prior_source}" "${dependency}"; then
        echo "Conflicting FFmpeg libraries share the name ${name}." >&2
        exit 1
      fi
      if [[ -z "${prior_source}" ]]; then
        source_names+=("${name}")
        source_paths+=("${dependency}")
      fi
      if [[ ! -f "${destination}" ]]; then
        cp -L "${dependency}" "${destination}"
        chmod u+w "${destination}"
        pending+=("${destination}")
      fi
    done < <(otool -L "${payload}" | tail -n +2 | awk '{print $1}')
  done

  local -a payloads=("${FFMPEG_BIN_DIR}/ffmpeg" "${FFMPEG_BIN_DIR}/ffprobe")
  while IFS= read -r -d '' library; do
    payloads+=("${library}")
  done < <(find "${FFMPEG_LIB_DIR}" -type f -name '*.dylib' -print0)

  for payload in "${payloads[@]}"; do
    while IFS= read -r dependency; do
      local name
      name="$(basename "${dependency}")"
      [[ -f "${FFMPEG_LIB_DIR}/${name}" ]] || continue
      if [[ "${payload}" == "${FFMPEG_LIB_DIR}/"* ]]; then
        install_name_tool -change "${dependency}" "@loader_path/${name}" "${payload}" 2>/dev/null
      else
        install_name_tool -change "${dependency}" "@executable_path/../lib/${name}" "${payload}" 2>/dev/null
      fi
    done < <(otool -L "${payload}" | tail -n +2 | awk '{print $1}')
    if [[ "${payload}" == *.dylib ]]; then
      install_name_tool -id "@rpath/$(basename "${payload}")" "${payload}" 2>/dev/null
    fi
  done
}

if [[ "${TATER_BUNDLE_FFMPEG:-1}" == "1" ]]; then
  bundle_ffmpeg
else
  rmdir "${FFMPEG_BIN_DIR}" "${FFMPEG_LIB_DIR}" "${RESOURCES_DIR}/FFmpeg" 2>/dev/null || true
fi

sign_payload() {
  local payload="$1"
  if [[ "${CODESIGN_IDENTITY}" == "-" ]]; then
    codesign --force --sign - "${payload}"
  else
    codesign --force --options runtime --timestamp --sign "${CODESIGN_IDENTITY}" "${payload}"
  fi
}

while IFS= read -r -d '' payload; do
  if file "${payload}" | grep -q 'Mach-O'; then
    sign_payload "${payload}"
  fi
done < <(find "${RESOURCES_DIR}" -type f -print0)

sign_payload "${MACOS_DIR}/TaterTubeServer"
if [[ -x "${FFMPEG_BIN_DIR}/ffmpeg" ]]; then
  "${FFMPEG_BIN_DIR}/ffmpeg" -hide_banner -version >/dev/null
  "${FFMPEG_BIN_DIR}/ffprobe" -hide_banner -version >/dev/null
fi
if [[ "${CODESIGN_IDENTITY}" == "-" ]]; then
  codesign --force --sign - --entitlements "${ENTITLEMENTS}" "${APP_DIR}"
else
  codesign --force --options runtime --timestamp --sign "${CODESIGN_IDENTITY}" --entitlements "${ENTITLEMENTS}" "${APP_DIR}"
fi

codesign --verify --deep --strict --verbose=2 "${APP_DIR}"
echo "Built ${APP_DIR}"
