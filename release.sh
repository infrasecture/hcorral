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
Local use only. GitHub Actions uses scripts/actions-release.sh.

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

# release.sh is the local maintainer entry point. Actions uses its own entry
# point with repository-scoped GitHub credentials and no cross-repository push.
# shellcheck source=scripts/lib/release-state.sh
source "${script_dir}/scripts/lib/release-state.sh"
init_release_state

publish() {
	require_command gh
	[[ -n "${GH_TOKEN:-}" ]] || { echo 'ERROR: GH_TOKEN is required for publication' >&2; exit 1; }
	[[ -n "${HCORRAL_TAP_TOKEN:-}" ]] || { echo 'ERROR: HCORRAL_TAP_TOKEN is required for publication' >&2; exit 1; }
  require_clean_source
  load_and_verify
  verify_qualifications
  verify_actions_transport
  git fetch origin main --tags
  remote_main="$(git rev-parse origin/main)"
  if [[ "${remote_main}" != "${source_commit}" ]] && ! is_pointer_bump_child "${remote_main}"; then
    echo 'ERROR: origin/main is neither the release source nor its exact generated pointer-bump child' >&2
		exit 1
  fi
	[[ "${current_commit}" == "${remote_main}" ]] || { echo 'ERROR: publish checkout must equal the verified origin/main release source or pointer-bump child' >&2; exit 1; }
  if git rev-parse -q --verify "refs/tags/${version}" >/dev/null; then verify_release_tag || exit 1
  else git tag -a "${version}" "${source_commit}" -m "Harness Corral ${version}"; git push origin "refs/tags/${version}"; fi
	record_publication tag "${version}@${source_commit}"

  mapfile -t release_assets < <(awk -F$'\t' '$1 !~ /Formula\/hcorral.rb$/ { print $1 }' "${artifacts_file}")
  if gh release view "${version}" >/dev/null 2>&1; then
		echo "GitHub release ${version} already exists; reconciling and verifying every asset byte."
		verify_release_channel
		reconcile_existing_release
  else
    release_args=(release create "${version}" --verify-tag --title "Harness Corral ${version}")
    [[ "${channel}" == preview ]] && release_args+=(--prerelease)
    release_args+=(--generate-notes "${release_assets[@]}")
		gh "${release_args[@]}"
		verify_existing_release
  fi
	record_publication github-release "$(gh release view "${version}" --json url --jq .url)"

  git -C homebrew-tap fetch origin main
	if is_pointer_bump_child "${remote_main}"; then
		tap_commit="$(git ls-tree "${remote_main}" homebrew-tap | awk '{print $3}')"
		git -C homebrew-tap checkout --detach "${tap_commit}"
		cmp -s dist/Formula/hcorral.rb homebrew-tap/Formula/hcorral.rb || { echo 'ERROR: existing pointer-bump child references a different formula' >&2; exit 1; }
	else
		git -C homebrew-tap checkout --detach origin/main
		cp dist/Formula/hcorral.rb homebrew-tap/Formula/hcorral.rb
		if ! git -C homebrew-tap diff --quiet -- Formula/hcorral.rb || [[ -n "$(git -C homebrew-tap status --porcelain -- Formula/hcorral.rb)" ]]; then
			git -C homebrew-tap add Formula/hcorral.rb
			git -C homebrew-tap commit -m "hcorral ${version}"
			git -C homebrew-tap push origin HEAD:main
			tap_commit="$(git -C homebrew-tap rev-parse HEAD)"
		else
			tap_commit="$(git -C homebrew-tap log -1 --format=%H -- Formula/hcorral.rb)"
			[[ -n "${tap_commit}" ]] || { echo 'ERROR: matching tap formula has no commit' >&2; exit 1; }
			git -C homebrew-tap checkout --detach "${tap_commit}"
		fi
	fi
	record_publication homebrew-tap "${tap_commit}"
  git fetch origin main
  remote_main="$(git rev-parse origin/main)"
  if [[ "${remote_main}" == "${source_commit}" ]]; then
    git add homebrew-tap
    git commit -m "chore(submodule): bump homebrew-tap after ${version} release"
    git push origin HEAD:main
	elif is_pointer_bump_child "${remote_main}"; then
    remote_tap_commit="$(git ls-tree "${remote_main}" homebrew-tap | awk '{print $3}')"
    [[ "${remote_tap_commit}" == "${tap_commit}" ]] || { echo 'ERROR: existing pointer-bump child references a different tap commit' >&2; exit 1; }
    echo "Repository pointer-bump commit ${remote_main} already exists."
  else
    echo 'ERROR: origin/main changed during publication' >&2
    exit 1
  fi
	record_publication repository "$(git rev-parse origin/main)"
	record_publication complete "${version}"
  echo "Published ${version}; formula commit $(git -C homebrew-tap rev-parse HEAD); repository commit $(git rev-parse HEAD)."
}

run_selected_phases() {
	if [[ "${do_prepare}" == true ]]; then
		prepare
	fi
	if [[ "${do_publish}" == true ]]; then
		publish
	fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  [[ "${GITHUB_ACTIONS:-false}" != true ]] || { echo "ERROR: release.sh is for local use only" >&2; exit 1; }
	run_selected_phases
fi
