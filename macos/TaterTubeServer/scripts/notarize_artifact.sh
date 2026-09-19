#!/bin/bash
set -euo pipefail

ARTIFACT="${1:?Usage: notarize_artifact.sh /path/to/artifact}"

if [[ "${TATER_NOTARIZE:-0}" != "1" ]]; then
  echo "Skipping notarization for ${ARTIFACT} (set TATER_NOTARIZE=1 to enable)."
  exit 0
fi
if [[ ! -e "${ARTIFACT}" ]]; then
  echo "Cannot notarize missing artifact: ${ARTIFACT}" >&2
  exit 1
fi

OUTPUT="$(mktemp "${TMPDIR:-/tmp}/tater-tube-server-notary.XXXXXX")"
trap 'rm -f "${OUTPUT}"' EXIT

if [[ -n "${TATER_NOTARY_PROFILE:-}" ]]; then
  xcrun notarytool submit "${ARTIFACT}" --wait --output-format json --keychain-profile "${TATER_NOTARY_PROFILE}" > "${OUTPUT}"
elif [[ -n "${APPLE_API_KEY_PATH:-}" && -n "${APPLE_API_KEY_ID:-}" && -n "${APPLE_API_ISSUER_ID:-}" ]]; then
  xcrun notarytool submit "${ARTIFACT}" --wait --output-format json \
    --key "${APPLE_API_KEY_PATH}" --key-id "${APPLE_API_KEY_ID}" --issuer "${APPLE_API_ISSUER_ID}" > "${OUTPUT}"
elif [[ -n "${APPLE_ID:-}" && -n "${APPLE_TEAM_ID:-}" && -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" ]]; then
  xcrun notarytool submit "${ARTIFACT}" --wait --output-format json \
    --apple-id "${APPLE_ID}" --team-id "${APPLE_TEAM_ID}" --password "${APPLE_APP_SPECIFIC_PASSWORD}" > "${OUTPUT}"
else
  echo "TATER_NOTARIZE=1, but no notary profile, API key, or Apple ID credentials were configured." >&2
  exit 1
fi

cat "${OUTPUT}"
if ! /usr/bin/python3 - "${OUTPUT}" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    result = json.load(handle)
if result.get("status") != "Accepted":
    raise SystemExit(f"Notarization was not accepted: {result.get('status', 'unknown')}")
PY
then
  submission_id="$(/usr/bin/python3 - "${OUTPUT}" <<'PY'
import json
import sys
with open(sys.argv[1], "r", encoding="utf-8") as handle:
    print(json.load(handle).get("id", ""))
PY
)"
  if [[ -n "${submission_id}" && -n "${TATER_NOTARY_PROFILE:-}" ]]; then
    xcrun notarytool log "${submission_id}" --keychain-profile "${TATER_NOTARY_PROFILE}" || true
  fi
  exit 1
fi
