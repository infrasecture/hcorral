#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
case "${mode}" in x11|wayland) ;; *) echo 'usage: linux-gui.sh x11|wayland' >&2; exit 2 ;; esac
# Select the X11-only discovery case even when invoked from a Wayland desktop.
if [[ "$mode" == x11 ]]; then unset WAYLAND_DISPLAY; fi

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
binary="${HCORRAL_TEST_BINARY:-${repo_root}/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
[[ -x "${binary}" ]] || { echo "missing test binary: ${binary}" >&2; exit 2; }

test_root="$(mktemp -d /tmp/hcorral-gui.XXXXXX)"
workspace="${test_root}/workspace"
image="${HCORRAL_TEST_GUI_IMAGE:-hcorral-gui-qualification:$(date +%s)-$$}"
project=""
mkdir -p "${workspace}" "${test_root}/cache"
export XDG_CONFIG_HOME="${test_root}/config" XDG_STATE_HOME="${test_root}/state"

cleanup() {
  if [[ -n "${project}" ]]; then
    XDG_CACHE_HOME="${test_root}/cache" HCORRAL_WORKSPACE="${workspace}" HCORRAL_IMAGE="${image}" HCORRAL_PRIVATE_ENV=true HCORRAL_UPDATE_CHECK=false \
      "${binary}" --gui="${mode}" down -v >/dev/null 2>&1 || true
  fi
  if [[ -z "${HCORRAL_TEST_GUI_IMAGE:-}" ]]; then docker image rm "${image}" >/dev/null 2>&1 || true; fi
  rm -r -- "${test_root}"
}
trap cleanup EXIT

if [[ -z "${HCORRAL_TEST_GUI_IMAGE:-}" ]]; then
  docker build --quiet --tag "${image}" --file "${repo_root}/tests/fixtures/minimal-image/Dockerfile" "${repo_root}" >/dev/null
fi
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
if [[ -n "${HCORRAL_TEST_TMUX_UID:-}" ]]; then
  [[ "$(run_hcorral exec id -u)" == "$HCORRAL_TEST_TMUX_UID" ]]
fi

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

before="$(docker inspect --format '{{.Id}}|{{.State.StartedAt}}|{{json .Mounts}}' "$project")"
# Change discovery inputs while attaching: the existing deployment stays in its
# original GUI mode, and its session-scoped badge must remain visible.
XDG_CACHE_HOME="$test_root/cache" HCORRAL_WORKSPACE="$workspace" HCORRAL_IMAGE="$image" \
  HCORRAL_PRIVATE_ENV=true HCORRAL_UPDATE_CHECK=false DISPLAY='' WAYLAND_DISPLAY='' \
  python3 "$repo_root/tests/integration/attach-probe.py" "$project" "$binary" attach
tmux_command=(docker exec "$project")
if [[ -n "${HCORRAL_TEST_TMUX_UID:-}" ]]; then tmux_command+=(gosu "$HCORRAL_TEST_TMUX_UID"); fi
tmux_command+=(tmux)
badge=GUI:X11
[[ "$mode" != wayland ]] || badge=GUI:WL
[[ "$("${tmux_command[@]}" show-options -qv -t hcorral @hcorral-gui)" == "$badge" ]]
[[ "$(docker inspect --format '{{.Id}}|{{.State.StartedAt}}|{{json .Mounts}}' "$project")" == "$before" ]]

# A failed explicit request cannot silently recreate this container headless.
if DISPLAY='' WAYLAND_DISPLAY='' run_hcorral --gui="$mode" up -d >"$test_root/missing-display.log" 2>&1; then
  echo 'explicit GUI request without its display unexpectedly succeeded' >&2
  exit 1
fi
[[ "$(docker inspect --format '{{.Id}}|{{.State.StartedAt}}|{{json .Mounts}}' "$project")" == "$before" ]]

run_hcorral --gui="${mode}" down -v >/dev/null
project=""
echo "PASS: native Linux ${mode} forwarding and mount boundary"
