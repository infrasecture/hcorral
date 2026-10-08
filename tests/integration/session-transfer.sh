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

if [[ -z "${HCORRAL_SESSION_TEST_IMAGE:-}" ]]; then
  HCORRAL_SESSION_TEST_IMAGE="hcorral-session-fixture:$(date +%s)-$$"
  export HCORRAL_SESSION_TEST_IMAGE
  trap 'docker image rm "$HCORRAL_SESSION_TEST_IMAGE" >/dev/null 2>&1 || true' EXIT
  docker build --quiet --tag "$HCORRAL_SESSION_TEST_IMAGE" \
    --file "$repo_root/tests/fixtures/session-image/Dockerfile" "$repo_root" >/dev/null
fi
case "${1:-docker}" in
  docker) "$test_binary" -test.v -test.timeout=2m ;;
  architecture)
    "$test_binary" -test.v -test.timeout=1m -test.run '^TestDockerSessionEndpoints$'
    ;;
  journey) "${repo_root}/tests/qualification/codex-sessions.sh" journey ;;
  *) echo 'usage: session-transfer.sh [docker|architecture|journey]' >&2; exit 2 ;;
esac
