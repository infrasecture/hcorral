#!/usr/bin/env bash
# Native protocol servers on a disposable runner, using the production runtime.
set -euo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
base="${1:?usage: hosted-gui.sh QUALIFIED_HCORRAL_CODEX_IMAGE}"
test_root="$(mktemp -d /tmp/hcorral-display.XXXXXX)"
image="hcorral-gui-probe:$$"
weston_pid=""; x_pid=""
cleanup() {
  for pid in "$x_pid" "$weston_pid"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  done
  docker image rm "$image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
for command in Xvfb Xwayland weston xauth xset openssl; do command -v "$command" >/dev/null; done

# Add only protocol diagnostic clients. Entrypoint, shared shell initialization,
# gosu, tmux, accounts and permission handling are the actual production image.
docker build --quiet --build-arg "BASE=$base" --tag "$image" - <<'DOCKERFILE'
ARG BASE
FROM ${BASE}
RUN apt-get update && apt-get install -y --no-install-recommends x11-xserver-utils wayland-utils \
    && rm -rf /var/lib/apt/lists/*
DOCKERFILE
export XDG_RUNTIME_DIR="$test_root/runtime" XAUTHORITY="$test_root/authority"
mkdir -m 0700 "$XDG_RUNTIME_DIR"
touch "$XAUTHORITY"
chmod 0600 "$XAUTHORITY"
unset SSH_CONNECTION SSH_CLIENT SSH_TTY HCORRAL_GUI HCORRAL_PROJECT_NAME HCORRAL_STATE_VOLUME_NAME
unset HCORRAL_PRIVATE_ENV HCORRAL_COMPOSE_FILES HCORRAL_WORKSPACE HCORRAL_CONTAINER_HOME DISPLAY WAYLAND_DISPLAY
export HCORRAL_TEST_GUI_IMAGE="$image" HCORRAL_TEST_TMUX_UID
HCORRAL_TEST_TMUX_UID="$(id -u)"

wait_socket() {
  local socket="$1" pid="$2" log="$3"
  for _ in {1..100}; do
    if [[ -S "$socket" ]]; then return; fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
  done
  cat "$log" >&2
  echo "display server did not create $socket" >&2
  return 1
}
start_x() {
  local server="$1" number="$2"
  shift 2
  export DISPLAY=":$number"
  [[ ! -e "/tmp/.X11-unix/X$number" && ! -e "/tmp/.X$number-lock" ]]
  xauth -f "$XAUTHORITY" add "$DISPLAY" . "$(openssl rand -hex 16)"
  "$server" "$DISPLAY" -nolisten tcp -noreset -auth "$XAUTHORITY" "$@" >"$test_root/x.log" 2>&1 &
  x_pid=$!
  wait_socket "/tmp/.X11-unix/X$number" "$x_pid" "$test_root/x.log"
  xset q >/dev/null
}
number=$((30000 + $$ % 20000))
start_x Xvfb "$number" -screen 0 1024x768x24
"$root/tests/qualification/linux-gui.sh" x11
kill "$x_pid"; wait "$x_pid" || true
x_pid=""
unset DISPLAY

weston --backend=headless --renderer=pixman --shell=kiosk-shell.so --no-config \
  --idle-time=0 --socket=hcorral-test --log="$test_root/weston.log" >"$test_root/weston.stderr" 2>&1 &
weston_pid=$!
export WAYLAND_DISPLAY=hcorral-test
wait_socket "$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY" "$weston_pid" "$test_root/weston.log"
"$root/tests/qualification/linux-gui.sh" wayland

# Xwayland runs as a Wayland client; hcorral selects its authenticated X11
# socket when no Wayland display is advertised to that launcher invocation.
start_x Xwayland "$((number + 1))" -shm
"$root/tests/qualification/linux-gui.sh" wayland
WAYLAND_DISPLAY='' "$root/tests/qualification/linux-gui.sh" x11
printf 'PASS: native Xvfb, Weston and XWayland protocol forwarding as UID %s\n' "$HCORRAL_TEST_TMUX_UID"
