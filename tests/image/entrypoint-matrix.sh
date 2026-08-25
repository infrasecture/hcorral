#!/usr/bin/env bash
set -euo pipefail

image="${1:-}"
[[ -n "${image}" ]] || { echo 'usage: entrypoint-matrix.sh <local-image>' >&2; exit 2; }
harness="$(docker image inspect --format '{{index .Config.Labels "ai.infrasecture.hcorral.harness.type"}}' "${image}")"
case "${harness}" in codex|claude|pi) ;; *) echo "image has unknown harness label: ${harness}" >&2; exit 1 ;; esac

test_root="$(mktemp -d /tmp/hcorral-entrypoint.XXXXXX)"
volumes=()
cleanup() { for volume in "${volumes[@]}"; do docker volume rm --force "${volume}" >/dev/null 2>&1 || true; done; rm -r -- "${test_root}"; }
trap cleanup EXIT

run_case() {
  local suffix="$1" uid="$2" gid="$3" user="$4" group="$5" groups="$6" expected_user="$7" expected_group="$8"
  local volume="hcorral-entrypoint-${suffix}-$$" workspace="${test_root}/${suffix}/work" home="/home/hcorral-${suffix}" extra_gid=""
  [[ "${suffix}" == mapped ]] && extra_gid=34567
  volumes+=("${volume}"); mkdir -p "${workspace}"; docker volume create "${volume}" >/dev/null
  docker run --rm \
    --env HCORRAL_LAUNCHED_BY_WRAPPER=1 --env "HCORRAL_HARNESS_TYPE=${harness}" \
    --env "HCORRAL_HOST_UID=${uid}" --env "HCORRAL_HOST_GID=${gid}" \
    --env "HCORRAL_HOST_USER=${user}" --env "HCORRAL_HOST_GROUP=${group}" --env "HCORRAL_HOST_GROUPS=${groups}" \
    --env "HCORRAL_CONTAINER_HOME=${home}" --env "HCORRAL_WORKDIR=${workspace}" --env HCORRAL_BYOBU_SESSION=matrix \
    --env "EXPECTED_UID=${uid}" --env "EXPECTED_GID=${gid}" --env "EXPECTED_USER=${expected_user}" --env "EXPECTED_GROUP=${expected_group}" \
    --env "EXPECTED_EXTRA_GID=${extra_gid}" \
    --mount "type=volume,source=${volume},target=${home}" --mount "type=bind,source=${workspace},target=${workspace}" \
    --workdir "${workspace}" "${image}" bash -c '
      set -euo pipefail
      actual_identity="$(id -u):$(id -g):$(id -un):$(id -gn)"
      expected_identity="$EXPECTED_UID:$EXPECTED_GID:$EXPECTED_USER:$EXPECTED_GROUP"
      [[ "$actual_identity" == "$expected_identity" ]] || { echo "identity: expected $expected_identity, got $actual_identity" >&2; exit 1; }
      [[ "$PWD" == "$HCORRAL_WORKDIR" && "$HOME" == "$HCORRAL_CONTAINER_HOME" && ! -e /workspace ]] || { echo "paths: expected PWD=$HCORRAL_WORKDIR HOME=$HCORRAL_CONTAINER_HOME and no /workspace; got PWD=$PWD HOME=$HOME" >&2; exit 1; }
      [[ -s "$HOME/.hcorral/home-bootstrap.env" ]]; tmux has-session -t matrix; sudo -n true
      if [[ -n "$EXPECTED_EXTRA_GID" ]]; then id -G | tr " " "\n" | grep -Fxq "$EXPECTED_EXTRA_GID"; fi
      if [[ "$HCORRAL_HARNESS_TYPE" == codex ]]; then [[ -s /etc/codex/config.toml && ! -e "$HOME/.codex/config.toml" ]]; fi
      if [[ "$HCORRAL_HARNESS_TYPE" == claude ]]; then [[ -s "$HOME/.claude/settings.json" && "$DISABLE_AUTOUPDATER" == 1 ]]; fi
      if [[ "$HCORRAL_HARNESS_TYPE" == pi ]]; then grep -Fq defaultProjectTrust "$HOME/.pi/agent/settings.json"; grep -Fq always "$HOME/.pi/agent/settings.json"; fi
      mkdir -p "$HOME/.local/bin"
      printf "#!/bin/sh\nprintf user-prefix\n" >"$HOME/.local/bin/$HCORRAL_HARNESS_TYPE"
      chmod 0755 "$HOME/.local/bin/$HCORRAL_HARNESS_TYPE"
      [[ "$(command -v "$HCORRAL_HARNESS_TYPE")" == "$HOME/.local/bin/$HCORRAL_HARNESS_TYPE" ]]
      [[ "$("$HCORRAL_HARNESS_TYPE" --version)" == user-prefix ]]
      [[ "$0" == argv-zero && "$1" == "space arg" && "$2" == $'"'"'line\nbreak'"'"' ]]
    ' argv-zero "space arg" $'line\nbreak'
}

