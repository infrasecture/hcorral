#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${root}"
fail(){ echo "FAIL: $*" >&2; exit 1; }

build_help="$(./build.sh --help)"
grep -Fq -- '--release' <<<"${build_help}" || fail 'build help lacks release mode'
grep -Fq -- '--packages' <<<"${build_help}" || fail 'build help lacks packages'
if grep -Eq 'docker (push|buildx imagetools create)' build.sh; then fail 'build.sh contains publication'; fi
grep -Fq 'ai.infrasecture.hcorral.build-cache' build.sh || fail 'build cache ownership label is missing'
grep -Fq 'lacks exact hcorral ownership labels' build.sh || fail 'build cache collision refusal is missing'
grep -Fq 'buildhost: hcorral-build' build.sh || fail 'fixed RPM build host is missing'
grep -Fq '/src/THIRD_PARTY_LICENSES.md=THIRD_PARTY_LICENSES.md' build.sh || fail 'raw archives omit the dependency-license inventory'
if grep -Fq 'find dist -maxdepth 1' build.sh release.sh; then fail 'release artifacts are discovered from stale dist contents'; fi
# shellcheck disable=SC2016 # Match the literal release-script expansion.
grep -Fq 'dist/hcorral-${package_version}-1-aarch64.pkg.tar.zst' release.sh || fail 'explicit release artifact inventory is incomplete'

release_help="$(./release.sh --help)"
grep -Fq -- '--prepare-only' <<<"${release_help}" || fail 'release help lacks prepare-only'
grep -Fq -- '--publish-prepared' <<<"${release_help}" || fail 'release help lacks publish-prepared'
grep -Fq 'AGPL-3.0-or-later' release.sh || fail 'formula license missing'
grep -Fq 'bin.install "hcorral"' release.sh || fail 'formula install contract missing'
grep -Fq 'publish-prepared' release.sh || fail 'publish phase missing'
grep -Fq 'verify_qualifications' release.sh || fail 'publish phase does not enforce qualification records'
grep -Fq 'publication-ledger.tsv' release.sh || fail 'publication ledger is missing'
grep -Fq 'reconcile_existing_release' release.sh || fail 'partial release recovery is missing'
grep -Fq 'existing pointer-bump child references a different formula' release.sh || fail 'tap retry identity check is missing'
grep -Fq 'actions/artifacts/' release.sh || fail 'Actions transport identity is not verified remotely'
grep -Fq 'object_type}" == tag' release.sh || fail 'existing release tag type is not verified'
# shellcheck disable=SC2016 # Match literal workflow expressions.
grep -Fq 'HCORRAL_REPOSITORY_TOKEN: ${{ secrets.HCORRAL_REPOSITORY_TOKEN }}' .github/workflows/release.yaml || fail 'protected repository publication credential is missing'
# shellcheck disable=SC2016 # Match the literal workflow-shell expansion.
grep -Fq 'git remote set-url origin "https://x-access-token:${HCORRAL_REPOSITORY_TOKEN}@github.com/infrasecture/hcorral.git"' .github/workflows/release.yaml || fail 'repository publication does not use its protected credential'
grep -Fq 'brew audit --strict dist/Formula/hcorral.rb' .github/workflows/release.yaml || fail 'prepublication formula audit is missing'
grep -Fq 'colima start' .github/workflows/release.yaml || fail 'macOS headless Colima qualification is missing'
if grep -Fq 'docker-desktop' .github/workflows/release.yaml release.sh; then fail 'Docker Desktop remains a release target'; fi
grep -Fq 'artifact}" != dist/Formula/hcorral.rb' release.sh || fail 'release checksums do not exclude the tap-only formula'
grep -Fq "grep -Fxq 'arch = aarch64'" .github/workflows/release.yaml || fail 'arm64 Arch package metadata qualification is missing'
grep -Fq 'pacman -U --noconfirm' .github/workflows/release.yaml || fail 'amd64 Arch package installation qualification is missing'
# shellcheck disable=SC2016 # Match the literal workflow-shell expansion.
grep -Fq 'HCORRAL_TEST_BINARY="${package_root}/deb/usr/bin/hcorral"' .github/workflows/release.yaml || fail 'Linux lifecycle does not exercise packaged bytes'

qualification_dir="$(mktemp -d "${TMPDIR:-/tmp}/hcorral-qualification.XXXXXX")"
trap 'rm -rf -- "${qualification_dir}"' EXIT
qualification_file="${qualification_dir}/linux-amd64.env"
scripts/release-qualification.sh \
  --version v1.2.3 \
  --source-commit "$(printf 'a%.0s' {1..40})" \
  --artifacts-sha256 "$(printf 'b%.0s' {1..64})" \
  --gate linux-amd64 \
  --status passed \
  --output "${qualification_file}"
grep -Fxq 'GATE=linux-amd64' "${qualification_file}" || fail 'qualification gate not recorded'
grep -Fxq 'STATUS=passed' "${qualification_file}" || fail 'qualification status not recorded'
if scripts/release-qualification.sh \
  --version v1.2.3 \
  --source-commit "$(printf 'a%.0s' {1..40})" \
  --artifacts-sha256 "$(printf 'b%.0s' {1..64})" \
  --gate linux-amd64 \
  --status unqualified \
  --output "${qualification_dir}/invalid.env" >/dev/null 2>&1; then
  fail 'invalid qualification status accepted'
