#!/usr/bin/env bash
# Generate binary delta (hdiff) patches from the previous release to the
# current release for each supported platform (windows_amd64, linux_amd64),
# then upload them alongside the pinned hpatchz binaries for that platform.
#
# Safe to no-op: missing prev/current assets, oversized patches, or tool
# failures never fail the overall release.
set -euo pipefail

HDIFFPATCH_VERSION="${HDIFFPATCH_VERSION:-5.1.3}"
PATCH_RATIO_THRESHOLD="${PATCH_RATIO_THRESHOLD:-0.5}"
REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"

# Platforms we generate binary patches for: "<os>_<arch> <archive-extension>".
# Linux ships tar.gz; Windows ships zip. The delta itself is platform-agnostic
# (hdiffz runs on the CI Linux host), only the archive layout differs.
PLATFORMS=(
  "windows_amd64 zip"
  "linux_amd64 tar.gz"
)

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
  echo "Unable to resolve release version; skipping delta patches"
  exit 0
fi

if [ "${SIMPLE_RELEASE:-false}" = "true" ]; then
  echo "SIMPLE_RELEASE=true; skipping delta patches"
  exit 0
fi

WORKDIR="$(mktemp -d)"
cleanup() { rm -rf "${WORKDIR}"; }
trap cleanup EXIT

echo "Building delta patches for ${TAG_NAME} (${VERSION})"

# ---------------------------------------------------------------------------
# hdiffz (linux CI host; binary delta is platform-agnostic)
# ---------------------------------------------------------------------------
mkdir -p "${WORKDIR}/tools"
HDIFF_ZIP_URL="https://github.com/sisong/HDiffPatch/releases/download/v${HDIFFPATCH_VERSION}/hdiffpatch_v${HDIFFPATCH_VERSION}_bin_linux64.zip"
curl -fsSL "${HDIFF_ZIP_URL}" -o "${WORKDIR}/tools/hdiff.zip"
unzip -q -o "${WORKDIR}/tools/hdiff.zip" -d "${WORKDIR}/tools/hdiff"
HDIFFZ="$(find "${WORKDIR}/tools/hdiff" -type f -name hdiffz | head -n1 || true)"
if [ -z "${HDIFFZ}" ]; then
  echo "hdiffz not found in HDiffPatch package; skipping"
  exit 0
fi
chmod +x "${HDIFFZ}"

# Fetch hpatchz for each platform once, keyed by <os>_<arch>.
declare -A HPATCHZ_BIN
fetch_hpatchz() {
  local os_arch="$1"
  if [ -n "${HPATCHZ_BIN[$os_arch]:-}" ]; then
    return
  fi
  local pkg=""
  case "${os_arch}" in
    windows_amd64) pkg="windows64";;
    linux_amd64)   pkg="linux64";;
    *) echo "unsupported hpatchz platform ${os_arch}"; return 1;;
  esac
  local zip_url="https://github.com/sisong/HDiffPatch/releases/download/v${HDIFFPATCH_VERSION}/hdiffpatch_v${HDIFFPATCH_VERSION}_bin_${pkg}.zip"
  local dest="${WORKDIR}/tools/hpatch-${pkg}"
  curl -fsSL "${zip_url}" -o "${WORKDIR}/tools/hpatch-${pkg}.zip"
  mkdir -p "${dest}"
  unzip -q -o "${WORKDIR}/tools/hpatch-${pkg}.zip" -d "${dest}"
  local bin
  bin="$(find "${dest}" -type f -name 'hpatchz*' | head -n1 || true)"
  if [ -z "${bin}" ]; then
    echo "hpatchz missing in ${pkg} package; skipping"
    return 1
  fi
  HPATCHZ_BIN["${os_arch}"]="${bin}"
}

# Resolve the previous release tag that has an archive for a given platform.
prev_release_tag() {
  local os_arch="$1"
  local ext="$2"
  mapfile -t RELEASE_TAGS < <(gh release list --repo "${REPO}" --limit 30 --json tagName,isDraft,isPrerelease \
    --jq '.[] | select((.isDraft|not) and (.isPrerelease|not)) | .tagName')
  local candidate cand_ver
  for candidate in "${RELEASE_TAGS[@]}"; do
    cand_ver="${candidate#v}"
    if [ "${cand_ver}" = "${VERSION}" ]; then
      continue
    fi
    # gh release list is newest-first; take the first strictly older tag.
    if [ "$(printf '%s\n%s\n' "${cand_ver}" "${VERSION}" | sort -V | head -n1)" != "${cand_ver}" ] \
      || [ "${cand_ver}" = "${VERSION}" ]; then
      continue
    fi
    rm -rf "${WORKDIR}/prev"
    mkdir -p "${WORKDIR}/prev"
    if gh release download "${candidate}" --repo "${REPO}" -p "sub2api_*_${os_arch}.${ext}" -D "${WORKDIR}/prev" 2>/dev/null; then
      echo "${candidate}"
      return 0
    fi
  done
  return 1
}

# Extract the sub2api binary from an archive into the given directory.
extract_binary() {
  local archive="$1"
  local dest_dir="$2"
  mkdir -p "${dest_dir}"
  case "${archive}" in
    *.zip)    unzip -q -o "${archive}" -d "${dest_dir}";;
    *.tar.gz) tar -xzf "${archive}" -C "${dest_dir}";;
    *) echo "unsupported archive ${archive}"; return 1;;
  esac
}

CHECKSUMS_FILE="${WORKDIR}/patch-checksums.txt"
: > "${CHECKSUMS_FILE}"
UPLOADS=()
UPLOADED_ANY="no"

