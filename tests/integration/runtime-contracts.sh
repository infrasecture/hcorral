#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
binary="${HCORRAL_TEST_BINARY:-${repo_root}/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
[[ -x "${binary}" ]] || { echo "missing test binary: ${binary}" >&2; exit 2; }

test_root="$(mktemp -d /tmp/hcorral-contracts.XXXXXX)"
cache="${test_root}/cache"
workspace="${test_root}/workspace"
image="hcorral-contracts:$(date +%s)-$$"
legacy_name="hcorral-test-legacy-$$"
ambiguous_name="hcorral-test-ambiguous-$$-codex"
foreign_name="hcorral-test-foreign-codex-$$"
shared_ref_name="hcorral-test-shared-ref-$$"
ambiguous_overlay_volume="hcorral-test-overlay-volume-$$"
foreign_network=""
external_network="hcorral-test-external-network-$$"
collision_name=""
custom_volume="hcorral-test-custom-$$"
private_volume=""
shared_created=false
mkdir -p "${cache}" "${workspace}"

cleanup() {
  for name in "${legacy_name}" "${ambiguous_name}" "${foreign_name}" "${shared_ref_name}" "${collision_name}"; do
    if [[ -n "${name}" ]]; then
      docker rm --force "${name}" >/dev/null 2>&1 || true
    fi
  done
  for volume in "${custom_volume}" "${private_volume}" "${ambiguous_overlay_volume}"; do
    if [[ -n "${volume}" ]]; then
      docker volume rm "${volume}" >/dev/null 2>&1 || true
    fi
  done
  if [[ -n "${foreign_network}" ]]; then
    docker network rm "${foreign_network}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${external_network}" ]]; then
    docker network rm "${external_network}" >/dev/null 2>&1 || true
  fi
  if [[ "${shared_created}" == true ]]; then
    docker volume rm hcorral_state >/dev/null 2>&1 || true
  fi
  docker image rm "${image}" >/dev/null 2>&1 || true
  rm -r -- "${test_root}"
}
trap cleanup EXIT

docker build --quiet --tag "${image}" --file "${repo_root}/tests/fixtures/minimal-image/Dockerfile" "${repo_root}" >/dev/null

run_hcorral() {
  XDG_CACHE_HOME="${cache}" \
  HCORRAL_WORKSPACE="${HCORRAL_WORKSPACE:-${workspace}}" \
  HCORRAL_IMAGE_NAME="${image%:*}" \
  HCORRAL_IMAGE_TAG="${image##*:}" \
  HCORRAL_UPDATE_CHECK=false \
    "${binary}" "$@"
}

project_from_info() {
  run_hcorral info --format=json \
    | sed -n '/^[[:space:]]*"project": {/,/^[[:space:]]*}/ s/^[[:space:]]*"name": "\([^"]*\)",*$/\1/p' \
    | head -1
}

project_for() {
  HCORRAL_WORKSPACE="$1" project_from_info
}

expect_status() {
  local want="$1"
  shift
  set +e
  "$@"
  local got=$?
  set -e
  [[ "${got}" -eq "${want}" ]] || { echo "status ${got}, want ${want}: $*" >&2; exit 1; }
}

# Physical-path identity is stable through symlinks, while equal basenames at
# distinct paths retain independent full IDs and short suffixes.
project="$(project_from_info)"
ln -s "${workspace}" "${test_root}/workspace-link"
link_project="$(project_for "${test_root}/workspace-link")"
[[ "${link_project}" == "${project}" ]]
mkdir -p "${test_root}/a/demo" "${test_root}/b/demo"
project_a="$(project_for "${test_root}/a/demo")"
project_b="$(project_for "${test_root}/b/demo")"
[[ "${project_a}" =~ ^hcorral-demo-[0-9a-f]{7}$ && "${project_b}" =~ ^hcorral-demo-[0-9a-f]{7}$ && "${project_a}" != "${project_b}" ]]

