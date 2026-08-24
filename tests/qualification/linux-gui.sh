#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
case "${mode}" in x11|wayland) ;; *) echo 'usage: linux-gui.sh x11|wayland' >&2; exit 2 ;; esac

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
binary="${HCORRAL_TEST_BINARY:-${repo_root}/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
[[ -x "${binary}" ]] || { echo "missing test binary: ${binary}" >&2; exit 2; }

test_root="$(mktemp -d /tmp/hcorral-gui.XXXXXX)"
workspace="${test_root}/workspace"
image="hcorral-gui-qualification:$(date +%s)-$$"
project=""
mkdir -p "${workspace}" "${test_root}/cache"

cleanup() {
  if [[ -n "${project}" ]]; then
    XDG_CACHE_HOME="${test_root}/cache" HCORRAL_WORKSPACE="${workspace}" HCORRAL_IMAGE_NAME="${image%:*}" HCORRAL_IMAGE_TAG="${image##*:}" HCORRAL_PRIVATE_ENV=true HCORRAL_UPDATE_CHECK=false \
      "${binary}" --gui="${mode}" down -v >/dev/null 2>&1 || true
  fi
  docker image rm "${image}" >/dev/null 2>&1 || true
  rm -r -- "${test_root}"
}
trap cleanup EXIT

docker build --quiet --tag "${image}" --file "${repo_root}/tests/fixtures/minimal-image/Dockerfile" "${repo_root}" >/dev/null
run_hcorral() {
  XDG_CACHE_HOME="${test_root}/cache" \
  HCORRAL_WORKSPACE="${workspace}" \
  HCORRAL_IMAGE_NAME="${image%:*}" \
  HCORRAL_IMAGE_TAG="${image##*:}" \
  HCORRAL_PRIVATE_ENV=true \
  HCORRAL_UPDATE_CHECK=false \
    "${binary}" "$@"
}

project="$(run_hcorral info --format=json | sed -n '/^[[:space:]]*"project": {/,/^[[:space:]]*}/ s/^[[:space:]]*"name": "\([^"]*\)",*$/\1/p' | head -1)"
run_hcorral --gui="${mode}" up -d >/dev/null
[[ "$(docker inspect --format '{{index .Config.Labels "ai.infrasecture.hcorral.gui"}}' "${project}")" == "${mode}" ]]

case "${mode}" in
  x11)
    run_hcorral exec xset q >/dev/null
    gui_mounts="$(docker inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' "${project}" | grep -Ec '^/tmp/\.hcorral-xauthority$|^/tmp/\.X11-unix/X[0-9]+$')"
    [[ "${gui_mounts}" -eq 2 ]]
    docker inspect --format '{{range .Mounts}}{{if eq .Destination "/tmp/.hcorral-xauthority"}}{{.RW}}{{end}}{{end}}' "${project}" | grep -Fxq false
    ;;
  wayland)
    run_hcorral exec wayland-info >/dev/null
    docker inspect --format '{{range .Mounts}}{{if eq .Destination "/tmp/.hcorral-wayland"}}{{.RW}}{{end}}{{end}}' "${project}" | grep -Fxq false
    ;;
esac

if docker inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' "${project}" | grep -Eq '^/run/user($|/)|^/tmp/\.X11-unix$'; then
  echo 'GUI qualification found a forbidden broad host mount' >&2
  exit 1
fi

run_hcorral --gui="${mode}" down -v >/dev/null
project=""
echo "PASS: native Linux ${mode} forwarding and mount boundary"
