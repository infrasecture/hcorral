#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
platform="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
export HCORRAL_TEST_BINARY="${HCORRAL_TEST_BINARY:-${repo_root}/dist/bin/hcorral-${platform}}"
test_binary="${HCORRAL_SESSION_TEST_BINARY:-${repo_root}/dist/tests/session-transfer-${platform}}"
[[ -x "${HCORRAL_TEST_BINARY}" && -x "${test_binary}" ]] || {
  echo 'missing launcher or session acceptance executable; run ./build.sh first' >&2
  exit 2
}

HCORRAL_SESSION_TEST_IMAGE="hcorral-session-fixture:$(date +%s)-$$"
export HCORRAL_SESSION_TEST_IMAGE
trap 'docker image rm "$HCORRAL_SESSION_TEST_IMAGE" >/dev/null 2>&1 || true' EXIT
docker build --quiet --tag "${HCORRAL_SESSION_TEST_IMAGE}" \
  --file "${repo_root}/tests/fixtures/session-image/Dockerfile" "${repo_root}" >/dev/null
# The full serial matrix can exceed 15 minutes through Colima on hosted Macs.
# Keep room for VM overhead; individual transfers and cancellation checks retain
# their shorter deadlines in the test executable.
"${test_binary}" -test.v -test.timeout=30m
# Reuse the same real daemon/image and packaged launcher for the richer native
# Codex histories, including a completed-turn return through public commands.
HCORRAL_NATIVE_DOCKER=1 "${repo_root}/tests/qualification/codex-sessions.sh"
