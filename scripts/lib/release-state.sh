#!/usr/bin/env bash
# Shared artifact preparation and verification; entry points own publication
# credentials and destinations. Sourcing this file has no side effects.
# shellcheck disable=SC2154 # channel and release_assets are supplied by entry points.
# shellcheck source=scripts/lib/release-versioning.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/release-versioning.sh"

require_command() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: required command not found: $1" >&2; exit 1; }; }

init_release_state() {
  require_command git

  current_commit="$(git rev-parse --verify HEAD)"
  source_commit="${HCORRAL_RELEASE_SOURCE_COMMIT:-${current_commit}}"
  [[ "${source_commit}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] || { echo 'ERROR: invalid recorded release source commit' >&2; exit 2; }
  state_dir="dist/release-state/${version}/${source_commit}"
  state_file="${state_dir}/release.env"
  artifacts_file="${state_dir}/artifacts.txt"
  gates_file="${state_dir}/gates.env"
  ledger_file="${state_dir}/publication-ledger.tsv"
}

require_clean_source() {
  [[ -z "$(git status --porcelain --untracked-files=normal)" ]] || { echo 'ERROR: working tree must be clean' >&2; exit 1; }
}

prepare() {
	require_command docker
  [[ "${source_commit}" == "${current_commit}" ]] || { echo 'ERROR: preparation source must be the checked-out commit' >&2; exit 1; }
  require_clean_source
  [[ "$(git branch --show-current)" == main || "${GITHUB_REF_NAME:-}" == main ]] || { echo 'ERROR: releases must be prepared from main' >&2; exit 1; }
  grep -Fq 'GNU AFFERO GENERAL PUBLIC LICENSE' LICENSE || { echo 'ERROR: AGPL license text missing' >&2; exit 1; }
  scripts/check-provenance.sh
  scripts/check-third-party.sh

  echo '==> source, test, race, fuzz, shell, license, and vulnerability gates'
  scripts/ci-source.sh

  # Runtime qualification consumes these exact packages in the platform jobs.
  # Do not build/test a second unversioned launcher during preparation.
  echo '==> release matrix and packages'
  ./build.sh --release --cli-version "${version}" --packages
  cp LICENSE dist/LICENSE
  cp THIRD_PARTY_LICENSES.md dist/THIRD_PARTY_LICENSES.md
  test -s dist/Formula/hcorral.rb

  package_version="${version#v}"
  artifacts=(
    "dist/hcorral_${package_version}_linux_amd64.tar.gz"
    "dist/hcorral_${package_version}_linux_arm64.tar.gz"
    "dist/hcorral_${package_version}_darwin_amd64.tar.gz"
    "dist/hcorral_${package_version}_darwin_arm64.tar.gz"
    "dist/hcorral_${package_version}_linux_amd64.deb"
    "dist/hcorral_${package_version}_linux_arm64.deb"
    "dist/hcorral-${package_version}-1.x86_64.rpm"
    "dist/hcorral-${package_version}-1.aarch64.rpm"
    "dist/hcorral-${package_version}-1-x86_64.pkg.tar.zst"
    "dist/hcorral-${package_version}-1-aarch64.pkg.tar.zst"
    dist/LICENSE
    dist/THIRD_PARTY_LICENSES.md
    dist/component-manifest.json
  )
  mapfile -t artifacts < <(printf '%s\n' "${artifacts[@]}" | LC_ALL=C sort)
  artifacts+=(dist/Formula/hcorral.rb)
  mkdir -p "${state_dir}"
  : >dist/SHA256SUMS
  : >"${artifacts_file}"
  for artifact in "${artifacts[@]}"; do
    if [[ "${artifact}" != dist/Formula/hcorral.rb ]]; then
      printf '%s  %s\n' "$(hcorral_sha256_file "${artifact}")" "$(basename "${artifact}")" >>dist/SHA256SUMS
    fi
    printf '%s\t%s\n' "${artifact}" "$(hcorral_sha256_file "${artifact}")" >>"${artifacts_file}"
  done
  printf '%s\t%s\n' dist/SHA256SUMS "$(hcorral_sha256_file dist/SHA256SUMS)" >>"${artifacts_file}"
  {
    printf 'FORMAT=1\nVERSION=%s\nCHANNEL=%s\nSOURCE_COMMIT=%s\n' "${version}" "${channel}" "${source_commit}"
    printf 'ARTIFACTS_SHA256=%s\n' "$(hcorral_sha256_file "${artifacts_file}")"
  } >"${state_file}"
  {
    printf 'FORMAT=1\nSOURCE=passed\nUNIT=passed\nRACE=passed\nFUZZ=passed\nSHELL=passed\nREAL_DOCKER=pending-platform-qualification\nPACKAGES=built\nFORMULA_TEMPLATE=passed\n'
    printf 'STATE_SHA256=%s\n' "$(hcorral_sha256_file "${state_file}")"
  } >"${gates_file}"
	{
		printf 'FORMAT\t1\nVERSION\t%s\nSOURCE_COMMIT\t%s\nARTIFACTS_SHA256\t%s\n' "${version}" "${source_commit}" "$(hcorral_sha256_file "${artifacts_file}")"
		printf 'EVENT\t%s\tprepared\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${source_commit}"
	} >"${ledger_file}"
  echo "Prepared ${version} at ${source_commit}; no publication occurred."
}

load_and_verify() {
	[[ -f "${state_file}" && -f "${artifacts_file}" && -f "${gates_file}" && -f "${ledger_file}" ]] || { echo 'ERROR: prepared release state is missing' >&2; exit 1; }
  [[ "$(hcorral_state_value "${state_file}" VERSION)" == "${version}" ]]
  [[ "$(hcorral_state_value "${state_file}" CHANNEL)" == "${channel}" ]] || { echo "ERROR: prepared release channel differs" >&2; exit 1; }
  [[ "$(hcorral_state_value "${state_file}" SOURCE_COMMIT)" == "${source_commit}" ]]
  [[ "$(hcorral_state_value "${state_file}" ARTIFACTS_SHA256)" == "$(hcorral_sha256_file "${artifacts_file}")" ]] || { echo 'ERROR: prepared artifact inventory changed' >&2; exit 1; }
	[[ "$(hcorral_state_value "${gates_file}" STATE_SHA256)" == "$(hcorral_sha256_file "${state_file}")" ]] || { echo 'ERROR: prepared gate state changed' >&2; exit 1; }
	[[ "$(awk -F '\t' '$1 == "FORMAT" { print $2; exit }' "${ledger_file}")" == 1 ]] || { echo 'ERROR: invalid publication ledger' >&2; exit 1; }
	[[ "$(awk -F '\t' '$1 == "SOURCE_COMMIT" { print $2; exit }' "${ledger_file}")" == "${source_commit}" ]] || { echo 'ERROR: publication ledger source mismatch' >&2; exit 1; }
	[[ "$(awk -F '\t' '$1 == "ARTIFACTS_SHA256" { print $2; exit }' "${ledger_file}")" == "$(hcorral_sha256_file "${artifacts_file}")" ]] || { echo 'ERROR: publication ledger artifact mismatch' >&2; exit 1; }
  while IFS=$'\t' read -r artifact expected; do [[ -f "${artifact}" ]] || { echo "ERROR: missing artifact ${artifact}" >&2; exit 1; }; [[ "$(hcorral_sha256_file "${artifact}")" == "${expected}" ]] || { echo "ERROR: changed artifact ${artifact}" >&2; exit 1; }; done <"${artifacts_file}"
}

verify_qualification() {
  local gate="$1" allowed_status="$2"
  local file="dist/qualification/${gate}.env"
  [[ -f "${file}" ]] || { echo "ERROR: qualification gate is missing: ${gate}" >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" FORMAT)" == 1 ]] || { echo "ERROR: invalid qualification format: ${gate}" >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" VERSION)" == "${version}" ]] || { echo "ERROR: qualification version mismatch: ${gate}" >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" SOURCE_COMMIT)" == "${source_commit}" ]] || { echo "ERROR: qualification source mismatch: ${gate}" >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" ARTIFACTS_SHA256)" == "$(hcorral_state_value "${state_file}" ARTIFACTS_SHA256)" ]] || { echo "ERROR: qualification artifact mismatch: ${gate}" >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" GATE)" == "${gate}" ]] || { echo "ERROR: qualification gate identity mismatch: ${gate}" >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" STATUS)" == "${allowed_status}" ]] || { echo "ERROR: qualification status mismatch: ${gate}" >&2; exit 1; }
}

