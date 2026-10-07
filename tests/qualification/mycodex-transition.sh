#!/usr/bin/env bash
# Exercise the documented manual transition on disposable, synthetic state.
set -Eeuo pipefail
trap 'printf "Transition assertion failed at line %s: %s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=tests/qualification/container-state.sh
source "$root/tests/qualification/container-state.sh"
image="${1:?usage: mycodex-transition.sh QUALIFIED_HCORRAL_CODEX_IMAGE}"
arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
binary="${HCORRAL_TEST_BINARY:-$root/dist/bin/hcorral-linux-$arch}"
[[ "$(uname -s)" == Linux && -x "$binary" ]]
baseline=ebc930ac00adea662789d6c2f43666ec1003eca0
test_root="$(mktemp -d /tmp/hcorral-transition.XXXXXX)"
legacy_root="$test_root/myCodex"
legacy_image="hcorral-transition-legacy:$$"
legacy_container=""; project=""; source_volume=""; copy_volume=""
receiver=""; receiver_volume=""
cleanup() {
  for container in "$legacy_container" "$project" "$receiver"; do
    if [[ -n "$container" ]]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi
  done
  if [[ -n "${workspace:-}" ]]; then
    docker network rm "$(basename "$workspace")_default" "${project}_default" >/dev/null 2>&1 || true
  fi
  if [[ -n "$receiver" ]]; then docker network rm "${receiver}_default" >/dev/null 2>&1 || true; fi
  for volume in "$source_volume" "$copy_volume" "$receiver_volume"; do
    if [[ -n "$volume" ]]; then docker volume rm "$volume" >/dev/null 2>&1 || true; fi
  done
  docker image rm "$legacy_image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT

# Fetch immutable source, not the user's checkout or its uncommitted changes.
git init --quiet "$legacy_root"
git -C "$legacy_root" fetch --quiet --depth=1 https://github.com/emsi/myCodex.git "$baseline"
git -C "$legacy_root" checkout --quiet --detach FETCH_HEAD
[[ "$(git -C "$legacy_root" rev-parse HEAD)" == "$baseline" ]]
docker buildx build --load --platform "linux/$arch" --tag "$legacy_image" \
  --build-arg CODEX_VERSION=0.160.0 --build-arg "MYCODEX_SOURCE_REVISION=$baseline" \
  --build-arg INSTALL_CLAUDE_CODE=0 --build-arg INSTALL_GEMINI_CLI=0 --build-arg INSTALL_OPENCODE=0 \
  "$legacy_root"
[[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$legacy_image")" == "$baseline" ]]

export XDG_CONFIG_HOME="$test_root/config" XDG_CACHE_HOME="$test_root/cache"
export HCORRAL_HARNESS=codex HCORRAL_GUI=none HCORRAL_UPDATE_CHECK=false HCORRAL_AUTO_PULL=false
export HCORRAL_CONTAINER_HOME=/home/transition HCORRAL_IMAGE="$image"
unset HCORRAL_PRIVATE_ENV HCORRAL_COMPOSE_FILES
uid="$(id -u)"; gid="$(id -g)"
thread=019a1234-1111-7111-8111-111111111111
rollout=".codex/sessions/2026/10/06/rollout-2026-10-06T12-34-56-$thread.jsonl"

legacy() {
  (cd "$workspace" && MYCODEX_IMAGE_NAME="${legacy_image%:*}" MYCODEX_IMAGE_TAG="${legacy_image##*:}" \
    MYCODEX_STATE_VOLUME_NAME="$source_volume" MYCODEX_CONTAINER_HOME=/home/transition \
    MYCODEX_UPDATE_CHECK=0 MYCODEX_AUTO_PULL=0 CODEX_CONTAINER_NAME="$legacy_container" \
    "$legacy_root/bin/myCodex" --no-gui "$@")
}
manifest() {
  docker run --rm --network none --entrypoint bash --mount "type=volume,src=$1,dst=/state,readonly" "$image" -c '
    set -eu
    cd /state
    for path in .bashrc .profile .codex/config.toml .codex/auth.json .local/bin/transition-tool "$1"; do
      sha256sum "$path"
      stat -c "%n %u:%g:%a" "$path"
    done
    readlink .transition-link
    stat -c "%n %u:%g:%a" .transition-link . .codex
  ' bash "$rollout"
}
visible() {
  python3 "$root/tests/qualification/session-visibility.py" "$thread" 'persisted transition message' "$workspace" \
    docker exec -i --user "$uid:$gid" "$1" env HOME=/home/transition \
    CODEX_HOME=/home/transition/.codex RUST_LOG=error codex app-server --listen stdio://
}
refused() {
  local status=0
  "$binary" up -d >"$test_root/refusal.log" 2>&1 || status=$?
  [[ "$status" == 3 ]]
  grep -Fq 'use the original myCodex launcher' "$test_root/refusal.log"
  ! docker container inspect "$project" >/dev/null 2>&1
}
public_round_trip() {
  local host_home="$test_root/exported home" native_home="$test_root/native-home"
  local native_codex="$test_root/codex" stopped
  mkdir -p "$native_home"
  "$binary" session export "$thread" "$host_home"
  [[ ! -e "$host_home/.codex" && ! -e "$host_home/auth.json" && ! -e "$host_home/config.toml" ]]
  # A separate test configuration enables native resume without transferring
  # credentials or invoking a provider. The fixture uses no model turn.
  docker exec "$project" cat /home/transition/.codex/config.toml >"$host_home/config.toml"
  docker cp "$project:/usr/local/bin/codex" "$native_codex"
  chmod 0755 "$native_codex"
  python3 "$root/tests/qualification/session-visibility.py" "$thread" 'persisted transition message' "$workspace" \
    env -i PATH="$PATH" HOME="$native_home" CODEX_HOME="$host_home" RUST_LOG=error \
    "$native_codex" app-server --listen stdio://

  receiver="hcorral-transition-receiver-$$"
  receiver_volume="${receiver}-state"
  "$binary" --project-name "$receiver" --state-volume "$receiver_volume" up -d
  local ready=0
  for _ in {1..100}; do
    if [[ "$(docker exec "$receiver" cat /run/hcorral-startup-status 2>/dev/null || true)" == ready ]]; then ready=1; break; fi
    sleep 0.1
  done
  if [[ "$ready" != 1 ]]; then docker logs "$receiver" >&2; exit 1; fi
  docker exec -i --user "$uid:$gid" "$receiver" sh -c 'cat >/home/transition/.codex/config.toml' <"$host_home/config.toml"
  "$binary" --project-name "$receiver" --state-volume "$receiver_volume" stop
  stopped="$(container_snapshot "$receiver")"
  "$binary" --project-name "$receiver" --state-volume "$receiver_volume" session import "$thread" "$host_home"
  assert_container_snapshot "$receiver" "$stopped"
  "$binary" --project-name "$receiver" --state-volume "$receiver_volume" start
  # The existing runtime account/home survives container restart; wait for the
  # actual tmux session rather than interpreting Docker's running flag as ready.
  ready=0
  for _ in {1..100}; do
    if docker exec "$receiver" gosu "$uid" tmux has-session -t hcorral 2>/dev/null; then ready=1; break; fi
    sleep 0.1
  done
  if [[ "$ready" != 1 ]]; then docker logs "$receiver" >&2; exit 1; fi
  visible "$receiver"
  "$binary" --project-name "$receiver" --state-volume "$receiver_volume" down
  docker volume rm "$receiver_volume" >/dev/null
  receiver=""; receiver_volume=""
  printf 'PASS: public Docker export, host native resume, stopped import and container native resume (%s)\n' "$arch"
}

for mode in copy reuse; do
  workspace="$test_root/transition-$mode-$$"
  project="hcorral-transition-$mode-$$"
  legacy_container="transition-$mode-$$-codex"
  source_volume="hcorral-transition-source-$mode-$$"
  copy_volume="hcorral-transition-copy-$mode-$$"
  mkdir -p "$workspace"
  export HCORRAL_WORKSPACE="$workspace" HCORRAL_PROJECT_NAME="$project"
  export HCORRAL_STATE_VOLUME_NAME="$source_volume"
  legacy up -d --wait
  # Only test-owned state and fake credentials are written. Preserve the actual
  # myCodex startup stub, adding a user customization to test its preservation.
  docker exec -i --user "$uid:$gid" "$legacy_container" env HOME=/home/transition \
    bash --noprofile --norc -s -- "$rollout" <<'EOF'
set -euo pipefail
umask 077
cd "$HOME"
mkdir -p .codex/sessions/2026/10/06 .local/bin
printf '\nexport HCORRAL_TRANSITION_CUSTOM=yes\n' >>.bashrc
printf '#!/bin/sh\nprintf "preserved tool\\n"\n' >.local/bin/transition-tool
chmod 0755 .local/bin/transition-tool
ln -s .codex/config.toml .transition-link
printf '{"OPENAI_API_KEY":"synthetic-unused-transition-key"}\n' >.codex/auth.json
cat >.codex/config.toml <<'CONFIG'
model = "fixture-model"
model_provider = "test-provider"
[model_providers.test-provider]
name = "Offline qualification"
base_url = "http://127.0.0.1:9/v1"
wire_api = "responses"
requires_openai_auth = false
CONFIG
cat >"$1" <<'HISTORY'
{"timestamp":"2026-10-06T12:34:56Z","type":"session_meta","payload":{"id":"019a1234-1111-7111-8111-111111111111","session_id":"019a1234-1111-7111-8111-111111111111","timestamp":"2026-10-06T12:34:56Z","cwd":"/home/transition","originator":"hcorral_test","cli_version":"0.160.0","source":"cli","model_provider":"test-provider","history_mode":"legacy"}}
{"timestamp":"2026-10-06T12:34:56Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"persisted transition message"}]}}
{"timestamp":"2026-10-06T12:34:56Z","type":"event_msg","payload":{"type":"user_message","message":"persisted transition message"}}
HISTORY
EOF
  visible "$legacy_container"
  before="$(manifest "$source_volume")"
  source_labels="$(docker volume inspect --format '{{json .Labels}}' "$source_volume")"
  legacy_identity="$(container_snapshot "$legacy_container")"
  refused
  [[ "$(manifest "$source_volume")" == "$before" ]]
  assert_container_snapshot "$legacy_container" "$legacy_identity"
  legacy stop
  refused
  [[ "$(manifest "$source_volume")" == "$before" ]]

  # The copy path retains the original for recovery; reuse is explicitly chosen
  # only for the second isolated fixture. No volume is adopted or relabelled.
  if [[ "$mode" == copy ]]; then
    docker volume create "$copy_volume" >/dev/null
    docker run --rm --network none --entrypoint bash \
      --mount "type=volume,src=$source_volume,dst=/source,readonly" \
      --mount "type=volume,src=$copy_volume,dst=/target" "$image" -c \
      'set -euo pipefail; tar --numeric-owner --acls --xattrs -C /source -cpf - . | tar --numeric-owner --acls --xattrs -C /target -xpf -'
    [[ "$(manifest "$copy_volume")" == "$before" ]]
    export HCORRAL_STATE_VOLUME_NAME="$copy_volume"
  fi
  legacy down
  docker volume inspect "$source_volume" >/dev/null
  "$binary" up -d
  ready=0
  for _ in {1..100}; do
    if [[ "$(docker exec "$project" cat /run/hcorral-startup-status 2>/dev/null || true)" == ready ]]; then ready=1; break; fi
    sleep 0.1
  done
  if [[ "$ready" != 1 ]]; then docker logs "$project" >&2; exit 1; fi
  [[ "$(manifest "$HCORRAL_STATE_VOLUME_NAME")" == "$before" ]]
  [[ "$(docker volume inspect --format '{{json .Labels}}' "$source_volume")" == "$source_labels" ]]
  # shellcheck disable=SC2016 # Assertions run in the runtime user's container.
  "$binary" exec bash --login -ic '[[ $- == *i* ]] && shopt -q login_shell && shopt -q histappend && [[ "$HCORRAL_TRANSITION_CUSTOM" == yes ]] && complete -p codex >/dev/null && [[ "$("$HOME/.local/bin/transition-tool")" == "preserved tool" ]]'
  [[ "$("$binary" exec id -u)" == "$uid" ]]
  [[ "$("$binary" exec id -g)" == "$gid" ]]
  visible "$project"
  if [[ "$mode" == copy ]]; then public_round_trip; fi
  "$binary" down -v
  docker volume inspect "$source_volume" "$HCORRAL_STATE_VOLUME_NAME" >/dev/null
  [[ "$(docker volume inspect --format '{{json .Labels}}' "$source_volume")" == "$source_labels" ]]
  if [[ "$mode" == copy ]]; then [[ "$(manifest "$source_volume")" == "$before" ]]; fi
  # Return through the original launcher and image with the original home.
  legacy up -d --wait
  visible "$legacy_container"
  legacy down
  docker volume rm "$source_volume" >/dev/null
  if [[ "$mode" == copy ]]; then docker volume rm "$copy_volume" >/dev/null; fi
  printf 'PASS: myCodex transition (%s), preserved state, native resume and return (%s)\n' "$mode" "$arch"
  legacy_container=""; project=""; source_volume=""; copy_volume=""
done
