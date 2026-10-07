#!/usr/bin/env bash
# Real Docker/Compose state transitions against an isolated local registry.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
binary="${HCORRAL_TEST_BINARY:-${root}/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
[[ -x "${binary}" ]] || { echo "missing test binary: ${binary}" >&2; exit 2; }
test_tmpdir="${TEST_TMPDIR:-${TMPDIR:-/tmp}}"
mkdir -p "${test_tmpdir}"
test_root="$(mktemp -d "${test_tmpdir%/}/hcorral-refresh.XXXXXX")"
workspace="${test_root}/workspace"
registry="hcorral-refresh-registry-$$"
fixture="hcorral-refresh-fixture:$$"
reference=""
project=""
image_ids=()
mkdir -p "${workspace}"
export HCORRAL_WORKSPACE="${workspace}" HCORRAL_PRIVATE_ENV=true
export HCORRAL_UPDATE_CHECK=false HCORRAL_AUTO_PULL=true HCORRAL_GUI=none
export XDG_CACHE_HOME="${test_root}/cache"

cleanup() {
  if [[ -n "${project}" ]]; then "${binary}" down -v >/dev/null 2>&1 || true; fi
  docker rm --force "${registry}" >/dev/null 2>&1 || true
  if [[ -n "${reference}" ]]; then docker image rm "${reference}" >/dev/null 2>&1 || true; fi
  docker image rm "${fixture}" >/dev/null 2>&1 || true
  for image_id in "${image_ids[@]}"; do docker image rm "${image_id}" >/dev/null 2>&1 || true; done
  rm -r -- "${test_root}"
}
trap cleanup EXIT

docker run -d --name "${registry}" --publish 127.0.0.1::5000 \
  registry:2@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373 >/dev/null
port="$(docker port "${registry}" 5000/tcp | sed -nE 's/^.*:([0-9]+)$/\1/p')"
[[ "${port}" =~ ^[1-9][0-9]*$ ]]
reference="127.0.0.1:${port}/hcorral/refresh:latest"
export HCORRAL_IMAGE="${reference}"
for _ in {1..100}; do
  if curl --fail --silent "http://127.0.0.1:${port}/v2/" >/dev/null; then break; fi
  sleep 0.1
done
curl --fail --silent "http://127.0.0.1:${port}/v2/" >/dev/null

publish_fixture() {
  docker build --quiet --label "hcorral-test-generation=$1" --tag "${fixture}" \
    --file "${root}/tests/fixtures/minimal-image/Dockerfile" "${root}" >/dev/null
  image_ids+=("$(docker image inspect --format '{{.Id}}' "${fixture}")")
  docker image tag "${fixture}" "${reference}"
  docker push "${reference}" >/dev/null
}
attach() { python3 "${root}/tests/integration/attach-probe.py" "${project}" "${binary}" "$@"; }
id() { docker inspect --format '{{.Id}}' "${project}"; }
image_id() { docker inspect --format '{{.Image}}' "${project}"; }
report() { docker exec "${project}" tmux show-options -qv -t hcorral @hcorral-notices; }

publish_fixture 1
project="$("${binary}" info --format=json | python3 -c 'import json,sys; print(json.load(sys.stdin)["project"]["name"])')"
attach
original="$(id)"
first_image="$(image_id)"
docker exec "${project}" sh -c 'printf preserved >"$HCORRAL_CONTAINER_HOME/refresh-marker"'

publish_fixture 2
second_image="$(docker image inspect --format '{{.Id}}' "${reference}")"
# Move the cache back while the registry has generation 2. Running attach must
# not even refresh that alias; inactive launch must retrieve generation 2.
docker image tag "${first_image}" "${reference}"
attach
[[ "$(id)" == "${original}" && "$(image_id)" == "${first_image}" ]]
[[ "$(docker image inspect --format '{{.Id}}' "${reference}")" == "${first_image}" ]]
"${binary}" stop >/dev/null
attach
updated="$(id)"
[[ "${updated}" != "${original}" && "$(image_id)" == "${second_image}" ]]
[[ "$(docker exec "${project}" sh -c 'cat "$HCORRAL_CONTAINER_HOME/refresh-marker"')" == preserved ]]
report | grep -Fq 'applying the refreshed image'

# An unchanged registry image starts the existing stopped container.
"${binary}" stop >/dev/null
attach
[[ "$(id)" == "${updated}" ]]

# Missing original overlay options must never remove a mount or environment.
cat >"${test_root}/overlay.yaml" <<'EOF'
services:
  hcorral:
    environment:
      REFRESH_PRESERVE: original
EOF
"${binary}" -f "${test_root}/overlay.yaml" up -d >/dev/null
configured="$(id)"
publish_fixture 3
third_image="$(docker image inspect --format '{{.Id}}' "${reference}")"
"${binary}" stop >/dev/null
attach
[[ "$(id)" == "${configured}" && "$(image_id)" == "${second_image}" ]]
[[ "$(docker exec "${project}" printenv REFRESH_PRESERVE)" == original ]]
report | grep -Fq 'could not be reproduced'

# Supplying the original configuration permits the guarded update.
"${binary}" stop >/dev/null
attach -f "${test_root}/overlay.yaml"
configured="$(id)"
[[ "$(image_id)" == "${third_image}" ]]

# Offline fallback must ignore even a locally moved alias.
docker stop "${registry}" >/dev/null
docker image tag "${first_image}" "${reference}"
"${binary}" stop >/dev/null
attach -f "${test_root}/overlay.yaml"
[[ "$(id)" == "${configured}" && "$(image_id)" == "${third_image}" ]]
report | grep -Fq 'original image and mounts'

# Opt-out and explicit start preserve the same container without registry access.
"${binary}" stop >/dev/null
HCORRAL_AUTO_PULL=false attach
[[ "$(id)" == "${configured}" ]]
"${binary}" stop >/dev/null
"${binary}" start >/dev/null
[[ "$(id)" == "${configured}" && "$(image_id)" == "${third_image}" ]]
echo 'PASS: real image refresh, configuration guards, retained reports, and offline preservation'
