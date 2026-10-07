#!/usr/bin/env bash
# Exercise the real container-side deadline, including a child ignoring TERM.
set -Eeuo pipefail
trap 'printf "Version probe assertion failed at line %s: %s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=tests/qualification/container-state.sh
source "$root/tests/qualification/container-state.sh"
image="${1:?usage: version-probe.sh QUALIFIED_HCORRAL_CODEX_IMAGE}"
arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
binary="${HCORRAL_TEST_BINARY:-$root/dist/bin/hcorral-linux-$arch}"
[[ "$(uname -s)" == Linux && -x "$binary" ]]
test_root="$(mktemp -d /tmp/hcorral-version-probe.XXXXXX)"
project="hcorral-probe-$$"
volume="$project-state"
uid="$(id -u)"; gid="$(id -g)"
cleanup() {
  "$binary" down >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
export XDG_CONFIG_HOME="$test_root/config" XDG_CACHE_HOME="$test_root/cache"
export HCORRAL_WORKSPACE="$test_root/workspace" HCORRAL_PROJECT_NAME="$project"
export HCORRAL_STATE_VOLUME_NAME="$volume" HCORRAL_CONTAINER_HOME=/home/probe
export HCORRAL_HARNESS=codex HCORRAL_IMAGE="$image" HCORRAL_GUI=none
export HCORRAL_UPDATE_CHECK=false HCORRAL_AUTO_PULL=false
unset HCORRAL_PRIVATE_ENV HCORRAL_COMPOSE_FILES
mkdir -p "$HCORRAL_WORKSPACE"
"$binary" up -d
ready=0
for _ in {1..100}; do
  if [[ "$(docker exec "$project" cat /run/hcorral-startup-status 2>/dev/null || true)" == ready ]]; then ready=1; break; fi
  sleep 0.1
done
if [[ "$ready" != 1 ]]; then docker logs "$project" >&2; exit 1; fi
before="$(container_snapshot "$project")"
# The real image must grant the numeric supplementary groups supplied by the
# launcher as well as switching UID/GID. Extra image-owned memberships are not
# interpreted as lost host membership.
docker exec "$project" gosu "$uid" id -G >"$test_root/runtime-groups"
python3 - "$test_root/runtime-groups" <<'PY'
import os
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    actual = {int(group) for group in stream.read().split()}
expected = set(os.getgroups()) | {os.getegid()}
assert expected <= actual, (actual, expected)
PY
expected="$(docker image inspect --format '{{index .Config.Labels "ai.infrasecture.hcorral.harness.version"}}' "$image")"
[[ -n "$expected" ]]
timeout 20 "$binary" info --format=json >"$test_root/healthy.json"
python3 - "$test_root/healthy.json" "$expected" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    facts = json.load(stream)["update"]
assert facts["installed"] == facts["current"] == facts["bundled"] == sys.argv[2], facts
PY

for mode in login executable; do
  # Only this fixture's private startup file/tool is changed. Both scenarios
  # must reach the actual runtime user and start a child before timing out.
  docker exec -i --user "$uid:$gid" "$project" env HOME=/home/probe \
    bash --noprofile --norc -s -- "$mode" <<'SHELL'
set -euo pipefail
umask 077
rm -f "$HOME/probe-pids" "$HOME/probe-uid"
mkdir -p "$HOME/.local/bin"
target="$HOME/.bash_profile"
if [[ "$1" == executable ]]; then
  printf 'export PATH="$HOME/.local/bin:$PATH"\n' >"$target"
  target="$HOME/.local/bin/codex"
fi
cat >"$target" <<'BLOCK'
#!/usr/bin/env bash
trap '' TERM
id -u >"$HOME/probe-uid"
sleep 120 &
printf '%s\n' "$PPID" "$$" "$!" >"$HOME/probe-pids"
wait
BLOCK
chmod 0700 "$target"
SHELL
  timeout 20 "$binary" info --format=json >"$test_root/$mode.json"
  python3 - "$test_root/$mode.json" "$expected" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    facts = json.load(stream)["update"]
assert facts["installed"] == "", facts
assert facts["current"] == facts["bundled"] == sys.argv[2], facts
PY
  [[ "$(docker exec "$project" cat /home/probe/probe-uid)" == "$uid" ]]
  # Compose's init reaps the killed process group. Require disappearance,
  # including the timeout monitor, rather than treating a zombie as running.
  reaped=0
  for _ in {1..50}; do
    if docker exec "$project" bash --noprofile --norc -c '
      mapfile -t pids </home/probe/probe-pids
      [[ ${#pids[@]} == 3 ]]
      for pid in "${pids[@]}"; do
        [[ "$pid" =~ ^[1-9][0-9]*$ ]] && [[ ! -e "/proc/$pid" ]] || exit 1
      done
    '; then reaped=1; break; fi
    sleep 0.1
  done
  if [[ "$reaped" != 1 ]]; then
    docker top "$project" -eo pid,ppid,args >&2
    exit 1
  fi
  assert_container_snapshot "$project" "$before"
  printf 'PASS: bounded %s version probe, runtime UID and process-group cleanup (%s)\n' "$mode" "$arch"
done

docker exec --user "$uid:$gid" "$project" rm /home/probe/.bash_profile /home/probe/.local/bin/codex
timeout 20 "$binary" info --format=json >"$test_root/recovered.json"
python3 - "$test_root/recovered.json" "$expected" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    facts = json.load(stream)["update"]
assert facts["installed"] == sys.argv[2], facts
PY
assert_container_snapshot "$project" "$before"
