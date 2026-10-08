#!/usr/bin/env bash
# Cross-platform artifact checks only; no claim of native macOS qualification.
set -euo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
version="${1:?usage: darwin-artifacts.sh vX.Y.Z}"
[[ "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT
formula=dist/Formula/hcorral.rb
for arch in amd64 arm64; do
  archive="dist/hcorral_${version#v}_darwin_${arch}.tar.gz"
  mkdir "$scratch/$arch"
  tar -xzf "$archive" -C "$scratch/$arch"
  cmp "$scratch/$arch/hcorral" "dist/bin/hcorral-darwin-$arch"
  machine=x86_64
  [[ "$arch" != arm64 ]] || machine=arm64
  file "$scratch/$arch/hcorral" | grep -F "Mach-O 64-bit $machine executable"
  for license in LICENSE THIRD_PARTY_LICENSES.md THIRD_PARTY_GO_LICENSES.txt; do
    cmp "$scratch/$arch/$license" "$license"
  done
  grep -Fq "releases/download/$version/${archive#dist/}" "$formula"
  checksum="$(sha256sum "$archive")"
  grep -Fq "sha256 \"${checksum%% *}\"" "$formula"
done
printf 'PASS: Darwin archives and formula match the inspected build; macOS runtime is untested.\n'
