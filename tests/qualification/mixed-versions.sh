#!/usr/bin/env bash
# Qualify actual old/new artifacts, with no publication or user-state adoption.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
binary="${HCORRAL_TEST_BINARY:-$root/dist/bin/hcorral-linux-$arch}"
[[ "$(uname -s)" == Linux && -x "$binary" ]]
baseline=18fb3944f9b04758b920b1e9415fd017eff8cf51
version=0.160.0
test_root="$(mktemp -d /tmp/hcorral-mixed.XXXXXX)"
baseline_root="$test_root/baseline"
old_repo="hcorral-compat-old-$$"
new_repo="hcorral-compat-new-$$"
old_image="$old_repo:$version-r1-$arch"
new_image="$new_repo:$version-r1-$arch"
project=""; volume=""
cleanup() {
  if [[ -n "$project" ]]; then "$binary" down >/dev/null 2>&1 || true; fi
  if [[ -n "$volume" ]]; then docker volume rm "$volume" >/dev/null 2>&1 || true; fi
  docker image rm "$old_image" "$old_repo:$version-r1" "$new_image" "$new_repo:$version-r1" >/dev/null 2>&1 || true
  git -C "$root" worktree remove --force "$baseline_root" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT

# Download the real published launcher; checksums came from its release assets.
case "$arch" in
  amd64) checksum=bdfb4d53728ec47af174d741ca30b75451fdadd931512defd8656704ff954ba2 ;;
  arm64) checksum=06528c2f1a9d390afaecf9e79c27aad494d7150cdb2cce6c84f941bd3c4b6d30 ;;
  *) echo 'unsupported qualification architecture' >&2; exit 2 ;;
esac
archive="hcorral_0.1.0_linux_${arch}.tar.gz"
curl --fail --silent --show-error --location --retry 3 --max-time 120 \
  --output "$test_root/$archive" "https://github.com/infrasecture/hcorral/releases/download/v0.1.0/$archive"
(cd "$test_root" && printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --strict)
tar -xzf "$test_root/$archive" -C "$test_root" hcorral
old_binary="$test_root/hcorral"
"$old_binary" version

# Build the historical recipe itself, retaining real provenance. No --push or
# moving alias is used; the repositories below exist only on the test daemon.
git -C "$root" worktree add --detach "$baseline_root" "$baseline"
HCORRAL_IMAGE_REPOSITORY="$old_repo" "$baseline_root/scripts/build-harness-image.sh" \
  --harness codex --version "$version" --revision 1 --arch "$arch"
HCORRAL_IMAGE_REPOSITORY="$new_repo" "$root/scripts/build-harness-image.sh" \
  --harness codex --version "$version" --revision 1 --arch "$arch"
[[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$old_image")" == "$baseline" ]]
docker run --rm --entrypoint /bin/bash "$old_image" -c 'test ! -e /etc/hcorral/bashrc'
docker run --rm --entrypoint /bin/bash "$new_image" -c 'test -r /etc/hcorral/bashrc'
HCORRAL_TEST_SHARED_HOME_ONLY=1 "$root/tests/image/runtime-home.sh" "$new_image" "$old_image"

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
    expected_image="$(docker image inspect --format '{{.Id}}' "$image")"
    [[ "$(docker inspect --format '{{.Image}}' "$project")" == "$expected_image" ]]
    before="$(docker inspect --format '{{.Id}}|{{.State.StartedAt}}|{{json .Mounts}}' "$project")"
    panes="$(tmux_in_container list-panes -t hcorral -F '#{session_id}:#{pane_id}')"
    # shellcheck disable=SC2016 # Expanded by the runtime user's container shell.
    "$launcher" exec bash -c 'printf "persistent state\n" >"$HOME/compatibility-sentinel"'
    marker="$(docker exec "$project" sha256sum /home/compatibility/compatibility-sentinel)"
    python3 "$root/tests/integration/attach-probe.py" "$project" "$launcher" attach
    python3 "$root/tests/integration/attach-probe.py" "$project" "$binary" attach
    [[ "$(tmux_in_container show-options -qv -t hcorral @hcorral-gui)" == GUI:off ]]
    tmux_in_container show-options -qv -t hcorral @hcorral-notices | grep -Fq 'GUI access disabled'
    tmux_in_container set-option -t hcorral @hcorral-notices 'Retained startup report on a mixed-version workstation'
    python3 "$root/tests/integration/attach-probe.py" "$project" "$binary" notices
    [[ "$(tmux_in_container show-options -qv -t hcorral @hcorral-notices)" == 'Retained startup report on a mixed-version workstation' ]]
    [[ "$(tmux_in_container list-panes -t hcorral -F '#{session_id}:#{pane_id}')" == "$panes" ]]
    [[ "$(docker exec "$project" sha256sum /home/compatibility/compatibility-sentinel)" == "$marker" ]]
    [[ "$(docker inspect --format '{{.Id}}|{{.State.StartedAt}}|{{json .Mounts}}' "$project")" == "$before" ]]
    "$launcher" down
    docker volume inspect "$volume" >/dev/null
    docker volume rm "$volume" >/dev/null
    project=""; volume=""
    printf 'PASS: %s launcher / %s image, retained notices and persistent state (%s)\n' "$launcher_generation" "$image_generation" "$arch"
  done
done