for entry in "${PLATFORMS[@]}"; do
  read -r os_arch ext <<< "${entry}"
  echo "=== Generating patch for ${os_arch} ==="

  CURRENT_ARCHIVE_NAME="sub2api_${VERSION}_${os_arch}.${ext}"
  if ! gh release download "${TAG_NAME}" --repo "${REPO}" -p "${CURRENT_ARCHIVE_NAME}" -D "${WORKDIR}"; then
    echo "Current ${os_arch} archive not found on ${TAG_NAME}; skipping"
    continue
  fi
  CURRENT_ARCHIVE="${WORKDIR}/${CURRENT_ARCHIVE_NAME}"
  FULL_SIZE="$(wc -c < "${CURRENT_ARCHIVE}" | tr -d ' ')"

  PREV_TAG="$(prev_release_tag "${os_arch}" "${ext}" || true)"
  if [ -z "${PREV_TAG}" ]; then
    echo "No previous ${os_arch} release found; skipping"
    continue
  fi
  PREV_VERSION="${PREV_TAG#v}"
  PREV_ARCHIVE="$(find "${WORKDIR}/prev" -maxdepth 1 -type f -name "sub2api_*_${os_arch}.${ext}" | head -n1 || true)"
  if [ -z "${PREV_ARCHIVE}" ]; then
    echo "Previous ${os_arch} archive missing after download; skipping"
    continue
  fi

  echo "Using base release ${PREV_TAG} -> ${TAG_NAME} (${os_arch})"

  # Extract both binaries (linux archives contain an ELF named `sub2api`,
  # windows archives contain `sub2api.exe`).
  extract_binary "${PREV_ARCHIVE}" "${WORKDIR}/old" || continue
  extract_binary "${CURRENT_ARCHIVE}" "${WORKDIR}/new" || continue

  OLD_EXE="$(find "${WORKDIR}/old" -type f \( -name 'sub2api.exe' -o -name 'sub2api' \) | head -n1 || true)"
  NEW_EXE="$(find "${WORKDIR}/new" -type f \( -name 'sub2api.exe' -o -name 'sub2api' \) | head -n1 || true)"
  if [ -z "${OLD_EXE}" ] || [ -z "${NEW_EXE}" ]; then
    echo "Failed to locate sub2api binary in ${os_arch} archives; skipping"
    continue
  fi

  BASE_SHA256="$(sha256sum "${OLD_EXE}" | awk '{print $1}')"
  RESULT_SHA256="$(sha256sum "${NEW_EXE}" | awk '{print $1}')"

  PATCH_NAME="sub2api_${PREV_VERSION}_to_${VERSION}_${os_arch}.hdiff"
  PATCH_PATH="${WORKDIR}/${PATCH_NAME}"
  JSON_NAME="sub2api_${PREV_VERSION}_to_${VERSION}_${os_arch}.patch.json"
  JSON_PATH="${WORKDIR}/${JSON_NAME}"

  # P0 winner: -WD -s-64
  if ! "${HDIFFZ}" -WD -s-64 "${OLD_EXE}" "${NEW_EXE}" "${PATCH_PATH}"; then
    echo "hdiffz failed for ${os_arch}; skipping"
    continue
  fi

  PATCH_SIZE="$(wc -c < "${PATCH_PATH}" | tr -d ' ')"
  RATIO="$(awk -v p="${PATCH_SIZE}" -v f="${FULL_SIZE}" 'BEGIN { if (f<=0) print 1; else print p/f }')"
  USE_PATCH="$(awk -v r="${RATIO}" -v t="${PATCH_RATIO_THRESHOLD}" 'BEGIN { print (r < t) ? "yes" : "no" }')"

  echo "patch_size=${PATCH_SIZE} full_size=${FULL_SIZE} ratio=${RATIO} threshold=${PATCH_RATIO_THRESHOLD}"

  if [ "${USE_PATCH}" != "yes" ]; then
    echo "Patch ratio ${RATIO} >= ${PATCH_RATIO_THRESHOLD}; not uploading ${os_arch}"
    continue
  fi

  # Publish a pinned hpatchz for this platform so clients can apply the delta.
  if ! fetch_hpatchz "${os_arch}"; then
    continue
  fi
  local_hpatchz="${HPATCHZ_BIN[${os_arch}]}"
  HPATCHZ_ASSET="hpatchz_${os_arch}"
  if [ "${os_arch}" = "windows_amd64" ]; then
    HPATCHZ_ASSET="${HPATCHZ_ASSET}.exe"
  fi
  cp "${local_hpatchz}" "${WORKDIR}/${HPATCHZ_ASSET}"
  chmod +x "${WORKDIR}/${HPATCHZ_ASSET}"

  cat > "${JSON_PATH}" <<EOF
{
  "from": "${PREV_VERSION}",
  "to": "${VERSION}",
  "os": "${os_arch%%_*}",
  "arch": "${os_arch##*_}",
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
  } >> "${CHECKSUMS_FILE}"

  UPLOADS+=("${PATCH_PATH}" "${JSON_PATH}" "${WORKDIR}/${HPATCHZ_ASSET}")
  UPLOADED_ANY="yes"
done

if [ "${UPLOADED_ANY}" = "no" ]; then
  echo "No delta patches generated for any platform"
  exit 0
fi

UPLOADS+=("${CHECKSUMS_FILE}")
gh release upload "${TAG_NAME}" --repo "${REPO}" --clobber "${UPLOADS[@]}"

echo "Uploaded delta patch assets for ${PREV_VERSION} -> ${VERSION}"