verify_qualifications() {
  verify_qualification linux-amd64 passed
  verify_qualification linux-arm64 passed
  verify_qualification darwin-amd64 artifact-checked
  verify_qualification darwin-arm64 artifact-checked
	local gui_gate gui_status
	for gui_gate in linux-x11 linux-wayland linux-xwayland; do
		if [[ "${channel}" == stable ]]; then
			verify_qualification "${gui_gate}" passed
		else
			gui_status="$(hcorral_state_value "dist/qualification/${gui_gate}.env" STATUS 2>/dev/null || true)"
			case "${gui_status}" in
				passed) verify_qualification "${gui_gate}" passed ;;
				waived-preview) verify_qualification "${gui_gate}" waived-preview ;;
				*) echo "ERROR: preview ${gui_gate} gate must be passed or explicitly waived" >&2; exit 1 ;;
			esac
		fi
	done
}

verify_actions_transport() {
  [[ "${HCORRAL_REQUIRE_ACTIONS_TRANSPORT:-false}" == true ]] || return 0
  local file=dist/release-transport.env artifact_id artifact_digest artifact_url expires_at expires_epoch now_epoch remote
  [[ -f "${file}" ]] || { echo 'ERROR: Actions artifact transport record is missing' >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" FORMAT)" == 1 ]]
  [[ "$(hcorral_state_value "${file}" VERSION)" == "${version}" ]]
  [[ "$(hcorral_state_value "${file}" SOURCE_COMMIT)" == "${source_commit}" ]]
  artifact_id="$(hcorral_state_value "${file}" ARTIFACT_ID)"
  artifact_digest="$(hcorral_state_value "${file}" ARTIFACT_DIGEST)"
  artifact_url="$(hcorral_state_value "${file}" ARTIFACT_URL)"
  expires_at="$(hcorral_state_value "${file}" EXPIRES_AT)"
  [[ "${artifact_id}" =~ ^[1-9][0-9]*$ ]] || { echo 'ERROR: invalid Actions artifact ID' >&2; exit 1; }
  [[ "${artifact_digest}" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo 'ERROR: invalid Actions artifact digest' >&2; exit 1; }
  [[ "${artifact_url}" == https://github.com/*/actions/runs/*/artifacts/* ]] || { echo 'ERROR: invalid Actions artifact URL' >&2; exit 1; }
  [[ "${expires_at}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] || { echo 'ERROR: invalid Actions artifact expiry' >&2; exit 1; }
  [[ "$(hcorral_state_value "${file}" RETENTION_DAYS)" == 30 ]] || { echo 'ERROR: unexpected Actions artifact retention' >&2; exit 1; }
  expires_epoch="$(date -u -d "${expires_at}" +%s 2>/dev/null)" || { echo 'ERROR: cannot parse Actions artifact expiry' >&2; exit 1; }
  now_epoch="$(date -u +%s)"
  [[ "${expires_epoch}" -gt "${now_epoch}" ]] || { echo 'ERROR: prepared Actions artifact has expired' >&2; exit 1; }
  remote="$(gh api "repos/infrasecture/hcorral/actions/artifacts/${artifact_id}" --jq '[.name,.digest,(.expired|tostring)]|join("|")')" || { echo 'ERROR: cannot verify prepared Actions artifact' >&2; exit 1; }
  [[ "${remote}" == "${version}-${source_commit}|${artifact_digest}|false" ]] || { echo "ERROR: prepared Actions artifact remote identity differs: ${remote}" >&2; exit 1; }
}

is_pointer_bump_child() {
  local commit="$1" parent subject changed
  parent="$(git rev-parse "${commit}^" 2>/dev/null)" || return 1
  [[ "${parent}" == "${source_commit}" ]] || return 1
  subject="$(git show -s --format=%s "${commit}")"
  [[ "${subject}" == "chore(submodule): bump homebrew-tap after ${version} release" ]] || return 1
  changed="$(git diff-tree --no-commit-id --name-only -r "${parent}" "${commit}")"
  [[ "${changed}" == homebrew-tap ]]
}

verify_existing_release() {
  local temporary expected_names actual_names artifact basename expected
  temporary="$(mktemp -d "${TMPDIR:-/tmp}/hcorral-release-verify.XXXXXX")"
  trap 'rm -rf -- "${temporary}"' RETURN
  expected_names="$(printf '%s\n' "${release_assets[@]##*/}" | LC_ALL=C sort)"
  actual_names="$(gh release view "${version}" --json assets --jq '.assets[].name' | LC_ALL=C sort)"
  [[ "${actual_names}" == "${expected_names}" ]] || { echo 'ERROR: existing GitHub release asset names differ' >&2; return 1; }
  gh release download "${version}" --dir "${temporary}"
  for artifact in "${release_assets[@]}"; do
    basename="$(basename -- "${artifact}")"
    expected="$(hcorral_sha256_file "${artifact}")"
    [[ "$(hcorral_sha256_file "${temporary}/${basename}")" == "${expected}" ]] || { echo "ERROR: existing GitHub asset differs: ${basename}" >&2; return 1; }
  done
  trap - RETURN
  rm -rf -- "${temporary}"
}

verify_release_channel() {
	local actual expected=false
	[[ "${channel}" == preview ]] && expected=true
	actual="$(gh release view "${version}" --json isPrerelease --jq .isPrerelease)"
	[[ "${actual}" == "${expected}" ]] || { echo "ERROR: existing GitHub release channel differs: prerelease=${actual}" >&2; return 1; }
}

reconcile_existing_release() {
	local temporary actual_names name artifact
	local -A expected_by_name=() actual=()
	local -a missing=()
	for artifact in "${release_assets[@]}"; do expected_by_name["$(basename -- "${artifact}")"]="${artifact}"; done
	mapfile -t actual_names < <(gh release view "${version}" --json assets --jq '.assets[].name')
	for name in "${actual_names[@]}"; do
		[[ -n "${expected_by_name[${name}]:-}" ]] || { echo "ERROR: existing GitHub release has unexpected asset: ${name}" >&2; return 1; }
		actual["${name}"]=1
	done
	if (( ${#actual_names[@]} > 0 )); then
		temporary="$(mktemp -d "${TMPDIR:-/tmp}/hcorral-release-reconcile.XXXXXX")"
		trap 'rm -rf -- "${temporary}"' RETURN
		gh release download "${version}" --dir "${temporary}"
		for name in "${actual_names[@]}"; do
			[[ "$(hcorral_sha256_file "${temporary}/${name}")" == "$(hcorral_sha256_file "${expected_by_name[${name}]}")" ]] || { echo "ERROR: existing GitHub asset differs: ${name}" >&2; return 1; }
		done
		trap - RETURN
		rm -rf -- "${temporary}"
	fi
	for artifact in "${release_assets[@]}"; do
		name="$(basename -- "${artifact}")"
		[[ -n "${actual[${name}]:-}" ]] || missing+=("${artifact}")
	done
	if (( ${#missing[@]} > 0 )); then
		echo "Completing partial GitHub release with ${#missing[@]} missing asset(s)."
		gh release upload "${version}" "${missing[@]}"
	fi
	verify_existing_release
}

record_publication() {
	local phase="$1" identity="$2"
	printf 'EVENT\t%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${phase}" "${identity}" >>"${ledger_file}"
}

verify_release_tag() {
	local ref="refs/tags/${version}" object_type peeled
	object_type="$(git cat-file -t "${ref}" 2>/dev/null)" || return 1
	[[ "${object_type}" == tag ]] || { echo "ERROR: version tag ${version} exists but is not annotated" >&2; return 1; }
	peeled="$(git rev-parse "${ref}^{}")" || return 1
	[[ "${peeled}" == "${source_commit}" ]] || { echo 'ERROR: version tag conflicts' >&2; return 1; }
}
