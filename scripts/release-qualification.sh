#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/release-versioning.sh
source "${root}/scripts/lib/release-versioning.sh"

version=""
source_commit=""
artifacts_sha256=""
gate=""
status=""
output=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) version="${2:-}"; shift 2 ;;
    --source-commit) source_commit="${2:-}"; shift 2 ;;
    --artifacts-sha256) artifacts_sha256="${2:-}"; shift 2 ;;
    --gate) gate="${2:-}"; shift 2 ;;
    --status) status="${2:-}"; shift 2 ;;
    --output) output="${2:-}"; shift 2 ;;
    *) echo "ERROR: unknown qualification argument: $1" >&2; exit 2 ;;
  esac
done

hcorral_require_stable_version "${version}" || exit 2
[[ "${source_commit}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] || { echo 'ERROR: invalid qualification source commit' >&2; exit 2; }
[[ "${artifacts_sha256}" =~ ^[0-9a-f]{64}$ ]] || { echo 'ERROR: invalid qualification artifact digest' >&2; exit 2; }
case "${gate}" in
  linux-amd64|linux-arm64|darwin-amd64|darwin-arm64|linux-x11|linux-wayland|linux-xwayland|docker-desktop) ;;
  *) echo "ERROR: invalid qualification gate: ${gate}" >&2; exit 2 ;;
esac
case "${status}" in
  passed|waived-preview) ;;
  *) echo "ERROR: invalid qualification status: ${status}" >&2; exit 2 ;;
esac
[[ -n "${output}" ]] || { echo 'ERROR: qualification output is required' >&2; exit 2; }

mkdir -p "$(dirname -- "${output}")"
temporary="${output}.tmp.$$"
trap 'rm -f -- "${temporary}"' EXIT
{
  printf 'FORMAT=1\n'
  printf 'VERSION=%s\n' "${version}"
  printf 'SOURCE_COMMIT=%s\n' "${source_commit}"
  printf 'ARTIFACTS_SHA256=%s\n' "${artifacts_sha256}"
  printf 'GATE=%s\n' "${gate}"
  printf 'STATUS=%s\n' "${status}"
} >"${temporary}"
chmod 0644 "${temporary}"
mv -- "${temporary}" "${output}"
trap - EXIT