# Proven myCodex state blocks both running and stopped; partial name evidence
# on the exact bind blocks as ambiguous. No hcorral object may be created.
docker run --detach --name "${legacy_name}" \
  --label io.infrasecture.mycodex.gui=none \
  --mount "type=bind,source=${workspace},target=${workspace}" \
  --entrypoint sleep "${image}" infinity >/dev/null
legacy_output="${test_root}/legacy.out"
expect_status 3 run_hcorral ps >"${legacy_output}" 2>&1
grep -Fq 'use the original myCodex launcher to attach' "${legacy_output}"
if docker container inspect "${project}" >/dev/null 2>&1; then
  echo "legacy refusal created hcorral container ${project}" >&2
  exit 1
fi
docker stop "${legacy_name}" >/dev/null
expect_status 3 run_hcorral ps >"${legacy_output}" 2>&1
docker rm "${legacy_name}" >/dev/null

docker run --detach --name "${ambiguous_name}" \
  --mount "type=bind,source=${workspace},target=${workspace}" \
  --entrypoint sleep "${image}" infinity >/dev/null
expect_status 3 run_hcorral ps >"${legacy_output}" 2>&1
docker rm --force "${ambiguous_name}" >/dev/null

# A marked foreign workspace and volume-only residue are not claimed.
foreign_workspace="${test_root}/foreign/workspace"
mkdir -p "${foreign_workspace}"
docker run --detach --name "${foreign_name}" \
  --label io.infrasecture.mycodex.gui=none \
  --mount "type=bind,source=${foreign_workspace},target=${foreign_workspace}" \
  --entrypoint sleep "${image}" infinity >/dev/null
run_hcorral ps >/dev/null
docker rm --force "${foreign_name}" >/dev/null
docker volume create "${custom_volume}" >/dev/null
run_hcorral ps >/dev/null

# A same-name foreign container is reported by info but blocks operations and
# is never executed or changed.
collision_name="${project}"
docker run --detach --name "${collision_name}" \
  --label ai.infrasecture.hcorral.workspace-id="$(printf 'f%.0s' {1..64})" \
  --label ai.infrasecture.hcorral.workspace-id-scheme=v1 \
  --label ai.infrasecture.hcorral.runtime-schema=1 \
  --entrypoint sleep "${image}" infinity >/dev/null
collision_info="$(run_hcorral info --format=json)"
grep -Fq '"status": "collision"' <<<"${collision_info}"
expect_status 1 run_hcorral ps >/dev/null 2>&1
[[ "$(docker inspect --format '{{.State.Running}}' "${collision_name}")" == true ]]
docker rm --force "${collision_name}" >/dev/null
collision_name=""

# A managed-field overlay is rejected before either the private state volume or
# its selected container can be created.
attack_overlay="${test_root}/managed-field-attack.yaml"
cat >"${attack_overlay}" <<'EOF'
services:
  hcorral:
    environment:
      HCORRAL_HOST_UID: "0"
EOF
attack_project="$(HCORRAL_PRIVATE_ENV=true project_from_info)"
export HCORRAL_PRIVATE_ENV=true
expect_status 2 run_hcorral -f "${attack_overlay}" >/dev/null 2>&1
unset HCORRAL_PRIVATE_ENV
if docker volume inspect "${attack_project}" >/dev/null 2>&1 || docker container inspect "${attack_project}" >/dev/null 2>&1; then
  echo 'managed-field refusal mutated Docker state' >&2
  exit 1
fi

# A colliding non-external Compose network is rejected before a private state
# volume, image pull, or container can be created.
foreign_network="${project}_default"
docker network create "${foreign_network}" >/dev/null
export HCORRAL_PRIVATE_ENV=true
expect_status 1 run_hcorral up -d >/dev/null 2>&1
unset HCORRAL_PRIVATE_ENV
if docker volume inspect "${project}" >/dev/null 2>&1 || docker container inspect "${project}" >/dev/null 2>&1; then
  echo 'network ownership refusal mutated Docker state' >&2
  exit 1
fi
docker network rm "${foreign_network}" >/dev/null
foreign_network=""

