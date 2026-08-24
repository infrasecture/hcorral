#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

builder_image="${HCORRAL_GOLANG_IMAGE:-golang:1.25.12-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587}"
nfpm_image="${HCORRAL_NFPM_IMAGE:-ghcr.io/goreleaser/nfpm:v2.47.0@sha256:a662cb167d7b6d3a83920c83d76b12d02b8ac5dd2c13e5c62c15270b23f6df0c}"
release=false
packages=false
cli_version=""

usage() {
  cat <<'EOF'
Usage: ./build.sh [--release --cli-version vX.Y.Z [--packages]]

Without --release, build the native hcorral binary. Release mode builds static
Linux and Darwin amd64/arm64 archives. --packages additionally creates deb,
rpm, and Arch Linux packages for both Linux architectures. Nothing is published.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --release) release=true; shift ;;
    --packages) packages=true; shift ;;
    --cli-version) [[ $# -ge 2 ]] || { echo 'ERROR: --cli-version requires a value' >&2; exit 2; }; cli_version="$2"; shift 2 ;;
    --cli-version=*) cli_version="${1#*=}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "ERROR: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ "${packages}" == true && "${release}" != true ]]; then
  echo 'ERROR: --packages requires --release' >&2
  exit 2
fi
if [[ "${release}" == true ]]; then
  [[ "${cli_version}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'ERROR: release --cli-version must be canonical vX.Y.Z' >&2; exit 2; }
  targets="${HCORRAL_CLI_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"
else
  host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  host_arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
  targets="${HCORRAL_CLI_TARGETS:-${host_os}/${host_arch}}"
  if [[ -z "${cli_version}" ]]; then cli_version="$(git describe --tags --always --dirty 2>/dev/null || printf devel)"; fi
fi
for target in ${targets}; do [[ "${target}" =~ ^(linux|darwin)/(amd64|arm64)$ ]] || { echo "ERROR: unsupported target ${target}" >&2; exit 2; }; done

commit="$(git rev-parse HEAD 2>/dev/null || printf unknown)"
source_date_epoch="${SOURCE_DATE_EPOCH:-315532800}"
pkg_version="${cli_version#v}"
mkdir -p dist/bin dist/package-config

docker volume create hcorral-gomodcache >/dev/null
docker volume create hcorral-gobuildcache >/dev/null

for target in ${targets}; do
  os="${target%/*}"; arch="${target#*/}"
  output="dist/bin/hcorral-${os}-${arch}"
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    --env HOME=/tmp \
    --env GOWORK=off \
    --env CGO_ENABLED=0 \
    --env GOOS="${os}" \
    --env GOARCH="${arch}" \
    --volume "${script_dir}:/src" \
    --volume hcorral-gomodcache:/go/pkg/mod \
    --volume hcorral-gobuildcache:/tmp/go-build \
    --workdir /src \
    "${builder_image}" \
    go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/infrasecture/hcorral/internal/app.Version=${cli_version} -X github.com/infrasecture/hcorral/internal/app.Commit=${commit}" -o "/src/${output}" ./cmd/hcorral
  chmod 0755 "${output}"
  archive="dist/hcorral_${pkg_version}_${os}_${arch}.tar.gz"
  docker run --rm --user "$(id -u):$(id -g)" --env HOME=/tmp --env GOWORK=off --network=none \
    --volume "${script_dir}:/src" --volume hcorral-gomodcache:/go/pkg/mod --volume hcorral-gobuildcache:/tmp/go-build --workdir /src "${builder_image}" \
    go run ./cmd/hcorral-pack archive -output "/src/${archive}" -mtime "${source_date_epoch}" -file "/src/${output}=hcorral" -file /src/LICENSE=LICENSE -file /src/README.md=README.md
done

if [[ "${packages}" == true ]]; then
  for arch in amd64 arm64; do
    binary="dist/bin/hcorral-linux-${arch}"
    config="dist/package-config/hcorral-${arch}.yaml"
    cat >"${config}" <<EOF
name: hcorral
arch: ${arch}
platform: linux
version: ${pkg_version}
release: "1"
section: default
priority: optional
maintainer: infrasecture
description: Harness Corral persistent AI workstation launcher
vendor: infrasecture
homepage: https://github.com/infrasecture/hcorral
license: AGPL-3.0-or-later
contents:
  - src: /src/${binary}
    dst: /usr/bin/hcorral
    file_info:
      mode: 0755
  - src: /src/LICENSE
    dst: /usr/share/doc/hcorral/LICENSE
EOF
    rpm_arch="${arch}"; arch_arch="${arch}"
    [[ "${arch}" == amd64 ]] && { rpm_arch=x86_64; arch_arch=x86_64; }
    [[ "${arch}" == arm64 ]] && { rpm_arch=aarch64; arch_arch=aarch64; }
    docker run --rm --user "$(id -u):$(id -g)" --volume "${script_dir}:/src" --workdir /src "${nfpm_image}" package --config "/src/${config}" --packager deb --target "/src/dist/hcorral_${pkg_version}_linux_${arch}.deb"
    docker run --rm --user "$(id -u):$(id -g)" --volume "${script_dir}:/src" --workdir /src "${nfpm_image}" package --config "/src/${config}" --packager rpm --target "/src/dist/hcorral-${pkg_version}-1.${rpm_arch}.rpm"
    docker run --rm --user "$(id -u):$(id -g)" --volume "${script_dir}:/src" --workdir /src "${nfpm_image}" package --config "/src/${config}" --packager archlinux --target "/src/dist/hcorral-${pkg_version}-1-${arch_arch}.pkg.tar.zst"
  done
fi

mapfile -d '' artifacts < <(find dist -maxdepth 1 -type f \( -name 'hcorral_*.tar.gz' -o -name '*.deb' -o -name '*.rpm' -o -name '*.pkg.tar.zst' \) -print0 | LC_ALL=C sort -z)
docker run --rm --user "$(id -u):$(id -g)" --env HOME=/tmp --env GOWORK=off --network=none \
  --volume "${script_dir}:/src" --volume hcorral-gomodcache:/go/pkg/mod --volume hcorral-gobuildcache:/tmp/go-build --workdir /src "${builder_image}" \
  go run ./cmd/hcorral-pack manifest -output /src/dist/component-manifest.json -version "${cli_version}" -commit "${commit}" "${artifacts[@]/#//src/}"
artifacts+=(dist/component-manifest.json)
docker run --rm --user "$(id -u):$(id -g)" --env HOME=/tmp --env GOWORK=off --network=none \
  --volume "${script_dir}:/src" --volume hcorral-gomodcache:/go/pkg/mod --volume hcorral-gobuildcache:/tmp/go-build --workdir /src "${builder_image}" \
  go run ./cmd/hcorral-pack checksums -output /src/dist/SHA256SUMS "${artifacts[@]/#//src/}"

printf 'Built hcorral %s for %s\n' "${cli_version}" "${targets}"
