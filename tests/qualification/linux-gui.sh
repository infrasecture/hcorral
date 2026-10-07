#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "GUI assertion failed at line %s: %s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR

mode="${1:-}"
case "${mode}" in x11|wayland) ;; *) echo 'usage: linux-gui.sh x11|wayland QUALIFIED_HCORRAL_CODEX_IMAGE' >&2; exit 2 ;; esac
[[ $# -eq 2 && -n "$2" ]] || { echo 'a qualified production Codex image is required' >&2; exit 2; }
base="$2"
# Select the X11-only discovery case even when invoked from a Wayland desktop.
if [[ "$mode" == x11 ]]; then unset WAYLAND_DISPLAY; fi

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=tests/qualification/container-state.sh
source "$repo_root/tests/qualification/container-state.sh"
binary="${HCORRAL_TEST_BINARY:-${repo_root}/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
[[ -x "${binary}" ]] || { echo "missing test binary: ${binary}" >&2; exit 2; }

test_root="$(mktemp -d /tmp/hcorral-gui.XXXXXX)"
workspace="${test_root}/workspace"
image="hcorral-gui-qualification:$(basename "$test_root")"
project=""
mkdir -p "${workspace}" "${test_root}/cache"
export XDG_CONFIG_HOME="${test_root}/config" XDG_STATE_HOME="${test_root}/state"
# Do not let a maintainer's project, shared volume or Compose overlays select
# real resources. Preserve desktop credentials and Docker connection settings.
unset HCORRAL_GUI HCORRAL_PROJECT_NAME HCORRAL_STATE_VOLUME_NAME HCORRAL_COMPOSE_FILES
unset HCORRAL_CONTAINER_HOME HCORRAL_WORKDIR HCORRAL_BYOBU_SESSION HCORRAL_AUTO_ATTACH
export HCORRAL_HARNESS=codex HCORRAL_TEST_TMUX_UID
HCORRAL_TEST_TMUX_UID="$(id -u)"

cleanup() {
  if [[ -n "${project}" ]]; then
    XDG_CACHE_HOME="${test_root}/cache" HCORRAL_WORKSPACE="${workspace}" HCORRAL_IMAGE="${image}" HCORRAL_PRIVATE_ENV=true HCORRAL_UPDATE_CHECK=false \
      "${binary}" --gui="${mode}" down -v >/dev/null 2>&1 || true
  fi
  docker image rm "${image}" >/dev/null 2>&1 || true
  rm -r -- "${test_root}"
}
trap cleanup EXIT

# Add diagnostic clients to the actual production runtime. Both hosted and
# physical-desktop qualification exercise its entrypoint, gosu and tmux user.
docker image inspect "$base" >/dev/null
docker build --quiet --build-arg "BASE=$base" --tag "$image" - <<'DOCKERFILE'
ARG BASE
FROM ${BASE}
RUN apt-get update && apt-get install -y --no-install-recommends x11-xserver-utils wayland-utils \
    && rm -rf /var/lib/apt/lists/*
DOCKERFILE
run_hcorral() {
  XDG_CACHE_HOME="${test_root}/cache" \
  HCORRAL_WORKSPACE="${workspace}" \
  HCORRAL_IMAGE="${image}" \
  HCORRAL_PRIVATE_ENV=true \
  HCORRAL_UPDATE_CHECK=false \
    "${binary}" "$@"
}

project="$(run_hcorral info --format=json | sed -n '/^[[:space:]]*"project": {/,/^[[:space:]]*}/ s/^[[:space:]]*"name": "\([^"]*\)",*$/\1/p' | head -1)"
run_hcorral up -d >/dev/null
ready=0
for _ in {1..100}; do
  if [[ "$(docker exec "$project" cat /run/hcorral-startup-status 2>/dev/null || true)" == ready ]]; then ready=1; break; fi
  sleep 0.1
done
if [[ "$ready" != 1 ]]; then docker logs "$project" >&2; exit 1; fi
[[ "$(docker inspect --format '{{index .Config.Labels "ai.infrasecture.hcorral.gui"}}' "${project}")" == "${mode}" ]]
[[ "$(run_hcorral exec id -u)" == "$HCORRAL_TEST_TMUX_UID" ]]

case "${mode}" in
  x11)
    run_hcorral exec timeout 10 xset q >/dev/null
    gui_mounts="$(docker inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' "${project}" | grep -Ec '^/tmp/\.hcorral-xauthority$|^/tmp/\.X11-unix/X[0-9]+$')"
    [[ "${gui_mounts}" -eq 2 ]]
    docker inspect --format '{{range .Mounts}}{{if eq .Destination "/tmp/.hcorral-xauthority"}}{{.RW}}{{end}}{{end}}' "${project}" | grep -Fxq false
    ;;
  wayland)
    run_hcorral exec timeout 10 wayland-info >/dev/null
    docker inspect --format '{{range .Mounts}}{{if eq .Destination "/tmp/.hcorral-wayland"}}{{.RW}}{{end}}{{end}}' "${project}" | grep -Fxq false
    ;;
esac

if docker inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' "${project}" | grep -Eq '^/run/user($|/)|^/tmp/\.X11-unix$'; then
  echo 'GUI qualification found a forbidden broad host mount' >&2
  exit 1
fi

before="$(container_snapshot "$project")"
# Change discovery inputs while attaching: the existing deployment stays in its
# original GUI mode, and its session-scoped badge must remain visible.
XDG_CACHE_HOME="$test_root/cache" HCORRAL_WORKSPACE="$workspace" HCORRAL_IMAGE="$image" \
  HCORRAL_PRIVATE_ENV=true HCORRAL_UPDATE_CHECK=false DISPLAY='' WAYLAND_DISPLAY='' \
  python3 "$repo_root/tests/integration/attach-probe.py" "$project" "$binary" attach
tmux_command=(docker exec "$project" gosu "$HCORRAL_TEST_TMUX_UID" tmux)
badge=GUI:X11
[[ "$mode" != wayland ]] || badge=GUI:WL
[[ "$("${tmux_command[@]}" show-options -qv -t hcorral @hcorral-gui)" == "$badge" ]]
assert_container_snapshot "$project" "$before"

# A failed explicit request cannot silently recreate this container headless.
if DISPLAY='' WAYLAND_DISPLAY='' run_hcorral --gui="$mode" up -d >"$test_root/missing-display.log" 2>&1; then
  echo 'explicit GUI request without its display unexpectedly succeeded' >&2
  exit 1
fi
assert_container_snapshot "$project" "$before"

run_hcorral --gui="${mode}" down -v >/dev/null
project=""
echo "PASS: native Linux ${mode} forwarding and mount boundary"
