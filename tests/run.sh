#!/usr/bin/env bash
# Suite boundaries are explicit: no suite silently invokes another full matrix.
set -euo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
suite="${1:-linux}"
case "$suite" in core|linux|architecture|compose|codex|journey|compat) ;;
  *) echo 'usage: tests/run.sh [core|linux|architecture|compose|codex|journey|compat]' >&2; exit 2 ;;
esac
if [[ "${HCORRAL_TEST_BUDGET_ACTIVE:-}" != 1 ]]; then
  export HCORRAL_TEST_BUDGET_ACTIVE=1
  exec timeout --signal=TERM --kill-after=15s 300 "$0" "$suite"
fi
started=$SECONDS
images=()
registry=""
cleanup() {
  local status=$?
  if [[ -n "$registry" ]]; then docker rm -f "$registry" >/dev/null 2>&1 || true; fi
  for image in "${images[@]}"; do docker image rm "$image" >/dev/null 2>&1 || true; done
  printf 'Suite %s: %ss, exit %s\n' "$suite" "$((SECONDS-started))" "$status"
}
trap cleanup EXIT
# A deadline must be reported as a failure even if the interrupted child was
# between commands and its EXIT cleanup returned success.
trap 'exit 124' TERM
trap 'exit 130' INT
step() {
  local name="$1" before=$SECONDS
  shift
  printf '\nRunning %s\n' "$name"
  "$@"
  printf 'Passed %s in %ss\n' "$name" "$((SECONDS-before))"
}
# One owner prepares immutable fixtures; cases own their mutable resources.
case "$suite" in
  linux|architecture|compose)
    HCORRAL_TEST_RUNTIME_IMAGE="hcorral-runtime-suite:$(date +%s)-$$"
    export HCORRAL_TEST_RUNTIME_IMAGE
    images+=("$HCORRAL_TEST_RUNTIME_IMAGE")
    step runtime-fixture docker build --quiet -t "$HCORRAL_TEST_RUNTIME_IMAGE" -f "$root/tests/fixtures/minimal-image/Dockerfile" "$root"
    ;;
esac
case "$suite" in
  linux|architecture|journey)
    HCORRAL_SESSION_TEST_IMAGE="hcorral-session-suite:$(date +%s)-$$"
    export HCORRAL_SESSION_TEST_IMAGE
    images+=("$HCORRAL_SESSION_TEST_IMAGE")
    step session-fixture docker build --quiet -t "$HCORRAL_SESSION_TEST_IMAGE" -f "$root/tests/fixtures/session-image/Dockerfile" "$root"
    ;;
esac
case "$suite" in
  linux|architecture)
    registry="hcorral-registry-suite-$(date +%s)-$$"
    export HCORRAL_TEST_REGISTRY="$registry"
    step registry docker run -d --name "$registry" --publish 127.0.0.1::5000 registry:2@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373
    ;;
esac
case "$suite" in
  core) step core "$root/scripts/ci-source.sh" ;;
  linux)
    step lifecycle "$root/tests/integration/real-docker.sh"
    step image-refresh "$root/tests/integration/image-refresh.sh"
    step compose-storage "$root/tests/integration/runtime-contracts.sh"
    step session-transport "$root/tests/integration/session-transfer.sh"
    step codex-compatibility "$root/tests/qualification/codex-sessions.sh"
    step public-journeys "$root/tests/integration/session-transfer.sh" journey
    ;;
  architecture)
    step lifecycle "$root/tests/integration/real-docker.sh"
    step native-helper-ownership "$root/tests/integration/session-transfer.sh" architecture
    step public-journeys "$root/tests/integration/session-transfer.sh" journey
    ;;
  compose) step compose-storage "$root/tests/integration/runtime-contracts.sh" ;;
  codex) step codex-compatibility "$root/tests/qualification/codex-sessions.sh" ;;
  journey) step public-journeys "$root/tests/integration/session-transfer.sh" journey ;;
  compat) step compatibility "$root/tests/qualification/compatibility.sh" ;;
esac
