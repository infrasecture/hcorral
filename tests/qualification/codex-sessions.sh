#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
platform="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
test_binary="${HCORRAL_CODEX_TEST_BINARY:-${repo_root}/dist/tests/session-core-${platform}}"
[[ -x "$test_binary" ]] || { echo "missing native session test executable: $test_binary" >&2; exit 2; }
umask 077
test_root="$(mktemp -d "${TMPDIR:-/tmp}/hcorral-native-codex.XXXXXX")"
download=""
cleanup() {
  [[ -z "$download" ]] || rm -f -- "$download"
  rm -rf -- "$test_root"
}
trap cleanup EXIT
# Native core qualification precedes Colima. Reuse its verified public assets
# later in the same job, without needing another network lookup after VM setup.
# Local runs remain temporary unless a cache directory is explicitly selected.
cache_root="${HCORRAL_CODEX_TEST_CACHE:-${RUNNER_TEMP:-$test_root}/hcorral-codex-test-assets}"
mkdir -p "$cache_root"

verify_archive() {
  local checksum="$1" file="$2"
  if [[ "$platform" == darwin-* ]]; then
    # macOS's sha256sum compatibility command lacks the GNU stdin interface.
    printf '%s  %s\n' "$checksum" "$file" | shasum -a 256 -c
  else
    printf '%s  %s\n' "$checksum" "$file" | sha256sum -c
  fi
}

binaries=()
for version in 0.160.0 0.160.1; do
  entry="$(awk -v version="$version" -v platform="$platform" '$1 == version && $2 == platform {print $3, $4}' "$repo_root/tests/fixtures/codex-releases.tsv")"
  [[ -n "$entry" && "$entry" != *$'\n'* ]] || {
    echo "missing or duplicate pinned Codex asset for $version $platform" >&2
    exit 2
  }
  read -r archive checksum <<<"$entry"
  [[ "$archive" == codex-*.tar.gz && "$checksum" =~ ^[0-9a-f]{64}$ ]] || {
    echo "no unambiguous pinned Codex asset for $version $platform" >&2
    exit 2
  }
  directory="$test_root/$version"
  mkdir -p "$directory/home/.codex"
  cached_archive="$cache_root/$checksum-$archive"
  if [[ ! -f "$cached_archive" ]]; then
    download="$(mktemp "$cache_root/.download.XXXXXX")"
    curl --fail --silent --show-error --location --retry 3 --max-time 120 \
      --output "$download" \
      "https://github.com/openai/codex/releases/download/rust-v${version}/${archive}"
    verify_archive "$checksum" "$download"
    mv -- "$download" "$cached_archive"
    download=""
  fi
  verify_archive "$checksum" "$cached_archive"
  tar -xzf "$cached_archive" -C "$directory" "${archive%.tar.gz}"
  binary="$directory/${archive%.tar.gz}"
  chmod 0755 "$binary"
  [[ "$(HOME="$directory/home" CODEX_HOME="$directory/home/.codex" "$binary" --version)" == "codex-cli $version" ]]
  binaries+=("$binary")
done

# Every process uses synthetic homes and an in-process loopback provider. The
# tests exercise native indexing/resume and a completed-turn return transfer;
# neither the user's credentials nor an external model service are involved.
test_args=(-test.v -test.timeout=10m)
if [[ "${HCORRAL_NATIVE_DOCKER:-}" == 1 ]]; then
  [[ -n "${HCORRAL_SESSION_TEST_IMAGE:-}" && -x "${HCORRAL_TEST_BINARY:-}" ]] || {
    echo 'public native qualification requires a launcher and Docker fixture image' >&2
    exit 2
  }
  test_args=(-test.v -test.timeout=20m -test.run '^TestCodexResumesNativeHistory$')
fi
for index in 0 1; do
  peer=$((1 - index))
  printf 'Native Codex qualification: %s -> %s (%s)\n' "${binaries[$index]}" "${binaries[$peer]}" "$platform"
  HCORRAL_TEST_CODEX="${binaries[$index]}" HCORRAL_TEST_CODEX_PEER="${binaries[$peer]}" \
    "$test_binary" "${test_args[@]}"
done
