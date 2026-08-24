#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"
# shellcheck source=scripts/lib/release-versioning.sh
source "${script_dir}/scripts/lib/release-versioning.sh"

version=""
prepare_only=false
publish_prepared=false
channel=preview

usage() {
  cat <<'EOF'
Usage: ./release.sh --version vX.Y.Z [--prepare-only|--publish-prepared] [--channel preview|stable]

Preparation runs all source, Docker, build, package, checksum, license, and
formula gates without publishing. Publication consumes the exact prepared
bytes and recorded source commit; it never rebuilds.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) [[ $# -ge 2 ]] || { echo 'ERROR: --version requires a value' >&2; exit 2; }; version="$2"; shift 2 ;;
    --version=*) version="${1#*=}"; shift ;;
    --prepare-only) prepare_only=true; shift ;;
    --publish-prepared) publish_prepared=true; shift ;;
    --channel) [[ $# -ge 2 ]] || { echo 'ERROR: --channel requires a value' >&2; exit 2; }; channel="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "ERROR: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

hcorral_require_stable_version "${version}"
[[ "${channel}" == preview || "${channel}" == stable ]] || { echo 'ERROR: channel must be preview or stable' >&2; exit 2; }
[[ "${prepare_only}" != true || "${publish_prepared}" != true ]] || { echo 'ERROR: phase flags are mutually exclusive' >&2; exit 2; }
do_prepare=true; do_publish=true
[[ "${prepare_only}" == true ]] && do_publish=false
[[ "${publish_prepared}" == true ]] && do_prepare=false

require_command() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: required command not found: $1" >&2; exit 1; }; }
require_command git
require_command docker

source_commit="$(git rev-parse --verify HEAD)"
state_dir="dist/release-state/${version}/${source_commit}"
state_file="${state_dir}/release.env"
artifacts_file="${state_dir}/artifacts.txt"
gates_file="${state_dir}/gates.env"

require_clean_source() {
  [[ -z "$(git status --porcelain --untracked-files=normal)" ]] || { echo 'ERROR: working tree must be clean' >&2; exit 1; }
}

write_formula() {
  local amd_sha="$1" arm_sha="$2" output="$3" pkg_version="${version#v}"
  mkdir -p "$(dirname "${output}")"
  cat >"${output}" <<EOF
class Hcorral < Formula
  desc "Persistent AI development workstations in Docker"
  homepage "https://github.com/infrasecture/hcorral"
  version "${pkg_version}"
  license "AGPL-3.0-or-later"

  on_arm do
    url "https://github.com/infrasecture/hcorral/releases/download/${version}/hcorral_${pkg_version}_darwin_arm64.tar.gz"
    sha256 "${arm_sha}"
  end

  on_intel do
    url "https://github.com/infrasecture/hcorral/releases/download/${version}/hcorral_${pkg_version}_darwin_amd64.tar.gz"
    sha256 "${amd_sha}"
  end

  def install
    bin.install "hcorral"
  end

  test do
    output = shell_output("#{bin}/hcorral version")
    assert_match "hcorral ${version}", output
  end
end
EOF
}

prepare() {
  require_clean_source
  [[ "$(git branch --show-current)" == main || "${GITHUB_REF_NAME:-}" == main ]] || { echo 'ERROR: releases must be prepared from main' >&2; exit 1; }
  grep -Fq 'GNU AFFERO GENERAL PUBLIC LICENSE' LICENSE || { echo 'ERROR: AGPL license text missing' >&2; exit 1; }
  grep -Fq "${source_commit}" docs/provenance.md || true
  [[ -f THIRD_PARTY_LICENSES.md ]] || { echo 'ERROR: dependency license inventory missing' >&2; exit 1; }

  echo '==> source, test, race, fuzz, shell, license, and vulnerability gates'
  scripts/ci-source.sh

  echo '==> real Docker lifecycle gate'
  ./build.sh
  tests/integration/real-docker.sh

  echo '==> release matrix and packages'
  ./build.sh --release --cli-version "${version}" --packages
  cp LICENSE dist/LICENSE
  cp THIRD_PARTY_LICENSES.md dist/THIRD_PARTY_LICENSES.md
  mkdir -p dist/Formula
  write_formula \
    "$(hcorral_sha256_file "dist/hcorral_${version#v}_darwin_amd64.tar.gz")" \
    "$(hcorral_sha256_file "dist/hcorral_${version#v}_darwin_arm64.tar.gz")" \
    dist/Formula/hcorral.rb

  mapfile -d '' artifacts < <(find dist -maxdepth 1 -type f \( -name 'hcorral_*.tar.gz' -o -name '*.deb' -o -name '*.rpm' -o -name '*.pkg.tar.zst' -o -name LICENSE -o -name THIRD_PARTY_LICENSES.md -o -name component-manifest.json \) -print0 | LC_ALL=C sort -z)
  artifacts+=(dist/Formula/hcorral.rb)
  mkdir -p "${state_dir}"
  : >dist/SHA256SUMS
  : >"${artifacts_file}"
  for artifact in "${artifacts[@]}"; do
    printf '%s  %s\n' "$(hcorral_sha256_file "${artifact}")" "$(basename "${artifact}")" >>dist/SHA256SUMS
    printf '%s\t%s\n' "${artifact}" "$(hcorral_sha256_file "${artifact}")" >>"${artifacts_file}"
  done
  printf '%s\t%s\n' dist/SHA256SUMS "$(hcorral_sha256_file dist/SHA256SUMS)" >>"${artifacts_file}"
  {
    printf 'FORMAT=1\nVERSION=%s\nCHANNEL=%s\nSOURCE_COMMIT=%s\n' "${version}" "${channel}" "${source_commit}"
    printf 'ARTIFACTS_SHA256=%s\n' "$(hcorral_sha256_file "${artifacts_file}")"
  } >"${state_file}"
  {
    printf 'FORMAT=1\nSOURCE=passed\nUNIT=passed\nRACE=passed\nFUZZ=passed\nSHELL=passed\nREAL_DOCKER=passed\nPACKAGES=passed\nFORMULA_TEMPLATE=passed\n'
    printf 'STATE_SHA256=%s\n' "$(hcorral_sha256_file "${state_file}")"
  } >"${gates_file}"
  echo "Prepared ${version} at ${source_commit}; no publication occurred."
}

load_and_verify() {
  [[ -f "${state_file}" && -f "${artifacts_file}" && -f "${gates_file}" ]] || { echo 'ERROR: prepared release state is missing' >&2; exit 1; }
  [[ "$(hcorral_state_value "${state_file}" VERSION)" == "${version}" ]]
  [[ "$(hcorral_state_value "${state_file}" SOURCE_COMMIT)" == "${source_commit}" ]]
  [[ "$(hcorral_state_value "${state_file}" ARTIFACTS_SHA256)" == "$(hcorral_sha256_file "${artifacts_file}")" ]] || { echo 'ERROR: prepared artifact inventory changed' >&2; exit 1; }
  [[ "$(hcorral_state_value "${gates_file}" STATE_SHA256)" == "$(hcorral_sha256_file "${state_file}")" ]] || { echo 'ERROR: prepared gate state changed' >&2; exit 1; }
  while IFS=$'\t' read -r artifact expected; do [[ -f "${artifact}" ]] || { echo "ERROR: missing artifact ${artifact}" >&2; exit 1; }; [[ "$(hcorral_sha256_file "${artifact}")" == "${expected}" ]] || { echo "ERROR: changed artifact ${artifact}" >&2; exit 1; }; done <"${artifacts_file}"
}

publish() {
  require_command gh
  require_clean_source
  load_and_verify
  git fetch origin main --tags
  git merge-base --is-ancestor "${source_commit}" origin/main || { echo 'ERROR: release commit is not on origin/main' >&2; exit 1; }
  if git rev-parse -q --verify "refs/tags/${version}" >/dev/null; then [[ "$(git rev-list -n1 "${version}")" == "${source_commit}" ]] || { echo 'ERROR: version tag conflicts' >&2; exit 1; }
  else git tag -a "${version}" "${source_commit}" -m "Harness Corral ${version}"; git push origin "refs/tags/${version}"; fi

  mapfile -t release_assets < <(awk -F$'\t' '$1 !~ /Formula\/hcorral.rb$/ { print $1 }' "${artifacts_file}")
  if gh release view "${version}" >/dev/null 2>&1; then
    echo "GitHub release ${version} already exists; verifying rather than replacing it."
  else
    release_args=(release create "${version}" --verify-tag --title "Harness Corral ${version}")
    [[ "${channel}" == preview ]] && release_args+=(--prerelease)
    release_args+=(--generate-notes "${release_assets[@]}")
    gh "${release_args[@]}"
  fi

  cp dist/Formula/hcorral.rb homebrew-tap/Formula/hcorral.rb
  if ! git -C homebrew-tap diff --quiet -- Formula/hcorral.rb || [[ -n "$(git -C homebrew-tap status --porcelain -- Formula/hcorral.rb)" ]]; then
    git -C homebrew-tap add Formula/hcorral.rb
    git -C homebrew-tap commit -m "hcorral ${version}"
    git -C homebrew-tap push origin HEAD:main
  fi
  if ! git diff --quiet -- homebrew-tap; then
    git add homebrew-tap
    git commit -m "chore(submodule): bump homebrew-tap after ${version} release"
    git push origin HEAD:main
  fi
  echo "Published ${version}; formula commit $(git -C homebrew-tap rev-parse HEAD); repository commit $(git rev-parse HEAD)."
}

[[ "${do_prepare}" == true ]] && prepare
[[ "${do_publish}" == true ]] && publish
