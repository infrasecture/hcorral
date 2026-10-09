#!/usr/bin/env bash
set -euo pipefail

# Actions publishes only this repository. Homebrew has its own workflow and
# repository-scoped credential. Never invoke the local release.sh from here.
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/release-state.sh
source "${root}/scripts/lib/release-state.sh"

validate_release_run() {
  jq -er '
    select(.event == "workflow_dispatch" and .head_branch == "main"
      and .path == ".github/workflows/release.yaml"
      and .head_repository.full_name == "infrasecture/hcorral")
    | .head_sha | select(test("^[0-9a-f]{40}$"))' "$1"
}

validate_release_jobs() {
  local jobs="$1" gate
  local gates=(release/prepare release/linux-amd64 release/linux-arm64 release/darwin-artifacts)
  if [[ "$channel" == stable ]]; then gates+=(release/linux-x11 release/linux-wayland release/linux-xwayland); fi
  for gate in "${gates[@]}"; do
    jq -e --arg gate "$gate" '[.[].jobs[] | select(.name == $gate)] | length == 1 and .[0].conclusion == "success"' "$jobs" >/dev/null || {
      echo "ERROR: prepared run lacks successful job: $gate" >&2; return 1;
    }
  done
}

download_prepared_release() {
  local run_id="$1" work metadata artifact_id artifact_digest gate
  [[ "$run_id" =~ ^[1-9][0-9]*$ ]] || { echo 'ERROR: invalid prepared run ID' >&2; return 1; }
  work="$(mktemp -d)"
  trap 'rm -rf -- "$work"' RETURN
  gh api "repos/$GH_REPO/actions/runs/$run_id" >"$work/run.json"
  source_commit="$(validate_release_run "$work/run.json")" || { echo 'ERROR: prepared run is not a main-branch launcher release' >&2; return 1; }
  gh api --paginate --slurp "repos/$GH_REPO/actions/runs/$run_id/jobs?per_page=100" >"$work/jobs.json"
  validate_release_jobs "$work/jobs.json"
  HCORRAL_RELEASE_SOURCE_COMMIT="$source_commit" init_release_state

  gh run download "$run_id" --name "release-transport-$source_commit" --dir "$work/transport"
  artifact_id="$(hcorral_state_value "$work/transport/release-transport.env" ARTIFACT_ID)"
  [[ "$artifact_id" =~ ^[1-9][0-9]*$ ]] || { echo 'ERROR: invalid artifact ID' >&2; return 1; }
  metadata="$(gh api "repos/$GH_REPO/actions/artifacts/$artifact_id")"
  artifact_digest="$(jq -er --argjson run "$run_id" --arg name "$version-$source_commit" '
    select(.workflow_run.id == $run and .name == $name and .expired == false)
    | .digest | select(test("^sha256:[0-9a-f]{64}$"))' <<<"$metadata")" || {
    echo 'ERROR: artifact does not belong to the prepared run, or has expired' >&2; return 1;
  }
  gh api "repos/$GH_REPO/actions/artifacts/$artifact_id/zip" >"$work/prepared.zip"
  [[ "$(hcorral_sha256_file "$work/prepared.zip")" == "${artifact_digest#sha256:}" ]] || { echo 'ERROR: downloaded artifact digest differs' >&2; return 1; }
  # A fresh checkout prevents stale files from supplying missing evidence.
  [[ ! -e dist ]] || { echo 'ERROR: publication requires an empty dist directory' >&2; return 1; }
  unzip -q "$work/prepared.zip" -d dist
  cp "$work/transport/release-transport.env" dist/
  load_and_verify
  HCORRAL_REQUIRE_ACTIONS_TRANSPORT=true verify_actions_transport
  for gate in linux-amd64 linux-arm64 darwin; do
    gh run download "$run_id" --name "qualification-$gate-$source_commit" --dir dist/qualification
  done
  for gate in linux-x11 linux-wayland linux-xwayland; do
    if [[ "$channel" == stable ]]; then
      gh run download "$run_id" --name "qualification-$gate-$source_commit" --dir dist/qualification
    else
      ./scripts/release-qualification.sh --version "$version" --source-commit "$source_commit" \
        --artifacts-sha256 "$(hcorral_state_value "$state_file" ARTIFACTS_SHA256)" \
        --gate "$gate" --status waived-preview --output "dist/qualification/$gate.env"
    fi
  done
  verify_qualifications
  rm -rf -- "$work"
  trap - RETURN
}

publish_actions_release() {
  download_prepared_release "$1"
  require_clean_source
  git fetch origin main --tags
  git merge-base --is-ancestor "$source_commit" origin/main || { echo 'ERROR: prepared source is no longer on main' >&2; exit 1; }
  git config user.name 'github-actions[bot]'
  git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
  if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
    verify_release_tag
  else
    git tag -a "$version" "$source_commit" -m "Harness Corral $version"
    git push origin "refs/tags/$version"
  fi
  record_publication tag "$version@$source_commit"
  mapfile -t release_assets < <(awk -F '\t' '$1 !~ /Formula\/hcorral.rb$/ { print $1 }' "$artifacts_file")
  if gh release view "$version" >/dev/null 2>&1; then
    verify_release_channel
    reconcile_existing_release
  else
    local args=(release create "$version" --verify-tag --title "Harness Corral $version" --generate-notes)
    [[ "$channel" == preview ]] && args+=(--prerelease)
    gh "${args[@]}" "${release_assets[@]}"
    verify_existing_release
  fi
  record_publication github-release "$(gh release view "$version" --json url --jq .url)"
  record_publication complete "$version"
  {
    printf 'Published and byte-verified %s from %s (prepared run %s).\n' "$version" "$source_commit" "$1"
    printf 'Update Homebrew using its own workflow:\n\n```console\n'
    printf 'gh workflow run update-hcorral.yaml --repo infrasecture/homebrew-tap -f version=%s\n```\n' "$version"
  } >>"$GITHUB_STEP_SUMMARY"
}

main() {
  [[ "${GITHUB_ACTIONS:-}" == true && "${GITHUB_REPOSITORY:-}" == infrasecture/hcorral && "${GITHUB_REF_NAME:-}" == main ]] || {
    echo 'ERROR: this entry point is for the main-branch hcorral Actions workflow' >&2; exit 1;
  }
  cd "$root"
  version="${VERSION:?}"; channel="${CHANNEL:?}"
  hcorral_require_stable_version "$version"
  [[ "$channel" == preview || "$channel" == stable ]] || { echo 'ERROR: invalid release channel' >&2; exit 1; }
  export GH_REPO=infrasecture/hcorral
  case "${1:-}" in
    prepare) init_release_state; prepare ;;
    publish) publish_actions_release "${PREPARED_RUN_ID:?}" ;;
    *) echo 'ERROR: expected prepare or publish' >&2; exit 2 ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
