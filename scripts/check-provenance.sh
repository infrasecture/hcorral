#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"

fail() {
  echo "ERROR: provenance: $*" >&2
  exit 1
}

mycodex_commit=cf3ee24af2b077996ac176ffffd1e34e892df061
vaka_commit=409ee53a8072282a2660f3e73ab1f204be17b4a9
tap_start_commit=e39fd9dff3b0d91f277d7c02a598b348e2f10a9a

for commit in "${mycodex_commit}" "${vaka_commit}" "${tap_start_commit}"; do
  grep -Fq -- "${commit}" docs/provenance.md || fail "baseline ${commit} is missing from docs/provenance.md"
done
grep -Fq -- 'discussioncomment-18127443' docs/provenance.md || fail 'Discussion 15 decision URL is missing'
grep -Fq -- "${mycodex_commit}" tests/contract/feature-parity.yaml || fail 'feature parity baseline differs from provenance'

for path in image/Dockerfile image/entrypoint.sh image/session-init.sh scripts/build-harness-image.sh scripts/lib/hcorral-image.sh; do
  grep -Fq -- "\`$path\`" docs/provenance.md || fail "copied/adapted inventory is missing ${path}"
done

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  recorded="$(git ls-files -s -- homebrew-tap | awk '$1 == 160000 { print $2 }')"
  [[ -n "${recorded}" ]] || fail 'homebrew-tap is not a tracked submodule'
fi

echo 'PASS: exact provenance baselines and adapted-file inventory are recorded'
