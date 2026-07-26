#!/usr/bin/env bash
# Generate a Windows amd64 binary delta (hdiff) from the previous release's
# windows zip to the current release's windows zip, then upload it.
#
# Safe to no-op: missing prev/current assets, oversized patches, or tool
# failures never fail the overall release.
set -euo pipefail

HDIFFPATCH_VERSION="${HDIFFPATCH_VERSION:-5.1.2}"
PATCH_RATIO_THRESHOLD="${PATCH_RATIO_THRESHOLD:-0.5}"
REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"

if [ "${GITHUB_EVENT_NAME:-}" = "workflow_dispatch" ]; then
  TAG_NAME="${INPUT_TAG:-${GITHUB_REF_NAME:-}}"
else
  TAG_NAME="${GITHUB_REF_NAME:-}"
  TAG_NAME="${TAG_NAME#refs/tags/}"
fi
TAG_NAME="${TAG_NAME#v}"
TAG_NAME="v${TAG_NAME#v}"
VERSION="${TAG_NAME#v}"

if [ -z "${VERSION}" ] || [ "${VERSION}" = "v" ]; then
  echo "Unable to resolve release version; skipping windows patch"
  exit 0
fi

if [ "${SIMPLE_RELEASE:-false}" = "true" ]; then
  echo "SIMPLE_RELEASE=true; skipping windows patch"
  exit 0
fi

WORKDIR="$(mktemp -d)"
cleanup() { rm -rf "${WORKDIR}"; }
trap cleanup EXIT

echo "Building windows hdiff patch for ${TAG_NAME} (${VERSION})"

# ---------------------------------------------------------------------------
# Current windows zip from this release
# ---------------------------------------------------------------------------
CURRENT_ZIP_NAME="sub2api_${VERSION}_windows_amd64.zip"
if ! gh release download "${TAG_NAME}" --repo "${REPO}" -p "${CURRENT_ZIP_NAME}" -D "${WORKDIR}"; then
  echo "Current windows zip not found on ${TAG_NAME}; skipping"
  exit 0
fi
CURRENT_ZIP="${WORKDIR}/${CURRENT_ZIP_NAME}"
FULL_SIZE="$(wc -c < "${CURRENT_ZIP}" | tr -d ' ')"

# ---------------------------------------------------------------------------
# Previous release that has a windows_amd64 zip
# ---------------------------------------------------------------------------
mapfile -t RELEASE_TAGS < <(gh release list --repo "${REPO}" --limit 30 --json tagName,isDraft,isPrerelease \
  --jq '.[] | select((.isDraft|not) and (.isPrerelease|not)) | .tagName')

PREV_TAG=""
for candidate in "${RELEASE_TAGS[@]}"; do
  cand_ver="${candidate#v}"
  if [ "${cand_ver}" = "${VERSION}" ]; then
    continue
  fi
  # gh release list is newest-first; take the first strictly older tag with a windows zip.
  if [ "$(printf '%s\n%s\n' "${cand_ver}" "${VERSION}" | sort -V | head -n1)" != "${cand_ver}" ] \
    || [ "${cand_ver}" = "${VERSION}" ]; then
    continue
  fi
  rm -rf "${WORKDIR}/prev"
  mkdir -p "${WORKDIR}/prev"
  if gh release download "${candidate}" --repo "${REPO}" -p 'sub2api_*_windows_amd64.zip' -D "${WORKDIR}/prev" 2>/dev/null; then
    PREV_TAG="${candidate}"
    break
  fi
done

if [ -z "${PREV_TAG}" ]; then
  echo "No previous windows release found; skipping patch"
  exit 0
fi

PREV_VERSION="${PREV_TAG#v}"
PREV_ZIP="$(find "${WORKDIR}/prev" -maxdepth 1 -type f -name 'sub2api_*_windows_amd64.zip' | head -n1 || true)"
if [ -z "${PREV_ZIP}" ]; then
  echo "Previous windows zip missing after download; skipping"
  exit 0
fi

echo "Using base release ${PREV_TAG} -> ${TAG_NAME}"

# ---------------------------------------------------------------------------
# Extract exes
# ---------------------------------------------------------------------------
mkdir -p "${WORKDIR}/old" "${WORKDIR}/new" "${WORKDIR}/tools"
unzip -q -o "${PREV_ZIP}" -d "${WORKDIR}/old"
unzip -q -o "${CURRENT_ZIP}" -d "${WORKDIR}/new"

OLD_EXE="$(find "${WORKDIR}/old" -type f \( -name 'sub2api.exe' -o -name 'sub2api' \) | head -n1 || true)"
NEW_EXE="$(find "${WORKDIR}/new" -type f \( -name 'sub2api.exe' -o -name 'sub2api' \) | head -n1 || true)"
if [ -z "${OLD_EXE}" ] || [ -z "${NEW_EXE}" ]; then
  echo "Failed to locate sub2api binary in archives; skipping"
  exit 0
fi

BASE_SHA256="$(sha256sum "${OLD_EXE}" | awk '{print $1}')"
RESULT_SHA256="$(sha256sum "${NEW_EXE}" | awk '{print $1}')"

