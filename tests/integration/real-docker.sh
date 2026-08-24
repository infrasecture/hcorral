#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
binary="${HCORRAL_TEST_BINARY:-${repo_root}/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
[[ -x "${binary}" ]] || { echo "missing test binary: ${binary}" >&2; exit 2; }

test_tmpdir="${TEST_TMPDIR:-${TMPDIR:-/tmp}}"
mkdir -p "${test_tmpdir}"
test_root="$(mktemp -d "${test_tmpdir%/}/hcorral-integration.XXXXXX")"
workspace="${test_root}/Client Portal"
mkdir -p "${workspace}" "${test_root}/cache"
fixture_image="hcorral-integration-fixture:$(date +%s)-$$"
registry_container="hcorral-test-registry-$$"
image=""

export XDG_CACHE_HOME="${test_root}/cache"
export HCORRAL_WORKSPACE="${workspace}"
export HCORRAL_PRIVATE_ENV=true
export HCORRAL_UPDATE_CHECK=false

project=""
cleanup() {
  if [[ -n "${project}" ]]; then
    "${binary}" down -v >/dev/null 2>&1 || true
  fi
  if [[ -n "${image}" ]]; then docker image rm "${image}" >/dev/null 2>&1 || true; fi
  docker image rm "${fixture_image}" >/dev/null 2>&1 || true
  docker rm --force "${registry_container}" >/dev/null 2>&1 || true
  rm -r -- "${test_root}"
}
trap cleanup EXIT

docker build --quiet --tag "${fixture_image}" --file "${repo_root}/tests/fixtures/minimal-image/Dockerfile" "${repo_root}" >/dev/null
docker run --detach --name "${registry_container}" --publish 127.0.0.1::5000 \
  registry:2@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373 >/dev/null
registry_port="$(docker port "${registry_container}" 5000/tcp | sed -nE 's/^.*:([0-9]+)$/\1/p')"
[[ "${registry_port}" =~ ^[1-9][0-9]*$ ]] || { echo 'could not resolve local registry port' >&2; exit 1; }
registry_ready=false
for _ in {1..30}; do
  if curl --fail --silent --show-error "http://127.0.0.1:${registry_port}/v2/" >/dev/null 2>&1; then
    registry_ready=true
    break
  fi
  sleep 0.1
done
[[ "${registry_ready}" == true ]] || { echo 'local test registry did not become ready' >&2; exit 1; }
image="127.0.0.1:${registry_port}/hcorral/integration:$(date +%s)-$$"
export HCORRAL_IMAGE="${image}"
docker image tag "${fixture_image}" "${image}"
docker image push "${image}" >/dev/null
docker image rm "${image}" >/dev/null

info="$(${binary} info --format=json)"
project="$(printf '%s' "${info}" | sed -n '/^[[:space:]]*"project": {/,/^[[:space:]]*}/ s/^[[:space:]]*"name": "\([^"]*\)",*$/\1/p' | head -1)"
private_volume="$(printf '%s' "${info}" | sed -n '/^[[:space:]]*"state": {/,/^[[:space:]]*}/ s/^[[:space:]]*"volume": "\([^"]*\)",*$/\1/p' | head -1)"
[[ "${project}" =~ ^hcorral-client_portal-[0-9a-f]{7}$ ]] || { echo "unexpected project: ${project}" >&2; exit 1; }
[[ "${private_volume}" =~ ^hcorral-client_portal-[0-9a-f]{7}$ ]] || { echo "unexpected private volume: ${private_volume}" >&2; exit 1; }
grep -Fq '"schema": 1' <<<"${info}"
grep -Fq '"ownership"' <<<"${info}"
grep -Fq '"state"' <<<"${info}"
grep -Fq '"compose"' <<<"${info}"
grep -Fq '"session"' <<<"${info}"
grep -Fq '"update"' <<<"${info}"

"${binary}" up -d
container_id="$(docker inspect --format '{{.Id}}' "${project}")"
started_at="$(docker inspect --format '{{.State.StartedAt}}' "${project}")"
[[ "$(docker inspect --format '{{index .Config.Labels "ai.infrasecture.hcorral.workspace-id-scheme"}}' "${project}")" == v1 ]]
[[ "$(docker inspect --format '{{.State.Running}}' "${project}")" == true ]]

# Initial creation had to pull the absent selected image. An explicit pull
# fetches it again without recreating or restarting the running container.
docker image inspect "${image}" >/dev/null
"${binary}" pull >/dev/null
[[ "$(docker inspect --format '{{.Id}}' "${project}")" == "${container_id}" ]]
[[ "$(docker inspect --format '{{.State.StartedAt}}' "${project}")" == "${started_at}" ]]

"${binary}" exec true
"${binary}" stop

# Bare stopped launch refuses desired/deployed drift; explicit up reconciles it.
drift_overlay="${test_root}/drift.yaml"
cat >"${drift_overlay}" <<'EOF'
services:
  hcorral:
    environment:
      TEST_DRIFT: reconciled
EOF
set +e
"${binary}" -f "${drift_overlay}" >/dev/null 2>"${test_root}/drift.err"
drift_status=$?
set -e
[[ ${drift_status} -eq 1 ]]
grep -Fq 'stopped environment has present drift' "${test_root}/drift.err"
"${binary}" -f "${drift_overlay}" up -d
[[ "$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${project}" | grep '^TEST_DRIFT=')" == TEST_DRIFT=reconciled ]]
container_id="$(docker inspect --format '{{.Id}}' "${project}")"
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

"${binary}" down -v
project=""
if docker volume inspect "${private_volume}" >/dev/null 2>&1; then
  echo "private volume was not removed: ${private_volume}" >&2
  exit 1
fi

echo 'PASS: real Docker lifecycle, identity, recovery, and private-state removal'
