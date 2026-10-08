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
fixture_image="${HCORRAL_TEST_RUNTIME_IMAGE:-hcorral-integration-fixture:$(date +%s)-$$}"
registry_container="${HCORRAL_TEST_REGISTRY:-hcorral-test-registry-$$}"
image=""

export XDG_CACHE_HOME="${test_root}/cache"
export HCORRAL_WORKSPACE="${workspace}"
export HCORRAL_PRIVATE_ENV=true
export HCORRAL_UPDATE_CHECK=false
export HCORRAL_GUI=none

project=""
cleanup() {
  if [[ -n "${project}" ]]; then
    "${binary}" down -v >/dev/null 2>&1 || true
  fi
  if [[ -n "${image}" ]]; then docker image rm "${image}" >/dev/null 2>&1 || true; fi
  if [[ -z "${HCORRAL_TEST_RUNTIME_IMAGE:-}" ]]; then docker image rm "${fixture_image}" >/dev/null 2>&1 || true; fi
  if [[ -z "${HCORRAL_TEST_REGISTRY:-}" ]]; then docker rm --force "${registry_container}" >/dev/null 2>&1 || true; fi
  rm -r -- "${test_root}"
}
trap cleanup EXIT

if [[ -z "${HCORRAL_TEST_RUNTIME_IMAGE:-}" ]]; then
  docker build --quiet --tag "${fixture_image}" --file "${repo_root}/tests/fixtures/minimal-image/Dockerfile" "${repo_root}" >/dev/null
fi
if [[ -z "${HCORRAL_TEST_REGISTRY:-}" ]]; then
  docker run --detach --name "${registry_container}" --publish 127.0.0.1::5000 \
    registry:2@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373 >/dev/null
fi
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

# Inspect what the packaged launcher actually sent through Compose. Account
# database memberships are not a substitute for the invoking process's groups,
# especially in static Linux builds and macOS directory-service environments.
docker inspect --format '{{json .Config.Env}}' "$project" >"$test_root/identity.json"
python3 - "$test_root/identity.json" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    environment = dict(entry.split("=", 1) for entry in json.load(stream))
assert int(environment["HCORRAL_HOST_UID"]) == os.geteuid()
assert int(environment["HCORRAL_HOST_GID"]) == os.getegid()
actual = {int(spec.split(":", 1)[0]) for spec in environment["HCORRAL_HOST_GROUPS"].split(",")}
groups = os.getgroups()
if sys.platform == "darwin":
    # Modern Python binds getgroups$DARWIN_EXTSN, which returns account-access
    # groups instead of the process credentials used by Go's os.Getgroups.
    # Call the POSIX symbol directly to compare the actual process group list.
    # https://docs.python.org/3/library/os.html#os.getgroups
    import ctypes
    getgroups = ctypes.CDLL(None, use_errno=True).getgroups
    getgroups.argtypes = [ctypes.c_int, ctypes.POINTER(ctypes.c_uint32)]
    getgroups.restype = ctypes.c_int
    count = getgroups(0, None)
    if count < 0:
        raise OSError(ctypes.get_errno(), "getgroups size")
    buffer = (ctypes.c_uint32 * count)()
    count = getgroups(count, buffer)
    if count < 0:
        raise OSError(ctypes.get_errno(), "getgroups values")
    groups = list(buffer[:count])
expected = set(groups) | {os.getegid()}
assert actual == expected, (actual, expected)
print("PASS: packaged launcher preserves host process UID/GID and supplementary groups")
PY

# Initial creation had to pull the absent selected image. An explicit pull
# fetches it again without recreating or restarting the running container.
docker image inspect "${image}" >/dev/null
"${binary}" pull >/dev/null
[[ "$(docker inspect --format '{{.Id}}' "${project}")" == "${container_id}" ]]
[[ "$(docker inspect --format '{{.State.StartedAt}}' "${project}")" == "${started_at}" ]]

"${binary}" exec true
"${binary}" stop

# A pinned stopped project keeps its original container even if desired
# options differ. This non-PTY invocation starts successfully, then Docker
# refuses interactive attachment; assert the lifecycle result separately.
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
[[ "$(docker inspect --format '{{.Id}}' "${project}")" == "${container_id}" ]]
[[ "$(docker inspect --format '{{.State.Running}}' "${project}")" == true ]]
if docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${project}" | grep -q '^TEST_DRIFT='; then
  echo 'bare startup applied configuration drift to a pinned container' >&2
  exit 1
fi
"${binary}" -f "${drift_overlay}" up -d
[[ "$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${project}" | grep '^TEST_DRIFT=')" == TEST_DRIFT=reconciled ]]
container_id="$(docker inspect --format '{{.Id}}' "${project}")"
"${binary}" stop
"${binary}" start
[[ "$(docker inspect --format '{{.Id}}' "${project}")" == "${container_id}" ]]
started_at="$(docker inspect --format '{{.State.StartedAt}}' "${project}")"

docker exec "${project}" tmux kill-server >/dev/null 2>&1 || true
attach_log="${test_root}/attach.log"
timeout_binary=timeout
if [[ "$(uname -s)" == Darwin ]]; then
  timeout_binary=gtimeout
fi
set +e
"${timeout_binary}" 35 python3 -c \
  'import os, pty, sys; raise SystemExit(os.waitstatus_to_exitcode(pty.spawn([sys.argv[1]])))' \
  "${binary}" >"${attach_log}" 2>&1 &
attach_pid=$!
set -e

# A remote Docker daemon can make each recovery probe take several seconds.
# Observe the actual PTY client instead of killing the launcher after an
# arbitrary delay. The outer timeout remains a guard for a broken recovery.
attach_ready=false
for _ in {1..120}; do
  if docker exec "${project}" tmux list-clients -t hcorral >/dev/null 2>&1; then
    attach_ready=true
    break
  fi
  if ! kill -0 "${attach_pid}" 2>/dev/null; then
    break
  fi
  sleep 0.25
done
if [[ "${attach_ready}" == true ]]; then
  docker exec "${project}" tmux kill-session -t hcorral >/dev/null 2>&1 || true
fi
set +e
wait "${attach_pid}"
attach_status=$?
set -e
if [[ "${attach_ready}" != true ]]; then
  cat "${attach_log}" >&2
  echo "attach recovery did not attach a PTY client (status ${attach_status})" >&2
  exit 1
fi
[[ "$(docker inspect --format '{{.Id}}' "${project}")" == "${container_id}" ]]
[[ "$(docker inspect --format '{{.State.StartedAt}}' "${project}")" == "${started_at}" ]]

"${binary}" down -v
project=""
if docker volume inspect "${private_volume}" >/dev/null 2>&1; then
  echo "private volume was not removed: ${private_volume}" >&2
  exit 1
fi

echo 'PASS: real Docker lifecycle, identity, recovery, and private-state removal'
