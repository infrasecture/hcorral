#!/usr/bin/env bash
set -euo pipefail

image="${1:-}"
[[ -n "${image}" ]] || { echo 'usage: entrypoint-matrix.sh <local-image>' >&2; exit 2; }

test_root="$(mktemp -d /tmp/hcorral-entrypoint.XXXXXX)"
volumes=()
cleanup() {
  local volume
  for volume in "${volumes[@]}"; do docker volume rm --force "${volume}" >/dev/null 2>&1 || true; done
  rm -r -- "${test_root}"
}
trap cleanup EXIT

run_case() {
  local suffix="$1" uid="$2" gid="$3" user="$4" group="$5" groups="$6" expected_user="$7" expected_group="$8"
  local volume="hcorral-entrypoint-${suffix}-$$" workspace="${test_root}/${suffix}/workspace" home="/home/hcorral-${suffix}"
  volumes+=("${volume}")
  mkdir -p "${workspace}"
  docker volume create "${volume}" >/dev/null
  docker run --rm \
    --env HCORRAL_LAUNCHED_BY_WRAPPER=1 \
    --env "HCORRAL_HOST_UID=${uid}" --env "HCORRAL_HOST_GID=${gid}" \
    --env "HCORRAL_HOST_USER=${user}" --env "HCORRAL_HOST_GROUP=${group}" --env "HCORRAL_HOST_GROUPS=${groups}" \
    --env "HCORRAL_CONTAINER_HOME=${home}" --env "HCORRAL_WORKDIR=${workspace}" --env HCORRAL_BYOBU_SESSION=matrix \
    --mount "type=volume,source=${volume},target=${home}" \
    --mount "type=bind,source=${workspace},target=${workspace}" \
    --workdir "${workspace}" \
    "${image}" bash -c '
      set -euo pipefail
      [[ "$(id -u)" == "$1" && "$(id -g)" == "$2" && "$(id -un)" == "$3" && "$(id -gn)" == "$4" ]]
      [[ "$PWD" == "$5" && "$HOME" == "$6" && "$CODEX_HOME" == "$6/.codex" ]]
      [[ -s "$CODEX_HOME/config.toml" && -s "$HOME/.claude/settings.json" && -s "$HOME/.hcorral/home-bootstrap.env" ]]
      tmux has-session -t matrix
      sudo -n true
      if [[ "$1" == 12345 ]]; then id -G | tr " " "\n" | grep -Fxq 34567; fi
      [[ "$(readlink /workspace)" == "$5" ]]
      [[ "$7" == "space arg" && "$8" == $'"'"'line\nbreak'"'"' ]]
    ' bash "${uid}" "${gid}" "${expected_user}" "${expected_group}" "${workspace}" "${home}" "space arg" $'line\nbreak'
}

run_case root 0 0 root hostroot 0:hostroot root root
run_case mapped 12345 23456 alice team 23456:team,34567:extras alice team
run_case uid_collision 1000 1000 hostuser hostgroup 1000:hostgroup hostuser hostgroup
run_case name_collision 12346 23457 vscode another 23457:another hcorral-12346 another

invalid_session_output="${test_root}/invalid-session.out"
if docker run --rm --env 'HCORRAL_BYOBU_SESSION=bad:window' "${image}" >"${invalid_session_output}" 2>&1; then
  echo 'entrypoint accepted a tmux target separator in the session name' >&2
  exit 1
fi
grep -Fq 'HCORRAL_BYOBU_SESSION must match [A-Za-z0-9_.-]{1,64}' "${invalid_session_output}"

# Existing user configuration is authoritative and is not replaced or extended.
preserve_volume="hcorral-entrypoint-preserve-$$"
volumes+=("${preserve_volume}")
preserve_workspace="${test_root}/preserve/workspace"
preserve_home=/home/hcorral-preserve
mkdir -p "${preserve_workspace}"
docker volume create "${preserve_volume}" >/dev/null
docker run --rm --entrypoint sh --mount "type=volume,source=${preserve_volume},target=${preserve_home}" "${image}" -c \
  'mkdir -p "$1/.codex"; printf "custom = true\n" >"$1/.codex/config.toml"; printf keep >"$1/preexisting"' sh "${preserve_home}"
docker run --rm \
  --env HCORRAL_LAUNCHED_BY_WRAPPER=1 \
  --env HCORRAL_HOST_UID=12347 --env HCORRAL_HOST_GID=23458 \
  --env HCORRAL_HOST_USER=preserve --env HCORRAL_HOST_GROUP=preserve --env HCORRAL_HOST_GROUPS=23458:preserve \
  --env "HCORRAL_CONTAINER_HOME=${preserve_home}" --env "HCORRAL_WORKDIR=${preserve_workspace}" \
  --mount "type=volume,source=${preserve_volume},target=${preserve_home}" \
  --mount "type=bind,source=${preserve_workspace},target=${preserve_workspace}" \
  "${image}" bash -c 'grep -Fxq "custom = true" "$HOME/.codex/config.toml" && [[ "$(cat "$HOME/preexisting")" == keep ]]'

echo "PASS: image entrypoint account, home, session, and argv matrix for ${image}"
