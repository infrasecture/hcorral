#!/usr/bin/env bash
# Build each compatibility fixture once, then run independently named contracts.
set -Eeuo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/hcorral-image.sh
source "$root/scripts/lib/hcorral-image.sh"
arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
binary="${HCORRAL_TEST_BINARY:-$root/dist/bin/hcorral-linux-$arch}"
[[ "$(uname -s)" == Linux && -x "$binary" ]]
baseline=18fb3944f9b04758b920b1e9415fd017eff8cf51
test_root="$(mktemp -d /tmp/hcorral-mixed.XXXXXX)"
baseline_root="$test_root/baseline"
old_repo="hcorral-compat-old-$$"
new_repo="hcorral-compat-new-$$"
old_image="$old_repo:fixture"
new_image="$new_repo:fixture"
cleanup() {
  docker image rm "$old_image" "$new_image" >/dev/null 2>&1 || true
  git -C "$root" worktree remove --force "$baseline_root" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
step() {
  local before=$SECONDS name="$1"
  shift
  "$@"
  printf 'PASS: %s in %ss\n' "$name" "$((SECONDS-before))"
}

# Compatibility owns fixture preparation, not the image publication canaries.
# Build the exact recipes with pinned upstream bytes and real provenance; the
# separate native image jobs own entrypoint/home matrices on both architectures.
build_fixture() {
  local source="$1" image="$2" version="$3" checksum
  checksum="$(awk -v version="$version" -v platform="linux-$arch" '$1 == version && $2 == platform {print $4}' "$root/tests/fixtures/codex-releases.tsv")"
  [[ "$checksum" =~ ^[0-9a-f]{64}$ ]]
  docker buildx build --load --platform "linux/$arch" --target codex \
    --file "$source/image/Dockerfile" --tag "$image" \
    --build-arg "HCORRAL_CODEX_VERSION=$version" \
    --build-arg "HCORRAL_CODEX_SHA256_${arch^^}=$checksum" \
    --build-arg HCORRAL_IMAGE_REVISION=1 \
    --build-arg "HCORRAL_SOURCE_REVISION=$(hcorral_source_revision "$source")" \
    --build-arg "HCORRAL_BUILD_INPUT_DIGEST=$(hcorral_build_input_digest "$source")" "$source"
  [[ "$(docker image inspect --format '{{.Architecture}}' "$image")" == "$arch" ]]
  [[ "$(docker run --rm --entrypoint codex "$image" --version)" == "codex-cli $version" ]]
}

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
step historical-image build_fixture "$baseline_root" "$old_image" 0.160.0
step current-image build_fixture "$root" "$new_image" 0.160.1
[[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$old_image")" == "$baseline" ]]
docker run --rm --entrypoint /bin/bash "$old_image" -c 'test ! -e /etc/hcorral/bashrc'
docker run --rm --entrypoint /bin/bash "$new_image" -c 'test -r /etc/hcorral/bashrc'


export HCORRAL_TEST_OLD_BINARY="$old_binary" HCORRAL_TEST_OLD_IMAGE="$old_image"
export HCORRAL_TEST_NEW_IMAGE="$new_image"
step shared-home env HCORRAL_TEST_SHARED_HOME_ONLY=1 "$root/tests/image/runtime-home.sh" "$new_image" "$old_image"
step launcher-image "$root/tests/qualification/mixed-versions.sh"
step version-probe "$root/tests/qualification/version-probe.sh" "$new_image"
step hosted-gui "$root/tests/qualification/hosted-gui.sh" "$new_image"
step mycodex-transition "$root/tests/qualification/mycodex-transition.sh" "$new_image"