run_case root 0 0 root hostroot 0:hostroot root root
run_case mapped 12345 23456 alice team 23456:team,34567:extras alice team
run_case uid_collision 1000 1000 hostuser hostgroup 1000:hostgroup hostuser hostgroup
run_case name_collision 12346 23457 vscode another 23457:another hcorral-12346 another

# Existing harness configuration in a non-empty persisted home is authoritative.
preserve_volume="hcorral-entrypoint-preserve-$$"
preserve_workspace="${test_root}/preserve/work"
preserve_home=/home/hcorral-preserve
case "${harness}" in
  codex) preserve_config=.codex/config.toml ;;
  claude) preserve_config=.claude/settings.json ;;
  pi) preserve_config=.pi/agent/settings.json ;;
esac
volumes+=("${preserve_volume}"); mkdir -p "${preserve_workspace}"; docker volume create "${preserve_volume}" >/dev/null
docker run --rm --entrypoint sh --mount "type=volume,source=${preserve_volume},target=${preserve_home}" "${image}" -c \
  'mkdir -p "$1/$(dirname "$2")"; printf "custom-config\n" >"$1/$2"; printf "keep\n" >"$1/preexisting"; chown -R 12347:23458 "$1"' sh "${preserve_home}" "${preserve_config}"
docker run --rm \
  --env HCORRAL_LAUNCHED_BY_WRAPPER=1 --env "HCORRAL_HARNESS_TYPE=${harness}" \
  --env HCORRAL_HOST_UID=12347 --env HCORRAL_HOST_GID=23458 \
  --env HCORRAL_HOST_USER=preserve --env HCORRAL_HOST_GROUP=preserve --env HCORRAL_HOST_GROUPS=23458:preserve \
  --env "HCORRAL_CONTAINER_HOME=${preserve_home}" --env "HCORRAL_WORKDIR=${preserve_workspace}" --env "EXPECTED_CONFIG_REL=${preserve_config}" \
  --mount "type=volume,source=${preserve_volume},target=${preserve_home}" \
  --mount "type=bind,source=${preserve_workspace},target=${preserve_workspace}" \
  --workdir "${preserve_workspace}" "${image}" bash -c '
    [[ "$(cat "$HOME/$EXPECTED_CONFIG_REL")" == custom-config ]]
    [[ "$(cat "$HOME/preexisting")" == keep ]]
    [[ ! -e "$HOME/.hcorral/home-bootstrap.env" ]]
    if [[ "$HCORRAL_HARNESS_TYPE" == codex ]]; then [[ -s /etc/codex/config.toml ]]; fi
  '

invalid="${test_root}/invalid.out"
if docker run --rm --env 'HCORRAL_BYOBU_SESSION=bad:window' "${image}" >"${invalid}" 2>&1; then echo 'invalid session accepted' >&2; exit 1; fi
grep -Fq 'HCORRAL_BYOBU_SESSION must match' "${invalid}"
echo "PASS: ${harness} image entrypoint account, home, session, configuration, and argv matrix"
