#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

builder_image="${HCORRAL_GOLANG_IMAGE:-golang:1.25.13-alpine@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946}"
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
gomod_cache_volume=hcorral-build-gomod-v1
gobuild_cache_volume=hcorral-build-gocache-v1
mkdir -p dist/bin dist/package-config
artifacts=()

ensure_build_cache() {
  local name="$1" kind="$2" actual
  if ! docker volume inspect "${name}" >/dev/null 2>&1; then
    docker volume create \
      --label "ai.infrasecture.hcorral.build-cache=${kind}" \
      --label ai.infrasecture.hcorral.runtime-schema=1 \
      "${name}" >/dev/null
  fi
  actual="$(docker volume inspect --format '{{index .Labels "ai.infrasecture.hcorral.build-cache"}}|{{index .Labels "ai.infrasecture.hcorral.runtime-schema"}}' "${name}")"
  [[ "${actual}" == "${kind}|1" ]] || { echo "ERROR: build cache volume ${name} lacks exact hcorral ownership labels" >&2; exit 1; }
}

ensure_build_cache "${gomod_cache_volume}" gomod
ensure_build_cache "${gobuild_cache_volume}" gobuild
docker run --rm --user root \
  --volume "${gomod_cache_volume}:/go/pkg/mod" \
  --volume "${gobuild_cache_volume}:/tmp/go-build" \
  "${builder_image}" sh -c 'chown -R "$1:$2" /go/pkg/mod /tmp/go-build' sh "$(id -u)" "$(id -g)"

for target in ${targets}; do
  os="${target%/*}"; arch="${target#*/}"
  output="dist/bin/hcorral-${os}-${arch}"
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    --env HOME=/tmp \
    --env GOWORK=off \
	--env GOMODCACHE=/go/pkg/mod \
	--env GOCACHE=/tmp/go-build \
    --env CGO_ENABLED=0 \
    --env GOOS="${os}" \
    --env GOARCH="${arch}" \
    --volume "${script_dir}:/src" \
    --volume "${gomod_cache_volume}:/go/pkg/mod" \
    --volume "${gobuild_cache_volume}:/tmp/go-build" \
    --workdir /src \
    "${builder_image}" \
    go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/infrasecture/hcorral/internal/app.Version=${cli_version} -X github.com/infrasecture/hcorral/internal/app.Commit=${commit}" -o "/src/${output}" ./cmd/hcorral
  chmod 0755 "${output}"
  archive="dist/hcorral_${pkg_version}_${os}_${arch}.tar.gz"
  docker run --rm --user "$(id -u):$(id -g)" --env HOME=/tmp --env GOWORK=off --env GOMODCACHE=/go/pkg/mod --env GOCACHE=/tmp/go-build --network=none \
    --volume "${script_dir}:/src" --volume "${gomod_cache_volume}:/go/pkg/mod" --volume "${gobuild_cache_volume}:/tmp/go-build" --workdir /src "${builder_image}" \
    go run ./cmd/hcorral-pack archive -output "/src/${archive}" -mtime "${source_date_epoch}" -file "/src/${output}=hcorral" -file /src/LICENSE=LICENSE -file /src/README.md=README.md -file /src/THIRD_PARTY_LICENSES.md=THIRD_PARTY_LICENSES.md
  artifacts+=("${archive}")
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
rpm:
  buildhost: hcorral-build
contents:
  - src: /src/${binary}
    dst: /usr/bin/hcorral
    file_info:
      mode: 0755
  - src: /src/LICENSE
    dst: /usr/share/doc/hcorral/LICENSE
  - src: /src/README.md
    dst: /usr/share/doc/hcorral/README.md
  - src: /src/THIRD_PARTY_LICENSES.md
    dst: /usr/share/doc/hcorral/THIRD_PARTY_LICENSES.md
EOF
    rpm_arch="${arch}"; arch_arch="${arch}"
    [[ "${arch}" == amd64 ]] && { rpm_arch=x86_64; arch_arch=x86_64; }
    [[ "${arch}" == arm64 ]] && { rpm_arch=aarch64; arch_arch=aarch64; }
    docker run --rm --user "$(id -u):$(id -g)" --env "SOURCE_DATE_EPOCH=${source_date_epoch}" --volume "${script_dir}:/src" --workdir /src "${nfpm_image}" package --config "/src/${config}" --packager deb --target "/src/dist/hcorral_${pkg_version}_linux_${arch}.deb"
    docker run --rm --user "$(id -u):$(id -g)" --env "SOURCE_DATE_EPOCH=${source_date_epoch}" --volume "${script_dir}:/src" --workdir /src "${nfpm_image}" package --config "/src/${config}" --packager rpm --target "/src/dist/hcorral-${pkg_version}-1.${rpm_arch}.rpm"
    docker run --rm --user "$(id -u):$(id -g)" --env "SOURCE_DATE_EPOCH=${source_date_epoch}" --volume "${script_dir}:/src" --workdir /src "${nfpm_image}" package --config "/src/${config}" --packager archlinux --target "/src/dist/hcorral-${pkg_version}-1-${arch_arch}.pkg.tar.zst"
    artifacts+=(
      "dist/hcorral_${pkg_version}_linux_${arch}.deb"
      "dist/hcorral-${pkg_version}-1.${rpm_arch}.rpm"
      "dist/hcorral-${pkg_version}-1-${arch_arch}.pkg.tar.zst"
    )
  done
fi

mapfile -t artifacts < <(printf '%s\n' "${artifacts[@]}" | LC_ALL=C sort)
docker run --rm --user "$(id -u):$(id -g)" --env HOME=/tmp --env GOWORK=off --env GOMODCACHE=/go/pkg/mod --env GOCACHE=/tmp/go-build --network=none \
  --volume "${script_dir}:/src" --volume "${gomod_cache_volume}:/go/pkg/mod" --volume "${gobuild_cache_volume}:/tmp/go-build" --workdir /src "${builder_image}" \
  go run ./cmd/hcorral-pack manifest -output /src/dist/component-manifest.json -version "${cli_version}" -commit "${commit}" "${artifacts[@]/#//src/}"
artifacts+=(dist/component-manifest.json)
docker run --rm --user "$(id -u):$(id -g)" --env HOME=/tmp --env GOWORK=off --env GOMODCACHE=/go/pkg/mod --env GOCACHE=/tmp/go-build --network=none \
  --volume "${script_dir}:/src" --volume "${gomod_cache_volume}:/go/pkg/mod" --volume "${gobuild_cache_volume}:/tmp/go-build" --workdir /src "${builder_image}" \
  go run ./cmd/hcorral-pack checksums -output /src/dist/SHA256SUMS "${artifacts[@]/#//src/}"

printf 'Built hcorral %s for %s\n' "${cli_version}" "${targets}"