# A colliding private-state name is refused by the read-only state preflight
# before Compose can create a network or container. The foreign volume is not
# relabelled or removed.
private_volume="${project}"
docker volume create "${private_volume}" >/dev/null
export HCORRAL_PRIVATE_ENV=true
expect_status 1 run_hcorral up -d >/dev/null 2>&1
unset HCORRAL_PRIVATE_ENV
if docker container inspect "${project}" >/dev/null 2>&1 || docker network inspect "${project}_default" >/dev/null 2>&1; then
  echo 'state ownership refusal mutated Docker state' >&2
  exit 1
fi
[[ -z "$(docker volume inspect --format '{{index .Labels "ai.infrasecture.hcorral.workspace-id"}}' "${private_volume}")" ]]
docker volume rm "${private_volume}" >/dev/null
private_volume=""

# Custom state is explicitly user-managed and survives down -v.
HCORRAL_STATE_VOLUME_NAME="${custom_volume}" run_hcorral up -d >/dev/null
HCORRAL_STATE_VOLUME_NAME="${custom_volume}" run_hcorral down -v >/dev/null
docker volume inspect "${custom_volume}" >/dev/null

# Private state and an extra read-only bind are owned and removed exactly.
extra_source="${test_root}/extra source"
mkdir -p "${extra_source}"
HCORRAL_PRIVATE_ENV=true run_hcorral --volume "${extra_source}:/mnt/hcorral-extra:ro" --volume "${custom_volume}:/mnt/hcorral-named-extra:ro" up -d >/dev/null
private_project="$(HCORRAL_PRIVATE_ENV=true project_from_info)"
private_volume="${private_project}"
mount_record="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/mnt/hcorral-extra"}}{{.Source}}|{{.RW}}{{end}}{{end}}' "${private_project}")"
[[ "${mount_record}" == "${extra_source}|false" ]]
named_mount_record="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/mnt/hcorral-named-extra"}}{{.Name}}|{{.RW}}{{end}}{{end}}' "${private_project}")"
[[ "${named_mount_record}" == "${custom_volume}|false" ]]
private_down_output="${test_root}/private-down.out"
HCORRAL_PRIVATE_ENV=true run_hcorral --volume "${extra_source}:/mnt/hcorral-extra:ro" --volume "${custom_volume}:/mnt/hcorral-named-extra:ro" down -v >"${private_down_output}" 2>&1
grep -Fq "retain external volume ${custom_volume}" "${private_down_output}"
if docker volume inspect "${private_volume}" >/dev/null 2>&1; then
  echo "private volume survived down -v: ${private_volume}" >&2
  exit 1
fi
private_volume=""
docker volume inspect "${custom_volume}" >/dev/null

# A generic Compose-compatible executable path containing whitespace receives
# exact argv and can add a sidecar without replacing the managed base.
wrapper_dir="${test_root}/wrapper dir"
wrapper="${wrapper_dir}/policy compose"
wrapper_log="${test_root}/wrapper.args"
overlay="${test_root}/sidecar.yaml"
mkdir -p "${wrapper_dir}"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>%q\nexec docker compose "$@"\n' "${wrapper_log}" >"${wrapper}"
chmod 0755 "${wrapper}"
cat >"${overlay}" <<'EOF'
services:
  sidecar:
    image: ${HCORRAL_IMAGE_NAME}:${HCORRAL_IMAGE_TAG}
    entrypoint: ["/bin/sh", "-c", "exec sleep infinity"]
    networks: [external_test]
networks:
  external_test:
    external: true
    name: ${HCORRAL_TEST_EXTERNAL_NETWORK}
