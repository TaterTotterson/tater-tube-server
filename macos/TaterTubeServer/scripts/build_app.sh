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
AI_UPSCALING_DIR="${RESOURCES_DIR}/AI Upscaling"
VULKAN_LIB_DIR="${RESOURCES_DIR}/Vulkan/lib"
VULKAN_ICD_DIR="${RESOURCES_DIR}/Vulkan/icd.d"
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
mkdir -p "${MACOS_DIR}" "${SERVER_DIR}" "${FFMPEG_BIN_DIR}" "${FFMPEG_LIB_DIR}" \
  "${AI_UPSCALING_DIR}" "${VULKAN_LIB_DIR}" "${VULKAN_ICD_DIR}"

cp "${SWIFT_BIN_DIR}/TaterTubeServer" "${MACOS_DIR}/TaterTubeServer"
cp "${INFO_PLIST_SOURCE}" "${CONTENTS_DIR}/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString ${VERSION}" "${CONTENTS_DIR}/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion ${BUILD_NUMBER}" "${CONTENTS_DIR}/Info.plist"
/usr/libexec/PlistBuddy -c "Set :LSMinimumSystemVersion ${MACOS_DEPLOYMENT_TARGET}" "${CONTENTS_DIR}/Info.plist"
cp "${PROJECT_DIR}/build/TaterTubeServerIcon.icns" "${RESOURCES_DIR}/TaterTubeServerIcon.icns"
cp "${PROJECT_DIR}/Resources/install-update.sh" "${RESOURCES_DIR}/install-update.sh"
chmod 755 "${RESOURCES_DIR}/install-update.sh"

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
  local -a pending_sources=("${ffmpeg_path}" "${ffprobe_path}")
  local index=0
  # Bash 3.2 treats an empty array expansion as unset when nounset is active.
  local -a inspected=("__none__")
  local -a source_names=("__none__")
  local -a source_paths=("__none__")

  while (( index < ${#pending[@]} )); do
    local payload="${pending[$index]}"
    local source_payload="${pending_sources[$index]}"
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
        /System/*|/usr/lib/*) continue ;;
        @rpath/*)
          dependency="$(dirname "${source_payload}")/${dependency#@rpath/}"
          ;;
        @loader_path/*)
          dependency="$(dirname "${source_payload}")/${dependency#@loader_path/}"
          ;;
        @executable_path/*)
          dependency="$(dirname "${ffmpeg_path}")/${dependency#@executable_path/}"
          ;;
        @*)
          echo "Unsupported FFmpeg dependency path in ${source_payload}: ${dependency}" >&2
          exit 1
          ;;
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
        pending_sources+=("${dependency}")
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

bundle_moltenvk() {
  local moltenvk_prefix="${TATER_MOLTENVK_PREFIX:-}"
  if [[ -z "${moltenvk_prefix}" ]] && command -v brew >/dev/null 2>&1; then
    moltenvk_prefix="$(brew --prefix molten-vk 2>/dev/null || true)"
  fi
  if [[ -z "${moltenvk_prefix}" ]]; then
    echo "MoltenVK is required for macOS AI upscaling. Install it with: brew install molten-vk" >&2
    exit 1
  fi

  local library_source="${moltenvk_prefix}/lib/libMoltenVK.dylib"
  local manifest_source="${moltenvk_prefix}/etc/vulkan/icd.d/MoltenVK_icd.json"
  local library_destination="${VULKAN_LIB_DIR}/libMoltenVK.dylib"
  local manifest_destination="${VULKAN_ICD_DIR}/MoltenVK_icd.json"
  if [[ ! -f "${library_source}" || ! -f "${manifest_source}" ]]; then
    echo "MoltenVK is incomplete under ${moltenvk_prefix}. Reinstall it with Homebrew." >&2
    exit 1
  fi

  cp -L "${library_source}" "${library_destination}"
  chmod 755 "${library_destination}"
  sed -E \
    's#("library_path"[[:space:]]*:[[:space:]]*)"[^"]+"#\1"../lib/libMoltenVK.dylib"#' \
    "${manifest_source}" > "${manifest_destination}"
  grep -Eq '"library_path"[[:space:]]*:[[:space:]]*"\.\./lib/libMoltenVK\.dylib"' "${manifest_destination}" || {
    echo "Unable to make the bundled MoltenVK manifest relocatable." >&2
    exit 1
  }
}

if [[ "${TATER_BUNDLE_FFMPEG:-1}" == "1" ]]; then
  bundle_ffmpeg
  bundle_moltenvk
else
  rm -rf "${RESOURCES_DIR}/FFmpeg" "${RESOURCES_DIR}/Vulkan"
fi

download_ai_upscaler() {
  local filename="$1"
  local url="$2"
  local checksum="$3"
  local destination="${AI_UPSCALING_DIR}/${filename}"

  curl --fail --location --silent --show-error --retry 3 \
    "${url}" \
    --output "${destination}"
  echo "${checksum}  ${destination}" | shasum -a 256 -c -
  chmod 644 "${destination}"
}

download_ai_upscaler \
  "FSRCNNX_x2_8-0-4-1.glsl" \
  "https://github.com/igv/FSRCNN-TensorFlow/releases/download/1.1/FSRCNNX_x2_8-0-4-1.glsl" \
  "e800dbc5c1c95185cc82216c597724533ff5f2880179f256eef600f03e8dc2ae"
download_ai_upscaler \
  "FSRCNNX_x2_16-0-4-1.glsl" \
  "https://github.com/igv/FSRCNN-TensorFlow/releases/download/1.1/FSRCNNX_x2_16-0-4-1.glsl" \
  "d5a24a271e5d9a3f7f7a053b150c460a44c25b3cf7f770857d57cc3a2e1c9965"
download_ai_upscaler \
  "ArtCNN_C4F16.glsl" \
  "https://github.com/Artoriuz/ArtCNN/releases/download/v1.6.2/ArtCNN_C4F16.glsl" \
  "03d0b3d31cb82c898a94a46663021a3e8f02c5a21d69c5cfdf0208de4bfd453e"
download_ai_upscaler \
  "ArtCNN_C4F32.glsl" \
  "https://github.com/Artoriuz/ArtCNN/releases/download/v1.6.2/ArtCNN_C4F32.glsl" \
  "f773bce6cf5fe7e5e5d599a695edd40df5cd7a20c3d08c4d164d07591d5bead3"

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
  bundled_filters="$("${FFMPEG_BIN_DIR}/ffmpeg" -hide_banner -filters 2>&1)"
  bundled_encoders="$("${FFMPEG_BIN_DIR}/ffmpeg" -hide_banner -encoders 2>&1)"
  for required_filter in zscale libplacebo; do
    if ! grep -Eq "[[:space:]]${required_filter}[[:space:]]" <<< "${bundled_filters}"; then
      echo "Bundled FFmpeg is missing the required ${required_filter} filter. Use Homebrew ffmpeg-full." >&2
      exit 1
    fi
  done
  if ! grep -Eq '[[:space:]]h264_videotoolbox[[:space:]]' <<< "${bundled_encoders}"; then
    echo "Bundled FFmpeg is missing the required h264_videotoolbox encoder." >&2
    exit 1
  fi
  VULKAN_MANIFEST="${VULKAN_ICD_DIR}/MoltenVK_icd.json"
  for shader_name in \
    "FSRCNNX_x2_8-0-4-1.glsl" \
    "FSRCNNX_x2_16-0-4-1.glsl" \
    "ArtCNN_C4F16.glsl" \
    "ArtCNN_C4F32.glsl"; do
    shader_path="${AI_UPSCALING_DIR}/${shader_name}"
    if ! env \
      VK_DRIVER_FILES="${VULKAN_MANIFEST}" \
      VK_ICD_FILENAMES="${VULKAN_MANIFEST}" \
      "${FFMPEG_BIN_DIR}/ffmpeg" \
        -hide_banner -loglevel error -nostdin \
        -f lavfi -i "color=size=64x36:rate=1:duration=1" \
        -vf "libplacebo=w=128:h=72:format=yuv420p:upscaler=spline36:custom_shader_path='${shader_path}'" \
        -frames:v 1 -an -f null -; then
      echo "Bundled FFmpeg could not run ${shader_name} through the packaged MoltenVK driver." >&2
      exit 1
    fi
  done
fi
if [[ "${CODESIGN_IDENTITY}" == "-" ]]; then
  codesign --force --sign - --entitlements "${ENTITLEMENTS}" "${APP_DIR}"
else
  codesign --force --options runtime --timestamp --sign "${CODESIGN_IDENTITY}" --entitlements "${ENTITLEMENTS}" "${APP_DIR}"
fi

codesign --verify --deep --strict --verbose=2 "${APP_DIR}"
echo "Built ${APP_DIR}"