# ---------------------------------------------------------------------------
# hdiffz (linux CI host; binary delta is platform-agnostic)
# ---------------------------------------------------------------------------
HDIFF_ZIP_URL="https://github.com/sisong/HDiffPatch/releases/download/v${HDIFFPATCH_VERSION}/hdiffpatch_v${HDIFFPATCH_VERSION}_bin_linux64.zip"
curl -fsSL "${HDIFF_ZIP_URL}" -o "${WORKDIR}/tools/hdiff.zip"
unzip -q -o "${WORKDIR}/tools/hdiff.zip" -d "${WORKDIR}/tools/hdiff"
HDIFFZ="$(find "${WORKDIR}/tools/hdiff" -type f -name hdiffz | head -n1 || true)"
if [ -z "${HDIFFZ}" ]; then
  echo "hdiffz not found in HDiffPatch package; skipping"
  exit 0
fi
chmod +x "${HDIFFZ}"

PATCH_NAME="sub2api_${PREV_VERSION}_to_${VERSION}_windows_amd64.hdiff"
PATCH_PATH="${WORKDIR}/${PATCH_NAME}"
JSON_NAME="sub2api_${PREV_VERSION}_to_${VERSION}_windows_amd64.patch.json"
JSON_PATH="${WORKDIR}/${JSON_NAME}"

# P0 winner: -WD -s-64 (~18% of full zip on 0.1.165->0.1.167)
if ! "${HDIFFZ}" -WD -s-64 "${OLD_EXE}" "${NEW_EXE}" "${PATCH_PATH}"; then
  echo "hdiffz failed; skipping patch upload"
  exit 0
fi

PATCH_SIZE="$(wc -c < "${PATCH_PATH}" | tr -d ' ')"
# bash arithmetic with floats via awk
RATIO="$(awk -v p="${PATCH_SIZE}" -v f="${FULL_SIZE}" 'BEGIN { if (f<=0) print 1; else print p/f }')"
USE_PATCH="$(awk -v r="${RATIO}" -v t="${PATCH_RATIO_THRESHOLD}" 'BEGIN { print (r < t) ? "yes" : "no" }')"

echo "patch_size=${PATCH_SIZE} full_size=${FULL_SIZE} ratio=${RATIO} threshold=${PATCH_RATIO_THRESHOLD}"

if [ "${USE_PATCH}" != "yes" ]; then
  echo "Patch ratio ${RATIO} >= ${PATCH_RATIO_THRESHOLD}; not uploading"
  exit 0
fi

# Also publish a pinned windows hpatchz for clients (from windows64 package).
HPATCHZ_ASSET="hpatchz_windows_amd64.exe"
HPATCHZ_WIN_ZIP_URL="https://github.com/sisong/HDiffPatch/releases/download/v${HDIFFPATCH_VERSION}/hdiffpatch_v${HDIFFPATCH_VERSION}_bin_windows64.zip"
curl -fsSL "${HPATCHZ_WIN_ZIP_URL}" -o "${WORKDIR}/tools/hpatch-win.zip"
unzip -q -o "${WORKDIR}/tools/hpatch-win.zip" -d "${WORKDIR}/tools/hpatch-win"
HPATCHZ_WIN="$(find "${WORKDIR}/tools/hpatch-win" -type f -name 'hpatchz.exe' | head -n1 || true)"
if [ -z "${HPATCHZ_WIN}" ]; then
  echo "windows hpatchz.exe missing; skipping patch upload"
  exit 0
fi
cp "${HPATCHZ_WIN}" "${WORKDIR}/${HPATCHZ_ASSET}"

cat > "${JSON_PATH}" <<EOF
{
  "from": "${PREV_VERSION}",
  "to": "${VERSION}",
  "os": "windows",
  "arch": "amd64",
  "base_sha256": "${BASE_SHA256}",
  "result_sha256": "${RESULT_SHA256}",
  "patch_size": ${PATCH_SIZE},
  "full_size": ${FULL_SIZE},
  "tool": "hdiffz",
  "tool_version": "${HDIFFPATCH_VERSION}",
  "hdiff_args": "-WD -s-64",
  "hpatchz_asset": "${HPATCHZ_ASSET}",
  "patch_asset": "${PATCH_NAME}"
}
EOF

# Append patch checksum lines into a sidecar so clients can verify without
# rewriting goreleaser's checksums.txt.
{
  sha256sum "${PATCH_PATH}" | awk '{print $1"  '"${PATCH_NAME}"'"}'
  sha256sum "${WORKDIR}/${HPATCHZ_ASSET}" | awk '{print $1"  '"${HPATCHZ_ASSET}"'"}'
  sha256sum "${JSON_PATH}" | awk '{print $1"  '"${JSON_NAME}"'"}'
} > "${WORKDIR}/windows-patch-checksums.txt"

gh release upload "${TAG_NAME}" \
  --repo "${REPO}" \
  --clobber \
  "${PATCH_PATH}" \
  "${JSON_PATH}" \
  "${WORKDIR}/${HPATCHZ_ASSET}" \
  "${WORKDIR}/windows-patch-checksums.txt"

echo "Uploaded windows patch assets for ${PREV_VERSION} -> ${VERSION}"