EOF
compose_json="$(printf '["%s"]' "${wrapper}")"
docker network create "${external_network}" >/dev/null
export HCORRAL_TEST_EXTERNAL_NETWORK="${external_network}"
HCORRAL_PRIVATE_ENV=true HCORRAL_COMPOSE_COMMAND="${compose_json}" run_hcorral -f "${overlay}" up -d >/dev/null
sidecar_project="$(HCORRAL_PRIVATE_ENV=true HCORRAL_COMPOSE_COMMAND="${compose_json}" run_hcorral -f "${overlay}" info --format=json \
  | sed -n '/^[[:space:]]*"project": {/,/^[[:space:]]*}/ s/^[[:space:]]*"name": "\([^"]*\)",*$/\1/p' | head -1)"
private_volume="${sidecar_project}"
[[ "$(docker ps --filter "label=com.docker.compose.project=${sidecar_project}" --format '{{.Names}}' | wc -l)" -eq 2 ]]
grep -Fq -- "-p ${sidecar_project} --project-directory ${workspace}" "${wrapper_log}"
sidecar_down_output="${test_root}/sidecar-down.out"
HCORRAL_PRIVATE_ENV=true HCORRAL_COMPOSE_COMMAND="${compose_json}" run_hcorral -f "${overlay}" down -v >"${sidecar_down_output}" 2>&1
grep -Fq "retain external network ${external_network}" "${sidecar_down_output}"
docker network inspect "${external_network}" >/dev/null
unset HCORRAL_TEST_EXTERNAL_NETWORK
docker network rm "${external_network}" >/dev/null
external_network=""
private_volume=""

# Every mutating Compose command refuses a rendered non-external overlay volume
# whose existing Docker object lacks this exact Compose project's labels. The
# initial up refusal occurs before private state, network, or container creation;
# down -v later proves that the same preflight occurs before stopping anything.
ambiguous_overlay="${test_root}/ambiguous-volume.yaml"
cat >"${ambiguous_overlay}" <<'EOF'
services:
  sidecar:
    image: ${HCORRAL_IMAGE_NAME}:${HCORRAL_IMAGE_TAG}
    entrypoint: ["/bin/sh", "-c", "exec sleep infinity"]
    volumes:
      - scratch:/scratch
volumes:
  scratch:
    name: ${HCORRAL_TEST_AMBIGUOUS_VOLUME}
EOF
docker volume create "${ambiguous_overlay_volume}" >/dev/null
export HCORRAL_TEST_AMBIGUOUS_VOLUME="${ambiguous_overlay_volume}"
export HCORRAL_PRIVATE_ENV=true
expect_status 1 run_hcorral -f "${ambiguous_overlay}" up -d >/dev/null 2>&1
unset HCORRAL_PRIVATE_ENV
if docker volume inspect "${project}" >/dev/null 2>&1 || docker container inspect "${project}" >/dev/null 2>&1 || docker network inspect "${project}_default" >/dev/null 2>&1; then
  echo 'volume ownership refusal mutated Docker state' >&2
  exit 1
fi
run_hcorral up -d >/dev/null
expect_status 1 run_hcorral -f "${ambiguous_overlay}" down -v >/dev/null 2>&1
unset HCORRAL_TEST_AMBIGUOUS_VOLUME
[[ "$(docker inspect --format '{{.State.Running}}' "${project}")" == true ]]
run_hcorral down >/dev/null
docker volume rm "${ambiguous_overlay_volume}" >/dev/null
ambiguous_overlay_volume=""

# Shared-state deletion is tested only when this disposable daemon did not
# already contain a user's global hcorral_state volume.
if ! docker volume inspect hcorral_state >/dev/null 2>&1; then
  shared_created=true
  run_hcorral up -d >/dev/null
  docker run --detach --name "${shared_ref_name}" \
    --mount type=volume,source=hcorral_state,target=/state \
    --entrypoint sleep "${image}" infinity >/dev/null
  run_hcorral down -v >/dev/null
  docker volume inspect hcorral_state >/dev/null
  docker rm --force "${shared_ref_name}" >/dev/null
  docker volume rm hcorral_state >/dev/null
  shared_created=false

  shared_created=true
  run_hcorral up -d >/dev/null
  run_hcorral down -v >/dev/null
  if docker volume inspect hcorral_state >/dev/null 2>&1; then
    echo 'shared hcorral_state survived safe down -v' >&2
    exit 1
  fi
  shared_created=false
else
  echo 'SKIP: shared-state removal (pre-existing hcorral_state)' >&2
fi

docker volume rm "${custom_volume}" >/dev/null
custom_volume=""
echo 'PASS: real Docker safety, state, overlay, mount, and wrapper contracts'