fi

# Exercise partial GitHub asset recovery against an argv-compatible fake. The
# publication helper must upload only missing bytes and reject any conflict.
(
  source ./release.sh --version v1.2.3 --publish-prepared
  fake_root="${qualification_dir}/fake-release"
  local_assets="${fake_root}/local"
  remote_assets="${fake_root}/remote"
  mkdir -p "${local_assets}" "${remote_assets}"
  printf alpha >"${local_assets}/alpha.tar.gz"
  printf beta >"${local_assets}/beta.tar.gz"
  cp "${local_assets}/alpha.tar.gz" "${remote_assets}/alpha.tar.gz"
  release_assets=("${local_assets}/alpha.tar.gz" "${local_assets}/beta.tar.gz")
  gh() {
    if [[ "$1 $2" == "release view" && "$*" == *isPrerelease* ]]; then
      printf 'false\n'
    elif [[ "$1 $2" == "release view" ]]; then
      find "${remote_assets}" -maxdepth 1 -type f -printf '%f\n' | LC_ALL=C sort
    elif [[ "$1 $2" == "release download" ]]; then
      local destination="${!#}"
      cp "${remote_assets}"/* "${destination}/"
    elif [[ "$1 $2" == "release upload" ]]; then
      shift 3
      local artifact
      for artifact in "$@"; do cp "${artifact}" "${remote_assets}/"; done
    else
      return 99
    fi
  }
  reconcile_existing_release
  cmp -s "${local_assets}/beta.tar.gz" "${remote_assets}/beta.tar.gz" || fail 'partial release was not repaired'
  printf conflict >"${remote_assets}/alpha.tar.gz"
  if reconcile_existing_release >/dev/null 2>&1; then fail 'conflicting published asset was accepted'; fi
  cp "${local_assets}/alpha.tar.gz" "${remote_assets}/alpha.tar.gz"
  printf extra >"${remote_assets}/unexpected.txt"
  if reconcile_existing_release >/dev/null 2>&1; then fail 'unexpected published asset was accepted'; fi
)

(
  source ./release.sh --version v1.2.3 --publish-prepared
  tag_repo="${qualification_dir}/tag-repo"
  mkdir -p "${tag_repo}"
  git -C "${tag_repo}" init -q
  git -C "${tag_repo}" config user.name hcorral-test
  git -C "${tag_repo}" config user.email hcorral@example.invalid
  printf source >"${tag_repo}/source"
  git -C "${tag_repo}" add source
  git -C "${tag_repo}" commit -qm source
  cd "${tag_repo}"
  source_commit="$(git rev-parse HEAD)"
  git tag v1.2.3
  if verify_release_tag >/dev/null 2>&1; then fail 'lightweight release tag was accepted'; fi
  git tag -d v1.2.3 >/dev/null
  git tag -a v1.2.3 -m 'Harness Corral v1.2.3'
  verify_release_tag || fail 'matching annotated release tag was rejected'
)

(
  source ./release.sh --version v1.2.3 --publish-prepared
  retry_repo="${qualification_dir}/retry-repo"
  mkdir -p "${retry_repo}"
  git -C "${retry_repo}" init -q
  git -C "${retry_repo}" config user.name hcorral-test
  git -C "${retry_repo}" config user.email hcorral@example.invalid
  printf source >"${retry_repo}/source"
  git -C "${retry_repo}" add source
  git -C "${retry_repo}" commit -qm source
  source_commit="$(git -C "${retry_repo}" rev-parse HEAD)"
  printf tap >"${retry_repo}/homebrew-tap"
  git -C "${retry_repo}" add homebrew-tap
  git -C "${retry_repo}" commit -qm 'chore(submodule): bump homebrew-tap after v1.2.3 release'
  cd "${retry_repo}"
  is_pointer_bump_child HEAD || fail 'exact generated pointer-bump child was rejected'
  printf unrelated >unrelated
  git add unrelated
  git commit -qm 'unrelated change'
  if is_pointer_bump_child HEAD; then fail 'arbitrary descendant was accepted as pointer-bump child'; fi
)

(
  source ./release.sh --version v1.2.3 --prepare-only
  prepare() { :; }
  publish() { fail 'prepare-only dispatched publication'; }
  run_selected_phases || fail 'successful prepare-only dispatch returned nonzero'
)

while IFS= read -r use; do
  [[ "${use}" =~ @[0-9a-f]{40}$ ]] || fail "workflow action is not pinned to a full commit: ${use}"
done < <(sed -nE 's/^[[:space:]]*- uses: ([^#[:space:]]+).*$/\1/p' .github/workflows/*.yaml)

set +e
output="$(./release.sh --version v1.0 --prepare-only 2>&1)"; status=$?
set -e
[[ ${status} -ne 0 ]] || fail 'invalid version accepted'
grep -Fq 'canonical v-prefixed SemVer' <<<"${output}" || fail 'invalid version error unclear'

echo 'PASS: build and release command contracts'
