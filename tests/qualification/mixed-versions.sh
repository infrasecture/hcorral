#!/usr/bin/env bash
# Qualify actual old/new artifacts, with no publication or user-state adoption.
set -Eeuo pipefail
trap 'printf "Mixed-version assertion failed at line %s: %s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=tests/qualification/container-state.sh
source "$root/tests/qualification/container-state.sh"
arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
binary="${HCORRAL_TEST_BINARY:-$root/dist/bin/hcorral-linux-$arch}"
[[ "$(uname -s)" == Linux && -x "$binary" ]]
old_binary="${HCORRAL_TEST_OLD_BINARY:?run tests/run.sh compat to prepare fixtures}"
old_image="${HCORRAL_TEST_OLD_IMAGE:?missing historical image}"
new_image="${HCORRAL_TEST_NEW_IMAGE:?missing current image}"
test_root="$(mktemp -d /tmp/hcorral-mixed-cases.XXXXXX)"
project=""; volume=""
cleanup() {
  if [[ -n "$project" ]]; then "$binary" down >/dev/null 2>&1 || true; fi
  if [[ -n "$volume" ]]; then docker volume rm "$volume" >/dev/null 2>&1 || true; fi
  rm -rf -- "$test_root"
}
trap cleanup EXIT

export XDG_CONFIG_HOME="$test_root/config" XDG_CACHE_HOME="$test_root/cache"
export HCORRAL_HARNESS=codex HCORRAL_GUI=none HCORRAL_UPDATE_CHECK=false
export HCORRAL_CONTAINER_HOME=/home/compatibility
HCORRAL_TEST_TMUX_UID="$(id -u)"
export HCORRAL_TEST_TMUX_UID
unset HCORRAL_PRIVATE_ENV HCORRAL_COMPOSE_FILES HCORRAL_AUTO_PULL

tmux_in_container() { docker exec "$project" gosu "$HCORRAL_TEST_TMUX_UID" tmux "$@"; }
for launcher_generation in old new; do
  launcher="$binary"
  [[ "$launcher_generation" != old ]] || launcher="$old_binary"
  for image_generation in old new; do
    image="$new_image"
    [[ "$image_generation" != old ]] || image="$old_image"
    project="hcorral-mixed-${launcher_generation}-${image_generation}-$$"
    volume="${project}-state"
    export HCORRAL_PROJECT_NAME="$project" HCORRAL_STATE_VOLUME_NAME="$volume" HCORRAL_IMAGE="$image"
    export HCORRAL_WORKSPACE="$test_root/workspace-${launcher_generation}-${image_generation}"
    mkdir -p "$HCORRAL_WORKSPACE"
    "$launcher" up -d
    ready=0
    for _ in {1..100}; do
      if [[ "$(docker exec "$project" cat /run/hcorral-startup-status 2>/dev/null || true)" == ready ]]; then ready=1; break; fi
      sleep 0.1
    done
    if [[ "$ready" != 1 ]]; then docker logs "$project" >&2; exit 1; fi
    expected_image="$(docker image inspect --format '{{.Id}}' "$image")"
    [[ "$(docker inspect --format '{{.Image}}' "$project")" == "$expected_image" ]]
    before="$(container_snapshot "$project")"
    panes="$(tmux_in_container list-panes -t hcorral -F '#{session_id}:#{pane_id}')"
    # shellcheck disable=SC2016 # Expanded by the runtime user's container shell.
    "$launcher" exec bash -c 'printf "persistent state\n" >"$HOME/compatibility-sentinel"'
    marker="$(docker exec "$project" sha256sum /home/compatibility/compatibility-sentinel)"
    python3 "$root/tests/integration/attach-probe.py" "$project" "$launcher" attach
    python3 "$root/tests/integration/attach-probe.py" "$project" "$binary" attach
    [[ "$(tmux_in_container show-options -qv -t hcorral @hcorral-gui)" == GUI:off ]]
    tmux_in_container show-options -qv -t hcorral @hcorral-notices | grep -Fq 'GUI access disabled'
    tmux_in_container set-option -t hcorral @hcorral-notices 'Retained startup report on a mixed-version workstation'
    HCORRAL_TEST_ATTACH_TEXT='Retained startup report on a mixed-version workstation' \
      python3 "$root/tests/integration/attach-probe.py" "$project" "$binary" notices
    [[ "$(tmux_in_container show-options -qv -t hcorral @hcorral-notices)" == 'Retained startup report on a mixed-version workstation' ]]
    [[ "$(tmux_in_container list-panes -t hcorral -F '#{session_id}:#{pane_id}')" == "$panes" ]]
    [[ "$(docker exec "$project" sha256sum /home/compatibility/compatibility-sentinel)" == "$marker" ]]
    assert_container_snapshot "$project" "$before"
    "$launcher" down
    docker volume inspect "$volume" >/dev/null
    docker volume rm "$volume" >/dev/null
    project=""; volume=""
    printf 'PASS: %s launcher / %s image, retained notices and persistent state (%s)\n' "$launcher_generation" "$image_generation" "$arch"
  done
done
