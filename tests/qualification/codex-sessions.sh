#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
platform="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
test_binary="${HCORRAL_CODEX_TEST_BINARY:-${repo_root}/dist/tests/session-core-${platform}}"
[[ -x "$test_binary" ]] || { echo "missing native session test executable: $test_binary" >&2; exit 2; }
test_root="$(mktemp -d "${TMPDIR:-/tmp}/hcorral-native-codex.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

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
  mkdir -p "$directory" "$directory/home"
  curl --fail --silent --show-error --location --retry 3 --max-time 120 \
    --output "$directory/$archive" \
    "https://github.com/openai/codex/releases/download/rust-v${version}/${archive}"
  if [[ "$platform" == darwin-* ]]; then
    # The macOS sha256sum compatibility command does not implement the GNU
    # stdin-check interface. Use the system Perl tool explicitly on macOS.
    (cd "$directory" && printf '%s  %s\n' "$checksum" "$archive" | shasum -a 256 -c)
  else
    (cd "$directory" && printf '%s  %s\n' "$checksum" "$archive" | sha256sum -c)
  fi
  tar -xzf "$directory/$archive" -C "$directory" "${archive%.tar.gz}"
  binary="$directory/${archive%.tar.gz}"
  chmod 0755 "$binary"
  [[ "$(HOME="$directory/home" CODEX_HOME="$directory/home/.codex" "$binary" --version)" == "codex-cli $version" ]]
  binaries+=("$binary")
done

# Every process uses synthetic homes and an in-process loopback provider. The
# tests exercise native indexing/resume and a completed-turn return transfer;
# neither the user's credentials nor an external model service are involved.
for index in 0 1; do
  peer=$((1 - index))
  printf 'Native Codex qualification: %s -> %s (%s)\n' "${binaries[$index]}" "${binaries[$peer]}" "$platform"
  HCORRAL_TEST_CODEX="${binaries[$index]}" HCORRAL_TEST_CODEX_PEER="${binaries[$peer]}" \
    "$test_binary" -test.v -test.timeout=10m
done
