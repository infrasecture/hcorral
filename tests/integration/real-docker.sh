#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
binary="${HCORRAL_TEST_BINARY:-${repo_root}/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
[[ -x "${binary}" ]] || { echo "missing test binary: ${binary}" >&2; exit 2; }

test_root="$(mktemp -d /tmp/hcorral-integration.XXXXXX)"
workspace="${test_root}/Client Portal"
mkdir -p "${workspace}" "${test_root}/cache"
image="hcorral-integration:$(date +%s)-$$"

export XDG_CACHE_HOME="${test_root}/cache"
export HCORRAL_WORKSPACE="${workspace}"
export HCORRAL_IMAGE_NAME="${image%:*}"
export HCORRAL_IMAGE_TAG="${image##*:}"
export HCORRAL_PRIVATE_ENV=true
export HCORRAL_UPDATE_CHECK=false

project=""
cleanup() {
  if [[ -n "${project}" ]]; then
    "${binary}" down -v >/dev/null 2>&1 || true
  fi
  docker image rm "${image}" >/dev/null 2>&1 || true
  rm -r -- "${test_root}"
}
trap cleanup EXIT

docker build --quiet --tag "${image}" --file "${repo_root}/tests/fixtures/minimal-image/Dockerfile" "${repo_root}" >/dev/null

info="$(${binary} info --format=json)"
project="$(printf '%s' "${info}" | sed -n 's/^[[:space:]]*"project": "\([^"]*\)",*$/\1/p' | head -1)"
[[ "${project}" =~ ^hcorral-client_portal-[0-9a-f]{7}$ ]] || { echo "unexpected project: ${project}" >&2; exit 1; }

"${binary}" up -d
container_id="$(docker inspect --format '{{.Id}}' "${project}")"
started_at="$(docker inspect --format '{{.State.StartedAt}}' "${project}")"
[[ "$(docker inspect --format '{{index .Config.Labels "ai.infrasecture.hcorral.workspace-id-scheme"}}' "${project}")" == v1 ]]
[[ "$(docker inspect --format '{{.State.Running}}' "${project}")" == true ]]

"${binary}" exec true
"${binary}" stop
"${binary}" start
[[ "$(docker inspect --format '{{.Id}}' "${project}")" == "${container_id}" ]]
started_at="$(docker inspect --format '{{.State.StartedAt}}' "${project}")"

docker exec "${project}" tmux kill-server
set +e
timeout 3 script -qec "${binary}" /dev/null >/dev/null 2>&1
attach_status=$?
set -e
[[ ${attach_status} -eq 0 || ${attach_status} -eq 124 ]]
docker exec "${project}" tmux has-session -t hcorral
[[ "$(docker inspect --format '{{.Id}}' "${project}")" == "${container_id}" ]]
[[ "$(docker inspect --format '{{.State.StartedAt}}' "${project}")" == "${started_at}" ]]

private_volume="${project}"
"${binary}" down -v
project=""
if docker volume inspect "${private_volume}" >/dev/null 2>&1; then
  echo "private volume was not removed: ${private_volume}" >&2
  exit 1
fi

echo 'PASS: real Docker lifecycle, identity, recovery, and private-state removal'
